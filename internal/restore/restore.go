// Package restore walks a snapshot from a mounted disc image or an
// unpacked NOAHSARK tree and writes its files, directories and symlinks
// into an output directory. It never reads a staging directory: every
// byte it uses comes from the disc tree the snapshot was written to.
package restore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// Restore reads snapshotID's tree from discRoot and writes it under
// outDir. discRoot is a mounted disc image or an unpacked NOAHSARK tree,
// found the same way image.Read finds it: discRoot itself, or
// discRoot/NOAHSARK. Restore verifies every object's content id before
// using its bytes; a chunk, blob, tree or snapshot that does not verify
// is a hard error.
func Restore(discRoot string, snapshotID object.ID, outDir string) error {
	return RestoreWithProgress(discRoot, snapshotID, outDir, nil)
}

// RestoreWithProgress is Restore, reporting bytes written through prog.
// A nil prog reports nothing. The total is the sum of every regular
// file's recorded size in the snapshot's tree, found by a pass over the
// tree objects alone, before any file content is read or written.
func RestoreWithProgress(discRoot string, snapshotID object.ID, outDir string, prog *progress.Reporter) error {
	cache := image.NewNameCache()
	base, err := findNoahsark(discRoot, cache)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}

	snapRaw, _, err := readVerified(base, snapshotID, true, cache)
	if err != nil {
		return err
	}
	var snap format.Snapshot
	if _, err := snap.Decode(snapRaw); err != nil {
		return fmt.Errorf("restore: snapshot %s: %w", snapshotID.TextForm(), err)
	}

	rootRaw, _, err := readVerified(base, object.ID(snap.RootTree), false, cache)
	if err != nil {
		return err
	}
	var rootTree format.Tree
	if _, err := rootTree.Decode(rootRaw); err != nil {
		return fmt.Errorf("restore: tree %s: %w", object.ID(snap.RootTree).TextForm(), err)
	}

	var total uint64
	for _, e := range rootTree.Entries {
		if e.EntryType == format.EntryTypeDirectory {
			total += sumRegularSizes(base, object.ID(e.ContentID), cache)
		}
	}
	prog.Start("restore: bytes written", int64(total))
	defer prog.Done()

	for _, e := range rootTree.Entries {
		if err := restoreRootEntry(base, absOut, e, prog, cache); err != nil {
			return err
		}
	}
	return nil
}

// sumRegularSizes recursively sums every regular file entry's recorded
// size under treeID, reading only tree objects, never file content. A
// tree that fails to read or decode contributes zero rather than failing
// the whole progress total: the real restore below is what reports any
// such error properly.
func sumRegularSizes(base string, treeID object.ID, cache *image.NameCache) uint64 {
	raw, _, err := readVerified(base, treeID, false, cache)
	if err != nil {
		return 0
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return 0
	}
	var total uint64
	for _, e := range t.Entries {
		switch e.EntryType {
		case format.EntryTypeDirectory:
			total += sumRegularSizes(base, object.ID(e.ContentID), cache)
		case format.EntryTypeRegular:
			total += e.Size
		}
	}
	return total
}

// restoreRootEntry restores one entry of the synthetic root tree. Its
// destination is the source's own absolute path, carried in the
// entry's root-path TLV, joined under outDir; its content is the
// entry's own directory tree, restored directly into that destination
// rather than one level below it.
func restoreRootEntry(base, outDir string, e format.TreeEntry, prog *progress.Reporter, cache *image.NameCache) error {
	if e.EntryType != format.EntryTypeDirectory {
		return fmt.Errorf("restore: root entry %q: expected a directory", e.Name)
	}
	rootPath := ""
	for _, t := range e.TLVs {
		if t.Type == format.TLVTypeRootPath {
			rootPath = string(t.Payload)
		}
	}
	if rootPath == "" {
		return fmt.Errorf("restore: root entry %q: no root path TLV", e.Name)
	}
	dest, err := joinSafe(outDir, rootPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if err := restoreDirContents(base, object.ID(e.ContentID), dest, prog, cache); err != nil {
		return err
	}
	applyMetadata(dest, e)
	return nil
}

// restoreDirContents decodes the tree at treeID and restores every entry
// as a child of dest, which already exists.
func restoreDirContents(base string, treeID object.ID, dest string, prog *progress.Reporter, cache *image.NameCache) error {
	raw, _, err := readVerified(base, treeID, false, cache)
	if err != nil {
		return err
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return fmt.Errorf("restore: tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range t.Entries {
		if err := restoreEntry(base, dest, e, prog, cache); err != nil {
			return err
		}
	}
	return nil
}

// restoreEntry writes one tree entry as a child of dir.
func restoreEntry(base, dir string, e format.TreeEntry, prog *progress.Reporter, cache *image.NameCache) error {
	name := string(e.Name)
	child, err := joinSafe(dir, name)
	if err != nil {
		return err
	}
	switch e.EntryType {
	case format.EntryTypeDirectory:
		if err := os.MkdirAll(child, 0o755); err != nil {
			return err
		}
		if err := restoreDirContents(base, object.ID(e.ContentID), child, prog, cache); err != nil {
			return err
		}
		applyMetadata(child, e)
		return nil
	case format.EntryTypeRegular:
		if err := restoreFile(base, child, object.ID(e.ContentID), prog, cache); err != nil {
			return err
		}
		applyMetadata(child, e)
		return nil
	case format.EntryTypeSymlink:
		target := ""
		for _, t := range e.TLVs {
			if t.Type == format.TLVTypeSymlinkTarget {
				target = string(t.Payload)
			}
		}
		if target == "" {
			return fmt.Errorf("restore: symlink %q has no target TLV", name)
		}
		if err := os.RemoveAll(child); err != nil {
			return err
		}
		return os.Symlink(target, child)
	default:
		return fmt.Errorf("restore: entry %q: entry type %d is not restored", name, e.EntryType)
	}
}

// restoreFile reassembles blobID's chunks into dest, in blob entry order,
// verifying every chunk's content id before writing its bytes.
func restoreFile(base, dest string, blobID object.ID, prog *progress.Reporter, cache *image.NameCache) error {
	raw, _, err := readVerified(base, blobID, false, cache)
	if err != nil {
		return err
	}
	var blob format.Blob
	if _, err := blob.Decode(raw); err != nil {
		return fmt.Errorf("restore: blob %s: %w", blobID.TextForm(), err)
	}

	entries := append([]format.BlobEntry(nil), blob.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].FileOffset < entries[j].FileOffset })

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	for _, be := range entries {
		_, payload, err := readVerified(base, object.ID(be.ContentID), false, cache)
		if err != nil {
			return err
		}
		if uint64(len(payload)) != be.Length {
			return fmt.Errorf("restore: chunk %s: length %d, blob entry says %d",
				object.ID(be.ContentID).TextForm(), len(payload), be.Length)
		}
		if _, err := f.WriteAt(payload, int64(be.FileOffset)); err != nil {
			return err
		}
		prog.Add(int64(len(payload)))
	}
	return nil
}

// applyMetadata sets mode and mtime from e. Ownership is applied best
// effort and never fails the restore.
func applyMetadata(dest string, e format.TreeEntry) {
	_ = os.Chmod(dest, os.FileMode(e.Mode&0o7777))
	mtime := time.Unix(e.MtimeSec, int64(e.MtimeNsec))
	_ = os.Chtimes(dest, mtime, mtime)
	_ = os.Chown(dest, int(e.UID), int(e.GID))
}

// joinSafe joins name under dir and refuses a result that escapes dir.
// A tree entry name is already validated to hold no '/', '\' or NUL, but
// a root-path TLV carries an arbitrary string read from disk, so this
// check is the one place an escape could otherwise slip through.
func joinSafe(dir, name string) (string, error) {
	dest := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("restore: path %q escapes the output directory", name)
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
		return "", fmt.Errorf("restore: %w", err)
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
// caller that already knows an object's copy lives somewhere other than
// its canonical path, such as a run's catalog/snapobj copy.
func readVerifiedAt(path string, id object.ID) (raw, payload []byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("restore: %s: %w", id.TextForm(), err)
	}
	var ch format.CommonHeader
	if err := ch.Decode(data); err != nil {
		return nil, nil, fmt.Errorf("restore: %s: %w", id.TextForm(), err)
	}
	if len(data) < format.CommonHeaderLen+format.ObjectHeaderLen {
		return nil, nil, fmt.Errorf("restore: %s: file too short", id.TextForm())
	}
	var oh format.ObjectHeader
	if err := oh.Decode(data[format.CommonHeaderLen:]); err != nil {
		return nil, nil, fmt.Errorf("restore: %s: %w", id.TextForm(), err)
	}
	headerLen := uint64(format.CommonHeaderLen + format.ObjectHeaderLen)
	if uint64(len(data)) < headerLen+oh.StoredLen {
		return nil, nil, fmt.Errorf("restore: %s: file too short", id.TextForm())
	}
	stored := data[headerLen : headerLen+oh.StoredLen]
	payload, err = object.Decompress(stored, oh.Compression, oh.PayloadLen)
	if err != nil {
		return nil, nil, fmt.Errorf("restore: %s: %w", id.TextForm(), err)
	}
	if object.ComputeID(payload) != id {
		return nil, nil, fmt.Errorf("restore: %s: content id does not verify", id.TextForm())
	}
	return data, payload, nil
}
