// Package restore walks a snapshot from a mounted disc image or an
// unpacked NOAHSARK tree, and writes its files, directories and symlinks
// into an output directory. It never reads a staging directory: every
// byte it uses comes from the disc tree the snapshot was written to.
package restore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// Restore reads snapshotID's tree from discRoot and writes it under
// outDir. discRoot is a mounted disc image or an unpacked NOAHSARK tree,
// found the same way image.Read finds it: discRoot itself, or
// discRoot/NOAHSARK. Restore verifies every object's content id before
// it uses the object's bytes.
//
// One disc root is the one-disc case of RestoreMulti, and runs the same
// walk.
func Restore(discRoot string, snapshotID object.ID, outDir string, opts ...Option) (Report, error) {
	return RestoreMulti([]string{discRoot}, snapshotID, outDir, opts...)
}

// RestoreWithProgress is Restore, reporting bytes written through prog.
// A nil prog reports nothing.
func RestoreWithProgress(discRoot string, snapshotID object.ID, outDir string, prog *progress.Reporter, opts ...Option) (Report, error) {
	return RestoreMultiWithProgress([]string{discRoot}, snapshotID, outDir, prog, opts...)
}

// existingFileStatus is the one decision every restore mode uses for a
// destination path that already exists: a regular file whose size
// matches the tree entry, and whose mtime or content also matches, is
// already fully restored (resumed); anything else found at dest is left
// for --overwrite to resolve (not resumed). found is false when dest
// does not exist.
func existingFileStatus(dest string, e format.TreeEntry, entries []placedChunk) (resumed, found bool) {
	fi, err := os.Lstat(dest)
	if err != nil {
		return false, false
	}
	if !fi.Mode().IsRegular() {
		return false, true
	}
	return fileAlreadyRestored(dest, fi, e, entries), true
}

// joinSafe joins name under dir and refuses a result that escapes dir.
// A tree entry name is already validated to hold no '/', '\' or NUL, but
// a root-path TLV carries an arbitrary string read from disk, so this
// check is the one place an escape could otherwise slip through.
func joinSafe(dir, name string) (string, error) {
	dest := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the output directory", name)
	}
	return dest, nil
}

// findNoahsark returns root if it already holds DISC.bin, or
// root/NOAHSARK otherwise, resolving both names case-insensitively
// through cache. It matches image.FindNoahsark's own rule, so Restore
// accepts the same two roots.
func findNoahsark(root string, cache *image.NameCache) (string, error) {
	base, err := image.FindNoahsark(root, cache)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	return base, nil
}

// objectPath returns the on-disc path of id: under snapshots/ for a
// snapshot, or under objects/<fanout>/ for every other kind. The
// fan-out directory and the object's own file name are exact-match
// lowercase; only the objects/snapshots root name is resolved through
// cache.
func objectPath(base string, id object.ID, snapshot bool, cache *image.NameCache) string {
	if snapshot {
		return filepath.Join(cache.Join(base, "snapshots"), id.TextForm())
	}
	return filepath.Join(cache.Join(base, "objects"), id.FanoutByte(), id.TextForm())
}

// readVerified reads id's object file through object.ReadVerified, at the
// path this package's own naming rule gives it.
func readVerified(base string, id object.ID, snapshot bool, cache *image.NameCache) (raw, payload []byte, err error) {
	return object.ReadVerified(objectPath(base, id, snapshot, cache), id)
}
