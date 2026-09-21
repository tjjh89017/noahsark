package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// snapshotSource resolves tree, blob and snapshot objects, and REFS,
// well enough for ls, log and plan to read a snapshot's tree. Both
// *restore.Source (reading one or more disc roots) and *cacheSource
// (reading the local cache, no disc present) implement it.
type snapshotSource interface {
	Tree(object.ID) (*format.Tree, error)
	Snapshot(object.ID) (*format.Snapshot, error)
	Refs() (*format.RefsTable, error)
	SnapshotIDs() ([]object.ID, error)
	ParseSnapshotArg(string) (object.ID, error)
}

// cacheSource resolves a snapshot from the repository alone, with no
// disc present. It looks in the staging store first and in the local
// cache second, so a snapshot that commit has just written lists before
// the first pack. ls, log and plan use it.
type cacheSource struct {
	c          *cache.Cache
	repoDir    string
	stagingDir string
}

// Tree reads one tree object: the staged file first, then the cache. An
// id that neither holds names both places, so the operator knows
// whether to pack or to recover.
func (s *cacheSource) Tree(id object.ID) (*format.Tree, error) {
	if buf, err := os.ReadFile(image.StagedPath(s.stagingDir, id, format.ObjectKindTree)); err == nil {
		var t format.Tree
		if _, err := t.Decode(buf); err != nil {
			return nil, fmt.Errorf("staged tree %s: %w", id.TextForm(), err)
		}
		return &t, nil
	}
	t, err := s.c.ReadTree(id)
	if err != nil {
		return nil, &notHeldError{kind: "tree", id: id}
	}
	return t, nil
}

// Snapshot reads one snapshot object: the staged file first, then the
// cache.
func (s *cacheSource) Snapshot(id object.ID) (*format.Snapshot, error) {
	if buf, err := os.ReadFile(image.StagedPath(s.stagingDir, id, format.ObjectKindSnapshot)); err == nil {
		var snap format.Snapshot
		if _, err := snap.Decode(buf); err != nil {
			return nil, fmt.Errorf("staged snapshot %s: %w", id.TextForm(), err)
		}
		return &snap, nil
	}
	snap, err := s.c.ReadSnapshot(id)
	if err != nil {
		return nil, &notHeldError{kind: "snapshot", id: id}
	}
	return snap, nil
}

// Refs merges the repository's own ref file over the cached REFS table.
// The ref file is the authoritative local state: commit writes it, and
// a pack has not yet carried the newest names into any disc's REFS.
func (s *cacheSource) Refs() (*format.RefsTable, error) {
	cached, cacheErr := s.c.Refs()
	local, localErr := readRefs(s.repoDir)
	if cacheErr != nil && (localErr != nil || len(local) == 0) {
		// Nothing local and nothing cached: the repository holds no ref
		// at all, and the cache's own message names the fix.
		return nil, cacheErr
	}
	merged := cached
	if merged == nil {
		merged = &format.RefsTable{}
	}
	if localErr != nil {
		return merged, nil
	}
	byName := make(map[string]int, len(merged.Records))
	for i, r := range merged.Records {
		byName[string(r.Name[:r.NameLen])] = i
	}
	for name, text := range local {
		id, err := parseSnapshotID(text)
		if err != nil {
			continue
		}
		rec := format.RefRecord{SnapshotID: id, NameLen: uint16(min(len(name), format.RefNameLen))}
		copy(rec.Name[:], name)
		if snap, err := s.Snapshot(id); err == nil {
			rec.TimeSec, rec.TimeNsec = snap.TimeSec, snap.TimeNsec
		}
		if i, ok := byName[name]; ok {
			merged.Records[i] = rec
			continue
		}
		merged.Records = append(merged.Records, rec)
	}
	sort.Slice(merged.Records, func(i, j int) bool {
		a, b := merged.Records[i], merged.Records[j]
		return string(a.Name[:a.NameLen]) < string(b.Name[:b.NameLen])
	})
	merged.RecordCount = uint64(len(merged.Records))
	return merged, nil
}

// SnapshotIDs lists every snapshot the repository knows: the staged
// ones and the cached ones, deduplicated.
func (s *cacheSource) SnapshotIDs() ([]object.ID, error) {
	seen := make(map[object.ID]bool)
	var ids []object.ID
	add := func(id object.ID) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.stagingDir, "snapshots"))
	if err == nil {
		for _, e := range entries {
			if id, err := object.ParseID(e.Name()); err == nil {
				add(id)
			}
		}
	}
	cached, err := s.c.ListSnapshots()
	if err != nil && len(ids) == 0 {
		return nil, err
	}
	for _, id := range cached {
		add(id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].TextForm() < ids[j].TextForm() })
	return ids, nil
}

// notHeldError reports an object that neither the staging store nor the
// local cache holds. It names both places, because the fix differs: an
// object that was never packed waits for pack, and one that was packed
// and freed comes back with recover.
type notHeldError struct {
	kind string
	id   object.ID
}

func (e *notHeldError) Error() string {
	return fmt.Sprintf("%s %s is neither staged nor cached; run pack, or run recover with the disc that holds it", e.kind, e.id.TextForm())
}

// ParseSnapshotArg resolves arg as a snapshot id, or, failing that, as a
// name in the merged ref set, the same rule restore.Source uses. A
// ref found in neither is reported as a *refNotFoundError, so a caller
// that knows discs were named on the command line (restore --mount) can
// reword the message; ls, log and plan, which never name a disc here,
// print it as returned.
func (s *cacheSource) ParseSnapshotArg(arg string) (object.ID, error) {
	if arg == "" {
		return object.ID{}, restore.ErrNoSnapshotArg
	}
	if id, err := object.ParseID(arg); err == nil {
		return id, nil
	}
	refs, err := s.Refs()
	if err != nil {
		return object.ID{}, &cacheReadError{err: err}
	}
	for _, r := range refs.Records {
		if string(r.Name[:r.NameLen]) == arg {
			return object.ID(r.SnapshotID), nil
		}
	}
	return object.ID{}, &refNotFoundError{arg: arg}
}

// cacheReadError marks a snapshot argument that did not resolve because
// the cache itself could not be read: an empty cache holds no REFS
// table. The argument may well be good, thus this is a failure at run
// time, not a usage error.
type cacheReadError struct{ err error }

func (e *cacheReadError) Error() string { return e.err.Error() }

func (e *cacheReadError) Unwrap() error { return e.err }

// exitForSnapshotArg gives the exit code for a SNAPSHOT argument that
// did not resolve. A malformed id, and a name that matches no ref, are
// usage errors, code 2. A cache that could not be read is a failure at
// run time, code 1, the code every other read failure takes; ls and log
// then report an empty cache the same way.
func exitForSnapshotArg(err error) int {
	if _, ok := errors.AsType[*cacheReadError](err); ok {
		return 1
	}
	return 2
}

// refNotFoundError reports that arg matched no snapshot id and no name
// in a *cacheSource's cached REFS table.
type refNotFoundError struct{ arg string }

func (e *refNotFoundError) Error() string {
	if restore.LooksLikeSnapshotIDPrefix(e.arg) {
		return fmt.Sprintf("%q looks like a snapshot id prefix; give the full snapshot id from noahsark log", e.arg)
	}
	return fmt.Sprintf("%q is neither a snapshot id nor a known ref name", e.arg)
}

// openCacheSource opens the local cache for the repository repoFlag
// names, or the usual discovery order when repoFlag is empty, and
// returns it wrapped as a snapshotSource plus the *cache.Cache itself,
// so a caller can also call CheckComplete on it.
func openCacheSource(repoFlag string) (*cacheSource, *cache.Cache, error) {
	repoDir, err := discoverRepo(repoFlag)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		return nil, nil, err
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return nil, nil, err
	}
	dir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		return nil, nil, err
	}
	c, err := cache.Open(dir)
	if err != nil {
		return nil, nil, err
	}
	return &cacheSource{c: c, repoDir: repoDir, stagingDir: cfg.StagingDir}, c, nil
}

// looksLikeDiscRoot reports whether s names an existing directory: a
// disc root is always a real directory on this host, a mounted disc or
// an unpacked NOAHSARK tree, so this tells a DISC-ROOT positional
// argument apart from a SNAPSHOT id or ref name, neither of which is
// ever also an existing directory in ordinary use.
func looksLikeDiscRoot(s string) bool {
	info, err := os.Stat(s)
	return err == nil && info.IsDir()
}

// looksLikePathNotDisc reports whether s was plainly meant as a path,
// even though looksLikeDiscRoot says it is not a directory: it contains
// a slash, or it exists as a file. A SNAPSHOT id or ref name never
// contains a slash and never already exists as a file, so this tells
// apart a mistyped or missing DISC-ROOT from an ordinary SNAPSHOT
// argument, letting log and ls report the mistake by name instead of
// falling into cache mode and resolving it as a ref.
func looksLikePathNotDisc(s string) bool {
	if strings.ContainsRune(s, '/') {
		return true
	}
	info, err := os.Stat(s)
	return err == nil && !info.IsDir()
}

// formatIncompleteError renders a *cache.IncompleteError the way ls,
// log and plan all report it: naming the snapshot and, when it can be
// resolved, the disc to insert and the command that would fix it.
func formatIncompleteError(cmd string, e *cache.IncompleteError) string {
	return fmt.Sprintf("noahsark: %s: %s", cmd, incompleteErrorBody(e))
}

// incompleteErrorBody renders a *cache.IncompleteError with no
// "noahsark: <cmd>:" prefix, for a caller that wraps it inside its own
// already-prefixed message instead of printing it standalone.
func incompleteErrorBody(e *cache.IncompleteError) string {
	if e.HasDiscUUID {
		return fmt.Sprintf("snapshot %s is not complete in the cache; insert disc %s%s and run recover",
			e.Snapshot.TextForm(), uuidText(e.DiscUUID), cache.LabelSuffix(e.Label))
	}
	return fmt.Sprintf("snapshot %s is not complete in the cache; run recover with the disc that holds it",
		e.Snapshot.TextForm())
}

// reportSourceError prints err the way ls, log and plan all report a
// read failure. An incomplete cached snapshot and a missing disc are
// both a failure at run time, exit code 1, the same as any other read
// failure here; a missing disc or an incomplete cache is not a bad
// argument, so it never takes the usage-error code. c is nil in disc
// mode.
func reportSourceError(cmd string, stderr io.Writer, err error, c *cache.Cache, snapID object.ID) int {
	if c != nil {
		if ce := c.CheckComplete(snapID); ce != nil {
			if ie, ok := errors.AsType[*cache.IncompleteError](ce); ok {
				_, _ = fmt.Fprintln(stderr, formatIncompleteError(cmd, ie))
				return 1
			}
		}
	}
	_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
	return 1
}
