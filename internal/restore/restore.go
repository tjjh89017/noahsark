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
	"syscall"

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

// openForWrite creates dest for a restore write, following the path
// safety rule of OPERATIONS.md's metadata restore policy: without
// --overwrite, an existing path counts as resumed or skipped by
// existingFileStatus, the one rule every restore mode shares; with
// --overwrite, the existing path is unlinked first and then created.
// A file created this way is always new, so O_TRUNC is never needed.
// An unlink that fails, most often because dest is a non-empty
// directory, leaves dest exactly as found and reports it through wp's
// report, instead of stopping the restore.
func openForWrite(dest string, e format.TreeEntry, entries []placedChunk, wp *writePolicy) (f *os.File, skipped bool, err error) {
	if wp != nil && wp.overwrite {
		if _, statErr := os.Lstat(dest); statErr == nil {
			if err := unlinkExisting("file", dest); err != nil {
				wp.blocked("file", dest, err)
				return nil, true, nil
			}
		} else if !os.IsNotExist(statErr) {
			return nil, false, statErr
		}
	} else if resumed, found := existingFileStatus(dest, e, entries); found {
		if resumed {
			wp.resume()
		} else {
			wp.skip(dest)
		}
		return nil, true, nil
	}
	f, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		if os.IsExist(err) {
			// Raced with something else creating dest since the check
			// above; treat it as an ordinary conflict.
			wp.skip(dest)
			return nil, true, nil
		}
		return nil, false, err
	}
	return f, false, nil
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

// readVerified reads id's object file, decompresses its payload, and
// verifies the payload hashes to id. It returns the whole raw file bytes
// (for a typed Decode call) and the decompressed payload separately.
func readVerified(base string, id object.ID, snapshot bool, cache *image.NameCache) (raw, payload []byte, err error) {
	return readVerifiedAt(objectPath(base, id, snapshot, cache), id)
}

// readVerifiedAt is readVerified against an explicit file path, for a
// caller that already knows where an object's file is.
func readVerifiedAt(path string, id object.ID) (raw, payload []byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", id.TextForm(), err)
	}
	var ch format.CommonHeader
	if err := ch.Decode(data); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", id.TextForm(), err)
	}
	if len(data) < format.CommonHeaderLen+format.ObjectHeaderLen {
		return nil, nil, fmt.Errorf("%s: file too short", id.TextForm())
	}
	var oh format.ObjectHeader
	if err := oh.Decode(data[format.CommonHeaderLen:]); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", id.TextForm(), err)
	}
	headerLen := uint64(format.CommonHeaderLen + format.ObjectHeaderLen)
	if uint64(len(data)) < headerLen+oh.StoredLen {
		return nil, nil, fmt.Errorf("%s: file too short", id.TextForm())
	}
	stored := data[headerLen : headerLen+oh.StoredLen]
	payload, err = object.Decompress(stored, oh.Compression, oh.PayloadLen)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", id.TextForm(), err)
	}
	if object.ComputeID(payload) != id {
		return nil, nil, fmt.Errorf("%s: content id does not verify", id.TextForm())
	}
	return data, payload, nil
}
