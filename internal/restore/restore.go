// Package restore writes a snapshot from the catalog and one mounted
// disc at a time into an output directory. It reads every tree and blob
// from the catalog, and every chunk from a disc. It never reads a
// staging directory.
package restore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// existingFileStatus is the one decision for a destination path that
// already exists: a regular file whose size matches the tree entry, and
// whose mtime or content also matches, is already fully restored
// (resumed); anything else found at dest is left for --overwrite to
// resolve (not resumed). found is false when dest does not exist.
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

// findNoahsark returns root if it already holds DISC.bin, or
// root/NOAHSARK otherwise, resolving both names case-insensitively
// through cache.
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
