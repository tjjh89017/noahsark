// Package catalog implements the catalog of a repository, as the
// "Catalog layout" section of OPERATIONS.md gives it. The catalog is the
// permanent history of the repository. It holds a byte copy of each
// snapshot, tree and blob object, and a byte copy of the INDEX, REFS and
// DISCS tables of each disc. It holds no chunk data. No command trims it.
//
// The key of the tables of a disc is the disc uuid, never run_seq: the
// host assigns that number from local state, and after a lost repository
// two discs can carry the same number.
//
// The completeness of each snapshot is in the file
// "state/catalog-state.txt" of the repository, not in the catalog
// directory.
package catalog

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// Directory and file names under the repository directory.
const (
	catalogDirName   = "catalog"
	stateDirName     = "state"
	stateFileName    = "catalog-state.txt"
	discsDirName     = "discs"
	snapshotsDirName = "snapshots"
	treesDirName     = "trees"
	blobsDirName     = "blobs"
)

// IndexFileName, RefsFileName and DiscsFileName are the file names
// WriteDisc writes inside one discs/<disc-uuid>/ directory.
const (
	IndexFileName = "INDEX.bin"
	RefsFileName  = "REFS.bin"
	DiscsFileName = "DISCS.bin"
)

// Catalog is the opened catalog of one repository.
type Catalog struct {
	dir       string
	statePath string
	state     map[string]bool // snapshot id text form -> complete
}

// Dir returns the catalog's root directory.
func (c *Catalog) Dir() string { return c.dir }

// Dir returns the catalog directory of the repository at repoDir.
func Dir(repoDir string) string {
	return filepath.Join(repoDir, catalogDirName)
}

// StatePath returns the path of the completeness file of the repository
// at repoDir.
func StatePath(repoDir string) string {
	return filepath.Join(repoDir, stateDirName, stateFileName)
}

// Open opens the catalog of the repository at repoDir. It creates the
// catalog directory when it does not exist. It reads the completeness
// file when that file exists.
func Open(repoDir string) (*Catalog, error) {
	c, err := OpenReadOnly(repoDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	return c, nil
}

// OpenReadOnly opens the catalog of the repository at repoDir, and
// changes no file or directory. A missing catalog directory reads as an
// empty catalog. A caller that writes the catalog uses Open.
func OpenReadOnly(repoDir string) (*Catalog, error) {
	if repoDir == "" {
		return nil, fmt.Errorf("catalog: repository directory is required")
	}
	c := &Catalog{dir: Dir(repoDir), statePath: StatePath(repoDir)}
	state, err := loadState(c.statePath)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	c.state = state
	return c, nil
}

// discDir returns the directory of the tables of one disc.
func (c *Catalog) discDir(uuid [16]byte) string {
	return filepath.Join(c.dir, discsDirName, uuidText(uuid))
}

// MetaPath returns the file path of a snapshot, tree or blob object in
// the catalog. A tree or a blob goes below a fan-out directory, the
// first two hex digits of the digest, as in staging. MetaPath returns ""
// for a chunk: the catalog holds no chunk.
func (c *Catalog) MetaPath(kind format.ObjectKind, id object.ID) string {
	switch kind {
	case format.ObjectKindSnapshot:
		return filepath.Join(c.dir, snapshotsDirName, id.TextForm())
	case format.ObjectKindTree:
		return filepath.Join(c.dir, treesDirName, id.FanoutByte(), id.TextForm())
	case format.ObjectKindBlob:
		return filepath.Join(c.dir, blobsDirName, id.FanoutByte(), id.TextForm())
	}
	return ""
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
// It does nothing when path already holds exactly data.
func atomicWriteFile(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return replaceFile(path, data)
}

// replaceFile writes data to path through a temporary file in the same
// directory and a rename. It creates the directory when it is absent.
func replaceFile(path string, data []byte) error {
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
