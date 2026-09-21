package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
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

// cacheSource adapts a *cache.Cache to snapshotSource, so ls, log and
// plan can read a snapshot with no disc root given.
type cacheSource struct {
	c *cache.Cache
}

func (s *cacheSource) Tree(id object.ID) (*format.Tree, error) { return s.c.ReadTree(id) }

func (s *cacheSource) Snapshot(id object.ID) (*format.Snapshot, error) { return s.c.ReadSnapshot(id) }

func (s *cacheSource) Refs() (*format.RefsTable, error) { return s.c.Refs() }

func (s *cacheSource) SnapshotIDs() ([]object.ID, error) { return s.c.ListSnapshots() }

// ParseSnapshotArg resolves arg as a snapshot id, or, failing that, as a
// name in the cached REFS table, the same rule restore.Source uses. A
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
	refs, err := s.c.Refs()
	if err != nil {
		return object.ID{}, err
	}
	for _, r := range refs.Records {
		if string(r.Name[:r.NameLen]) == arg {
			return object.ID(r.SnapshotID), nil
		}
	}
	return object.ID{}, &refNotFoundError{arg: arg}
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
	return &cacheSource{c: c}, c, nil
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
