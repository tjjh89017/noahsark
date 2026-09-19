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
// using its bytes; a chunk, blob, tree or snapshot that does not verify
// is a hard error.
func Restore(discRoot string, snapshotID object.ID, outDir string, opts ...Option) (resumed, skipped int, err error) {
	return RestoreWithProgress(discRoot, snapshotID, outDir, nil, opts...)
}

// writePolicy carries the overwrite rule of section 15.6 through the
// restore walk, plus the running counts of paths left alone because they
// already existed and --overwrite was not given: resumed for a path
// already fully restored, skipped for one that disagrees with the
// snapshot and needs --overwrite to replace.
type writePolicy struct {
	overwrite bool
	resumed   int
	skipped   int
	// unsupported holds every entry whose type this build does not
	// restore, such as a device node, a FIFO or a socket.
	unsupported   []UnsupportedEntry
	onUnsupported func(path string, entryType uint8)
	// overwriteBlocked holds every path --overwrite could not replace,
	// because removing what stood there failed; most often a directory
	// that still holds entries. Each one also counts in skipped.
	overwriteBlocked   []OverwriteBlockedEntry
	onOverwriteBlocked func(entry OverwriteBlockedEntry)
	// metadataFailures holds every metadata_not_applied event: a mode,
	// times or owner field that a path's Chmod, Chtimes or Chown could
	// not apply.
	metadataFailures  []MetadataFailure
	onMetadataFailure func(f MetadataFailure)
}

// UnsupportedEntry is one entry a restore did not write, because this
// build does not restore its entry type.
type UnsupportedEntry struct {
	Path      string
	EntryType uint8
}

// recordUnsupported notes one entry this build does not restore. The
// walk continues, so every later entry still reaches its path.
func (wp *writePolicy) recordUnsupported(path string, entryType uint8) {
	if wp == nil {
		return
	}
	wp.unsupported = append(wp.unsupported, UnsupportedEntry{Path: path, EntryType: entryType})
	if wp.onUnsupported != nil {
		wp.onUnsupported(path, entryType)
	}
}

// OverwriteBlockedEntry is one path left exactly as found because
// --overwrite could not remove what stood there. kind names what the
// snapshot entry wanted to create at path: "symlink", "file" or
// "directory". reason is a short, human-readable cause.
type OverwriteBlockedEntry struct {
	Path   string
	Kind   string
	Reason string
}

// recordOverwriteBlocked notes one path --overwrite could not clear.
// It counts as skipped, the same as a path left alone without
// --overwrite, and the walk continues into every other path.
func (wp *writePolicy) recordOverwriteBlocked(kind, path string, unlinkErr error) {
	if wp == nil {
		return
	}
	wp.skipped++
	entry := OverwriteBlockedEntry{Path: path, Kind: kind, Reason: overwriteBlockReason(unlinkErr)}
	wp.overwriteBlocked = append(wp.overwriteBlocked, entry)
	if wp.onOverwriteBlocked != nil {
		wp.onOverwriteBlocked(entry)
	}
}

// existingFileStatus is the one decision every restore mode uses for a
// destination path that already exists: a regular file whose size
// matches the tree entry, and whose mtime or content also matches, is
// already fully restored (resumed); anything else found at dest is left
// for --overwrite to resolve (not resumed). found is false when dest
// does not exist.
func existingFileStatus(dest string, e format.TreeEntry, entries []format.BlobEntry) (resumed, found bool) {
	fi, err := os.Lstat(dest)
	if err != nil {
		return false, false
	}
	if !fi.Mode().IsRegular() {
		return false, true
	}
	return fileAlreadyRestored(dest, fi, e, entries), true
}

// RestoreWithProgress is Restore, reporting bytes written through prog.
// A nil prog reports nothing. The total is the sum of every regular
// file's recorded size in the snapshot's tree that WithInclude leaves
// in scope, found by a pass over the tree objects alone, before any
// file content is read or written.
//
// With WithInclude, every include path is checked against the snapshot's
// tree, reading only tree objects, before any directory is created or
// file written. A path that matches nothing fails the whole call with an
// *UnmatchedIncludeError naming every such path.
func RestoreWithProgress(discRoot string, snapshotID object.ID, outDir string, prog *progress.Reporter, opts ...Option) (resumed, skipped int, err error) {
	o := newRestoreOptions(opts)
	cache := image.NewNameCache()
	base, err := findNoahsark(discRoot, cache)
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, 0, err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return 0, 0, err
	}

	snapRaw, _, err := readVerified(base, snapshotID, true, cache)
	if err != nil {
		return 0, 0, err
	}
	var snap format.Snapshot
	if _, err := snap.Decode(snapRaw); err != nil {
		return 0, 0, fmt.Errorf("snapshot %s: %w", snapshotID.TextForm(), err)
	}

	rootRaw, _, err := readVerified(base, object.ID(snap.RootTree), false, cache)
	if err != nil {
		return 0, 0, err
	}
	var rootTree format.Tree
	if _, err := rootTree.Decode(rootRaw); err != nil {
		return 0, 0, fmt.Errorf("tree %s: %w", object.ID(snap.RootTree).TextForm(), err)
	}

	fs, err := newFilterState(o.includes)
	if err != nil {
		return 0, 0, err
	}
	if fs != nil {
		if err := resolveIncludes(base, rootTree.Entries, fs, cache); err != nil {
			return 0, 0, err
		}
		if unmatched := unmatchedIncludes(fs, o.includes); len(unmatched) > 0 {
			return 0, 0, &UnmatchedIncludeError{Paths: unmatched}
		}
	}

	var total uint64
	for _, e := range rootTree.Entries {
		if e.EntryType != format.EntryTypeDirectory {
			continue
		}
		child, include := stepInto(fs, splitPath(rootPathOf(e)))
		if !include {
			continue
		}
		total += sumRegularSizes(base, object.ID(e.ContentID), child, cache)
	}
	prog.Start("restore: bytes written", int64(total))
	defer prog.Done()

	wp := &writePolicy{overwrite: o.overwrite, onUnsupported: o.onUnsupported, onOverwriteBlocked: o.onOverwriteBlocked, onMetadataFailure: o.onMetadataFailure}
	for _, e := range rootTree.Entries {
		if err := restoreRootEntry(base, absOut, e, prog, cache, fs, wp); err != nil {
			return wp.resumed, wp.skipped, err
		}
	}
	return wp.resumed, wp.skipped, nil
}

// sumRegularSizes recursively sums every regular file entry's recorded
// size under treeID that fs leaves in scope, reading only tree objects,
// never file content. A tree that fails to read or decode contributes
// zero rather than failing the whole progress total: the real restore
// below is what reports any such error properly.
func sumRegularSizes(base string, treeID object.ID, fs *filterState, cache *image.NameCache) uint64 {
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
		child, include := stepInto(fs, []string{string(e.Name)})
		if !include {
			continue
		}
		switch e.EntryType {
		case format.EntryTypeDirectory:
			total += sumRegularSizes(base, object.ID(e.ContentID), child, cache)
		case format.EntryTypeRegular:
			total += e.Size
		}
	}
	return total
}

// resolveIncludes checks every include path fs carries against
// rootEntries, reading only tree objects and writing nothing. It leaves
// fs.matched set for every path it found.
func resolveIncludes(base string, rootEntries []format.TreeEntry, fs *filterState, cache *image.NameCache) error {
	for _, e := range rootEntries {
		if e.EntryType != format.EntryTypeDirectory {
			continue
		}
		rootPath := rootPathOf(e)
		if rootPath == "" {
			continue
		}
		child, include := stepInto(fs, splitPath(rootPath))
		if !include || child == nil {
			continue
		}
		if err := resolveIncludesDir(base, object.ID(e.ContentID), child, cache); err != nil {
			return err
		}
	}
	return nil
}

// resolveIncludesDir is resolveIncludes for one already-matched
// directory's own tree.
func resolveIncludesDir(base string, treeID object.ID, fs *filterState, cache *image.NameCache) error {
	raw, _, err := readVerified(base, treeID, false, cache)
	if err != nil {
		return err
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return fmt.Errorf("tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range t.Entries {
		child, include := stepInto(fs, []string{string(e.Name)})
		if !include || child == nil || e.EntryType != format.EntryTypeDirectory {
			continue
		}
		if err := resolveIncludesDir(base, object.ID(e.ContentID), child, cache); err != nil {
			return err
		}
	}
	return nil
}

// restoreRootEntry restores one entry of the synthetic root tree. Its
// destination is the source's own absolute path, carried in the
// entry's root-path TLV, joined under outDir; its content is the
// entry's own directory tree, restored directly into that destination
// rather than one level below it.
func restoreRootEntry(base, outDir string, e format.TreeEntry, prog *progress.Reporter, cache *image.NameCache, fs *filterState, wp *writePolicy) error {
	if e.EntryType != format.EntryTypeDirectory {
		return fmt.Errorf("root entry %q: expected a directory", e.Name)
	}
	rootPath := rootPathOf(e)
	if rootPath == "" {
		return fmt.Errorf("root entry %q: no root path TLV", e.Name)
	}
	childFS, include := stepInto(fs, splitPath(rootPath))
	if !include {
		return nil
	}
	dest, ok, err := ensureDir(outDir, splitPath(rootPath), wp)
	if err != nil || !ok {
		return err
	}
	if err := restoreDirContents(base, object.ID(e.ContentID), dest, prog, cache, childFS, wp); err != nil {
		return err
	}
	applyMetadata(dest, e, wp)
	return nil
}

// restoreDirContents decodes the tree at treeID and restores every entry
// fs leaves in scope as a child of dest, which already exists.
func restoreDirContents(base string, treeID object.ID, dest string, prog *progress.Reporter, cache *image.NameCache, fs *filterState, wp *writePolicy) error {
	raw, _, err := readVerified(base, treeID, false, cache)
	if err != nil {
		return err
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return fmt.Errorf("tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range t.Entries {
		childFS, include := stepInto(fs, []string{string(e.Name)})
		if !include {
			continue
		}
		if err := restoreEntry(base, dest, e, prog, cache, childFS, wp); err != nil {
			return err
		}
	}
	return nil
}

// restoreEntry writes one tree entry as a child of dir. The caller has
// already decided the entry is in scope; fs is only used for a directory
// entry's own children.
func restoreEntry(base, dir string, e format.TreeEntry, prog *progress.Reporter, cache *image.NameCache, fs *filterState, wp *writePolicy) error {
	name := string(e.Name)
	child, err := joinSafe(dir, name)
	if err != nil {
		return err
	}
	switch e.EntryType {
	case format.EntryTypeDirectory:
		sub, ok, err := ensureDir(dir, []string{name}, wp)
		if err != nil || !ok {
			return err
		}
		if err := restoreDirContents(base, object.ID(e.ContentID), sub, prog, cache, fs, wp); err != nil {
			return err
		}
		applyMetadata(sub, e, wp)
		return nil
	case format.EntryTypeRegular:
		skipped, err := restoreFile(base, child, object.ID(e.ContentID), e, prog, cache, wp)
		if err != nil {
			return err
		}
		if skipped {
			// The path already existed; --overwrite was not given, or
			// would not have applied. Leave it exactly as found.
			return nil
		}
		applyMetadata(child, e, wp)
		return nil
	case format.EntryTypeSymlink:
		target, err := symlinkTarget(e)
		if err != nil {
			return err
		}
		return restoreSymlink(child, target, e, wp)
	default:
		wp.recordUnsupported(child, e.EntryType)
		return nil
	}
}

// restoreFile reassembles blobID's chunks into dest, in blob entry order,
// verifying every chunk's content id before writing its bytes. It
// reports skipped true, and leaves dest untouched, when dest already
// exists and wp.overwrite is false: existingFileStatus decides whether
// that counts as resumed (wp.resumed) or a conflict (wp.skipped).
func restoreFile(base, dest string, blobID object.ID, e format.TreeEntry, prog *progress.Reporter, cache *image.NameCache, wp *writePolicy) (skipped bool, err error) {
	raw, _, err := readVerified(base, blobID, false, cache)
	if err != nil {
		return false, err
	}
	var blob format.Blob
	if _, err := blob.Decode(raw); err != nil {
		return false, fmt.Errorf("blob %s: %w", blobID.TextForm(), err)
	}

	entries := append([]format.BlobEntry(nil), blob.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].FileOffset < entries[j].FileOffset })

	f, skipped, err := openForWrite(dest, e, entries, wp)
	if err != nil {
		return false, err
	}
	if skipped {
		return true, nil
	}

	_, err = writeChunks(f, entries, prog, func(id object.ID) ([]byte, bool, error) {
		_, payload, err := readVerified(base, id, false, cache)
		if err != nil {
			return nil, false, err
		}
		return payload, true, nil
	})
	return false, err
}

// openForWrite creates dest for a restore write, following the path
// safety rule of OPERATIONS.md's metadata restore policy: without
// --overwrite, an existing path counts as resumed or skipped by
// existingFileStatus, the one rule every restore mode shares; with
// --overwrite, the existing path is unlinked first and then created.
// A file created this way is always new, so O_TRUNC is never needed.
// An unlink that fails, most often because dest is a non-empty
// directory, leaves dest exactly as found, counts it as skipped and
// reports it through wp's overwrite-blocked report, instead of
// stopping the restore.
func openForWrite(dest string, e format.TreeEntry, entries []format.BlobEntry, wp *writePolicy) (f *os.File, skipped bool, err error) {
	if wp != nil && wp.overwrite {
		if _, statErr := os.Lstat(dest); statErr == nil {
			if err := unlinkExisting("file", dest); err != nil {
				wp.recordOverwriteBlocked("file", dest, err)
				return nil, true, nil
			}
		} else if !os.IsNotExist(statErr) {
			return nil, false, statErr
		}
	} else if resumed, found := existingFileStatus(dest, e, entries); found {
		if wp != nil {
			if resumed {
				wp.resumed++
			} else {
				wp.skipped++
			}
		}
		return nil, true, nil
	}
	f, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		if os.IsExist(err) {
			// Raced with something else creating dest since the check
			// above; treat it as an ordinary conflict.
			if wp != nil {
				wp.skipped++
			}
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
// caller that already knows an object's copy lives somewhere other than
// its canonical path, such as a run's catalog/snapobj copy.
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
