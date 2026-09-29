package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// catalogSource resolves a snapshot from the repository alone, with no
// disc present: from the catalog and the local ref file. commit writes
// every snapshot, tree and blob object into the catalog, thus a
// snapshot lists before the first pack. ls, log and restore use it.
type catalogSource struct {
	c        *catalog.Catalog
	refsPath string
}

// Tree reads one tree object from the catalog.
func (s *catalogSource) Tree(id object.ID) (*format.Tree, error) {
	t, err := s.c.ReadTree(id)
	if err != nil {
		return nil, &notHeldError{kind: "tree", id: id}
	}
	return t, nil
}

// Snapshot reads one snapshot object from the catalog.
func (s *catalogSource) Snapshot(id object.ID) (*format.Snapshot, error) {
	snap, err := s.c.ReadSnapshot(id)
	if err != nil {
		return nil, &notHeldError{kind: "snapshot", id: id}
	}
	return snap, nil
}

// Refs merges the local ref file over the REFS tables of every disc in
// the catalog. For each name, the newest record wins. A line of the
// local ref file has no time of its own: it takes the time of its
// snapshot, or no time when the catalog does not hold that snapshot.
// With no disc in the catalog and no local ref, it returns
// catalog.ErrNoDisc.
func (s *catalogSource) Refs() (*format.RefsTable, error) {
	newest, err := s.c.MergedRefs()
	if err != nil && !errors.Is(err, catalog.ErrNoDisc) {
		return nil, err
	}
	local, localErr := readRefs(s.refsPath)
	if localErr != nil {
		return nil, localErr
	}
	if err != nil && len(local) == 0 {
		return nil, err
	}
	if newest == nil {
		newest = make(map[string]format.RefRecord, len(local))
	}
	for name, text := range local {
		id, err := parseSnapshotID(text)
		if err != nil || len(name) > format.RefNameLen {
			continue
		}
		catalog.MergeRef(newest, localRefRecord(s.c, name, id))
	}
	names := slices.Sorted(maps.Keys(newest))
	merged := &format.RefsTable{RecordCount: uint64(len(names)), Records: make([]format.RefRecord, 0, len(names))}
	for _, name := range names {
		merged.Records = append(merged.Records, newest[name])
	}
	return merged, nil
}

// SnapshotIDs lists every snapshot of the catalog.
func (s *catalogSource) SnapshotIDs() ([]object.ID, error) {
	return s.c.ListSnapshots()
}

// errNoSnapshotArg reports that the snapshot argument is empty. It has
// its own message, because an empty name quoted back at the operator
// names nothing. Every command that takes a snapshot reports it.
var errNoSnapshotArg = errors.New("no snapshot given; name a ref, or a snapshot id from noahsark log")

// notHeldError reports an object that the catalog does not hold. recover
// with the disc that holds it gives it back.
type notHeldError struct {
	kind string
	id   object.ID
}

func (e *notHeldError) Error() string {
	return fmt.Sprintf("%s %s is not in the catalog; run recover with the disc that holds it", e.kind, e.id.TextForm())
}

// ParseSnapshotArg resolves a SNAPSHOT argument. A ref name wins, also
// when the name is a valid id prefix. Else arg is the full text form of
// an id, or a unique prefix of the hexadecimal digest in any letter
// case. The candidates of a prefix are the snapshot file names of the
// catalog and the snapshots that the refs name. It reads no snapshot
// object.
func (s *catalogSource) ParseSnapshotArg(arg string) (object.ID, error) {
	if arg == "" {
		return object.ID{}, errNoSnapshotArg
	}
	refs, refsErr := s.Refs()
	if refsErr != nil && !errors.Is(refsErr, catalog.ErrNoDisc) {
		return object.ID{}, &catalogReadError{err: refsErr}
	}
	var candidates []object.ID
	if refsErr == nil {
		for _, r := range refs.Records {
			if catalog.RefName(r) == arg {
				return object.ID(r.SnapshotID), nil
			}
			candidates = append(candidates, object.ID(r.SnapshotID))
		}
	}
	if id, err := object.ParseID(strings.ToLower(arg)); err == nil {
		return id, nil
	}
	ids, err := s.c.ListSnapshots()
	if err != nil {
		return object.ID{}, &catalogReadError{err: err}
	}
	candidates = append(candidates, ids...)
	matches := matchDigestPrefix(arg, candidates)
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if refsErr != nil {
			// The empty catalog is the reason that nothing matches.
			return object.ID{}, &catalogReadError{err: refsErr}
		}
		return object.ID{}, &refNotFoundError{arg: arg}
	}
	return object.ID{}, &ambiguousSnapshotError{arg: arg, candidates: matches}
}

// matchDigestPrefix returns each id of ids, once and in text form order,
// whose hexadecimal digest starts with arg in any letter case. A value
// that is not 1 to 64 hexadecimal characters matches nothing.
func matchDigestPrefix(arg string, ids []object.ID) []object.ID {
	prefix := strings.ToLower(arg)
	if prefix == "" || len(prefix) > 2*len(object.ID{}) {
		return nil
	}
	if strings.Trim(prefix, "0123456789abcdef") != "" {
		return nil
	}
	var matches []object.ID
	for _, id := range ids {
		if strings.HasPrefix(hex.EncodeToString(id[:]), prefix) && !slices.Contains(matches, id) {
			matches = append(matches, id)
		}
	}
	slices.SortFunc(matches, func(a, b object.ID) int { return bytes.Compare(a[:], b[:]) })
	return matches
}

// shortID gives the print form of a snapshot id: the first 12
// hexadecimal characters of its digest, without the multihash prefix.
// A file of state/ and a damaged line use the full text form.
func shortID(id object.ID) string {
	return hex.EncodeToString(id[:6])
}

// ambiguousSnapshotError reports a prefix that matches more than one
// snapshot. It lists each candidate in its full text form.
type ambiguousSnapshotError struct {
	arg        string
	candidates []object.ID
}

func (e *ambiguousSnapshotError) Error() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s matches more than one snapshot:", e.arg)
	for _, id := range e.candidates {
		_, _ = fmt.Fprintf(&b, "\nsnapshot %s", id.TextForm())
	}
	return b.String()
}

// catalogReadError marks a snapshot argument that did not resolve because
// the catalog itself could not be read: an empty catalog holds no REFS
// table. The argument may well be good, thus this is a failure at run
// time, not a usage error.
type catalogReadError struct{ err error }

func (e *catalogReadError) Error() string { return e.err.Error() }

func (e *catalogReadError) Unwrap() error { return e.err }

// exitForSnapshotArg gives the exit code for a SNAPSHOT argument that
// did not resolve. A malformed id, and a name that matches no ref, are
// usage errors, code 2. A catalog that could not be read is a failure at
// run time, code 1, the code every other read failure takes; ls and log
// then report an empty catalog the same way.
func exitForSnapshotArg(err error) int {
	if _, ok := errors.AsType[*catalogReadError](err); ok {
		return 1
	}
	return 2
}

// refNotFoundError reports that arg matched no ref name and no
// snapshot id of the catalog.
type refNotFoundError struct{ arg string }

func (e *refNotFoundError) Error() string {
	return fmt.Sprintf("no snapshot matches %s", e.arg)
}
