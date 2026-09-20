package restore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// Source resolves tree and snapshot objects across the disc roots it was
// given, the same way RestoreMulti locates the objects a restore needs.
// It never writes anything; ls and log use it as a read-only view over
// one or more mounted disc roots.
type Source struct {
	src *multiSource
}

// OpenSource resolves every disc root in discRoots, the same way
// RestoreMulti does: it reads each root's DISC.bin, INDEX.bin and
// DISCS.bin, so a later miss can name the disc a needed object lives on.
func OpenSource(discRoots []string) (*Source, error) {
	src, err := newMultiSource(discRoots)
	if err != nil {
		return nil, err
	}
	return &Source{src: src}, nil
}

// Tree reads and decodes the tree object id, trying every provided disc
// root in turn. A tree that lives only on a disc not provided is
// reported as a *MissingDiscError, the same one Restore would report.
func (s *Source) Tree(id object.ID) (*format.Tree, error) {
	raw, _, ok := s.src.read(id, false)
	if !ok {
		return nil, s.src.finalError()
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return nil, fmt.Errorf("tree %s: %w", id.TextForm(), err)
	}
	return &t, nil
}

// Snapshot reads and decodes the snapshot object id, the same way Tree
// resolves a tree object.
func (s *Source) Snapshot(id object.ID) (*format.Snapshot, error) {
	raw, _, ok := s.src.read(id, true)
	if !ok {
		return nil, s.src.finalError()
	}
	var snap format.Snapshot
	if _, err := snap.Decode(raw); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", id.TextForm(), err)
	}
	return &snap, nil
}

// Refs reads REFS from every provided disc and merges the results by
// ref name: when two discs disagree on a name, the record with the
// higher run_seq wins. A pack that has not yet carried an older disc's
// ref forward can still leave a disc's REFS short of the full name set,
// so Refs does not trust any one disc's copy alone.
func (s *Source) Refs() (*format.RefsTable, error) {
	var merged *format.RefsTable
	byName := make(map[string]format.RefRecord)
	for _, b := range s.src.bases {
		catalogDir := s.src.names.Join(b.runDir, "catalog")
		buf, err := os.ReadFile(filepath.Join(catalogDir, s.src.names.Resolve(catalogDir, "REFS.bin")))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", catalogDir, err)
		}
		var refs format.RefsTable
		if _, err := refs.Decode(buf); err != nil {
			return nil, fmt.Errorf("%s: %w", catalogDir, err)
		}
		if merged == nil {
			merged = &format.RefsTable{Header: refs.Header, RepoUUID: refs.RepoUUID, RecordSize: refs.RecordSize, HashAlgo: refs.HashAlgo, DigestLen: refs.DigestLen}
		}
		for _, r := range refs.Records {
			name := string(r.Name[:r.NameLen])
			cur, ok := byName[name]
			if !ok || r.RunSeq > cur.RunSeq {
				byName[name] = r
			}
		}
	}
	if merged == nil {
		return nil, fmt.Errorf("no disc root provided")
	}
	records := make([]format.RefRecord, 0, len(byName))
	for _, r := range byName {
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		return string(a.Name[:a.NameLen]) < string(b.Name[:b.NameLen])
	})
	merged.Records = records
	merged.RecordCount = uint64(len(records))
	return merged, nil
}

// SnapshotIDs returns the content id of every snapshot object the
// provided discs know, deduplicated. A snapshot's canonical copy is
// written only to the disc that packed it, but its catalog/snapobj copy
// is replicated in full on every run, so scanning every provided disc's
// catalog/snapobj directory finds every snapshot without needing the
// disc that packed each one.
func (s *Source) SnapshotIDs() ([]object.ID, error) {
	seen := make(map[object.ID]bool)
	var ids []object.ID
	for _, b := range s.src.bases {
		dir := s.src.names.Join(b.runDir, "catalog", "snapobj")
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			id, err := object.ParseID(e.Name())
			if err != nil {
				continue
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].TextForm() < ids[j].TextForm() })
	return ids, nil
}

// ErrNoSnapshotArg reports that the snapshot argument is empty. It has
// its own message, because an empty name quoted back at the operator
// names nothing. Every command that takes a snapshot reports it.
var ErrNoSnapshotArg = errors.New("no snapshot given; name a ref, or a snapshot id from noahsark log")

// ParseSnapshotArg resolves arg as a snapshot id: either a multihash text
// id, or a name found in REFS. An empty arg, a ref that does not
// resolve, or a malformed id, is reported as an error naming what the
// operator typed. A ref not among the
// provided discs' merged REFS is reported as not on those discs, since a
// later disc in the chain, not given here, may carry it, unless arg
// itself looks like a truncated snapshot id, which is never a valid ref
// name and is reported as that instead.
func (s *Source) ParseSnapshotArg(arg string) (object.ID, error) {
	if arg == "" {
		return object.ID{}, ErrNoSnapshotArg
	}
	if id, err := object.ParseID(arg); err == nil {
		return id, nil
	}
	refs, err := s.Refs()
	if err != nil {
		return object.ID{}, err
	}
	for _, r := range refs.Records {
		if string(r.Name[:r.NameLen]) == arg {
			return object.ID(r.SnapshotID), nil
		}
	}
	if LooksLikeSnapshotIDPrefix(arg) {
		return object.ID{}, fmt.Errorf("%q looks like a snapshot id prefix; give the full snapshot id from noahsark log", arg)
	}
	return object.ID{}, fmt.Errorf("ref %q is not on the provided disc(s); a later disc in the chain may carry it", arg)
}

// LooksLikeSnapshotIDPrefix reports whether arg is plausibly a
// truncated snapshot id: 8 or more hex characters. Nothing resolves
// arg by this alone; a caller that has already failed to resolve arg
// as a full snapshot id or as a ref name uses it only to choose which
// of two error messages to report, never to look up a snapshot by
// prefix.
func LooksLikeSnapshotIDPrefix(arg string) bool {
	if len(arg) < 8 {
		return false
	}
	for _, r := range arg {
		if !isHexDigit(r) {
			return false
		}
	}
	return true
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
