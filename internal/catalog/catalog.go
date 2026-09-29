// Package catalog implements the catalog OPERATIONS.md describes in
// its catalog layout section. Everything the catalog holds is derived
// from a disc or from staging and is rebuildable; a command must behave
// the same, apart from speed, with the catalog deleted. The catalog never
// holds anything whose loss loses archive data.
//
// One structure, INDEX, carries a run's index and its catalog. This
// package stores that file, byte for byte, as
// "discs/<disc-uuid>/INDEX.bin", beside that disc's own copy of
// REFS.bin and DISCS.bin. One disc holds one run, thus the disc uuid
// identifies the run too. The key is never run_seq: the host assigns
// that number from local state, and after a lost repository two discs
// can carry the same number.
//
// The catalog also holds every blob object reachable from a stored
// snapshot, under "blobs/<id>", alongside "trees/<id>": a blob is small,
// tree-sized metadata, the ordered chunk id list of one file, not the
// chunk data itself. plan reads it to resolve a file down to the chunk
// ids a restore needs, with no disc present. A chunk's own bulk payload
// is never stored.
package catalog

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Directory and file names under a catalog directory.
const (
	discsDirName     = "discs"
	snapshotsDirName = "snapshots"
	treesDirName     = "trees"
	blobsDirName     = "blobs"
	stateFileName    = "state.txt"
)

// IndexFileName, RefsFileName and DiscsFileName are the file names
// WriteDisc writes inside one discs/<disc-uuid>/ directory.
const (
	IndexFileName = "INDEX.bin"
	RefsFileName  = "REFS.bin"
	DiscsFileName = "DISCS.bin"
)

// Catalog is one opened catalog directory.
type Catalog struct {
	dir   string
	state map[string]bool // snapshot id text form -> complete
}

// Dir returns the catalog's root directory.
func (c *Catalog) Dir() string { return c.dir }

// Dir returns the catalog directory for the repository at repoDir: always
// "catalog" inside the repository directory, beside "staging" and the
// config file.
func Dir(repoDir string) string {
	return filepath.Join(repoDir, "cache")
}

// Open opens the catalog directory at dir, creating it and its state file
// when they do not exist yet. dir is normally ResolveDir's result.
func Open(dir string) (*Catalog, error) {
	if dir == "" {
		return nil, fmt.Errorf("cache: directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	c := &Catalog{dir: dir}
	state, err := loadState(c.statePath())
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	c.state = state
	return c, nil
}

// discDir returns the catalog directory for one disc's catalog copy.
func (c *Catalog) discDir(uuid [16]byte) string {
	return filepath.Join(c.dir, discsDirName, uuidText(uuid))
}

// snapshotsDir returns the directory holding catalog snapshot objects.
func (c *Catalog) snapshotsDir() string {
	return filepath.Join(c.dir, snapshotsDirName)
}

// treesDir returns the directory holding catalog tree objects.
func (c *Catalog) treesDir() string {
	return filepath.Join(c.dir, treesDirName)
}

// blobsDir returns the directory holding catalog blob objects. A blob
// carries only an ordered chunk id list, the same small, tree-sized
// metadata as a tree object; the catalog holds it for the same reason it
// holds trees, so plan can resolve a file's chunk ids without a disc.
// A chunk's own bulk payload is never stored.
func (c *Catalog) blobsDir() string {
	return filepath.Join(c.dir, blobsDirName)
}

// statePath returns the path of the snapshot completeness record.
func (c *Catalog) statePath() string {
	return filepath.Join(c.dir, stateFileName)
}

// uuidText formats a 16-byte uuid as hyphenated lowercase text, the
// standard uuid text form.
func uuidText(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// parseUUIDText parses the hyphenated lowercase text form uuidText
// writes. A directory name it cannot parse is not a catalog disc.
func parseUUIDText(s string) ([16]byte, bool) {
	var u [16]byte
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		return u, false
	}
	copy(u[:], b)
	if uuidText(u) != s {
		return u, false
	}
	return u, true
}

// atomicWriteFile writes data to path through a temporary file and a
// rename, so a crash or a concurrent reader never sees a partial file.
// It does nothing when path already holds exactly data, so repeated
// writes of the same bytes (a pack that carries the same snapshot or
// tree forward, or a recover run over an already-catalog disc)
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
