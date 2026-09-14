// Package cache implements the local cache: OPERATIONS.md "2.4 Local
// cache layout". Everything the cache holds is derived from a disc or
// from staging and is rebuildable; a command must behave the same,
// apart from speed, with the cache deleted. The cache never holds
// anything whose loss loses archive data.
//
// This build's layout renames one item from OPERATIONS.md's own table.
// OPERATIONS.md names a per-run "manifests/<seq>.bin" file, copied as
// discs are mounted. FORMAT.md's "The run index and the catalog"
// replaced the manifest, the filter, the layout table and the catalog
// container with one structure, INDEX. This package stores that file,
// byte for byte, as "runs/<seq>/INDEX.bin", beside that run's own copy
// of REFS.bin and DISCS.bin.
package cache

import (
	"fmt"
	"os"
	"path/filepath"
)

// Directory and file names under a cache directory.
const (
	runsDirName      = "runs"
	snapshotsDirName = "snapshots"
	treesDirName     = "trees"
	stateFileName    = "state.txt"
)

// IndexFileName, RefsFileName and DiscsFileName are the file names
// WriteRun writes inside one runs/<seq>/ directory.
const (
	IndexFileName = "INDEX.bin"
	RefsFileName  = "REFS.bin"
	DiscsFileName = "DISCS.bin"
)

// Cache is one opened local cache directory.
type Cache struct {
	dir   string
	state map[string]bool // snapshot id text form -> complete
}

// Dir returns the cache's root directory.
func (c *Cache) Dir() string { return c.dir }

// ResolveDir returns the cache directory for repoUUID: override when
// set (cache.dir in the configuration reference), else
// $XDG_CACHE_HOME/noahsark/<repo-uuid>/, falling back to
// ~/.cache/noahsark/<repo-uuid>/.
func ResolveDir(repoUUID [16]byte, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cache: resolve directory: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "noahsark", uuidText(repoUUID)), nil
}

// Open opens the cache directory at dir, creating it and its state file
// when they do not exist yet. dir is normally ResolveDir's result.
func Open(dir string) (*Cache, error) {
	if dir == "" {
		return nil, fmt.Errorf("cache: directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	c := &Cache{dir: dir}
	state, err := loadState(c.statePath())
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	c.state = state
	return c, nil
}

// runDir returns the cache directory for one run's catalog copy.
func (c *Cache) runDir(seq uint64) string {
	return filepath.Join(c.dir, runsDirName, fmt.Sprintf("%d", seq))
}

// snapshotsDir returns the directory holding cached snapshot objects.
func (c *Cache) snapshotsDir() string {
	return filepath.Join(c.dir, snapshotsDirName)
}

// treesDir returns the directory holding cached tree objects.
func (c *Cache) treesDir() string {
	return filepath.Join(c.dir, treesDirName)
}

// statePath returns the path of the snapshot completeness record.
func (c *Cache) statePath() string {
	return filepath.Join(c.dir, stateFileName)
}

// uuidText formats a 16-byte uuid as hyphenated lowercase text, the
// standard uuid text form.
func uuidText(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// atomicWriteFile writes data to path through a temporary file and a
// rename, so a crash or a concurrent reader never sees a partial file.
// It does nothing when path already holds exactly data, so repeated
// writes of the same bytes (a pack that carries the same snapshot or
// tree forward, or a rebuild-cache run over an already-cached disc)
// touch the filesystem only once.
func atomicWriteFile(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(data) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
