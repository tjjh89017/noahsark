package restore

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"syscall"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// Manifest is a restore's file and directory list, built from the local
// cache alone (OPERATIONS.md "14. Restore"'s disc-swap mode reads the
// snapshot's trees and blobs from the cache; only chunk payloads still
// need a disc). Every directory and symlink is created as soon as the
// manifest is built; every regular file waits until its chunks are
// spooled.
type Manifest struct {
	outDir       string
	files        []*pendingFile
	filesByChunk map[object.ID][]*pendingFile
	dirsForMeta  []dirMeta
	wp           *writePolicy
}

// pendingFile is one regular file the manifest still owes: its
// destination path, its chunk list in file order, and the chunk ids it
// is still waiting on.
type pendingFile struct {
	path      string
	entries   []format.BlobEntry
	remaining map[object.ID]bool
	treeEntry format.TreeEntry
	written   bool
}

// dirMeta is one directory the manifest already created, recorded so
// its metadata can be applied in a deferred pass, deepest first, once
// every file underneath it is written.
type dirMeta struct {
	path string
	e    format.TreeEntry
}

// BuildManifest reads snap's tree from the cache, restricted to
// includes (the whole snapshot when includes is empty), creates every
// directory and symlink under outDir, and returns the regular files
// still waiting on chunk data. A blob the cache does not hold is a hard
// error: this build has no path to fetch a blob object from a mounted
// disc during the disc-swap walk.
func BuildManifest(c *cache.Cache, snap *format.Snapshot, outDir string, includes []string, overwrite bool) (*Manifest, error) {
	rootTree, err := c.ReadTree(object.ID(snap.RootTree))
	if err != nil {
		return nil, err
	}
	fs, err := newFilterState(includes)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	m := &Manifest{
		outDir:       outDir,
		filesByChunk: make(map[object.ID][]*pendingFile),
		wp:           &writePolicy{overwrite: overwrite},
	}
	for _, e := range rootTree.Entries {
		if e.EntryType != format.EntryTypeDirectory {
			continue
		}
		rootPath := rootPathOf(e)
		if rootPath == "" {
			continue
		}
		childFS, include := stepInto(fs, splitPath(rootPath))
		if !include {
			continue
		}
		dest, ok, err := ensureDir(outDir, splitPath(rootPath), m.wp)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if err := m.walkDir(c, object.ID(e.ContentID), dest, childFS); err != nil {
			return nil, err
		}
		m.dirsForMeta = append(m.dirsForMeta, dirMeta{dest, e})
	}
	if unmatched := unmatchedIncludes(fs, includes); len(unmatched) > 0 {
		return nil, &UnmatchedIncludeError{Paths: unmatched}
	}
	return m, nil
}

func (m *Manifest) walkDir(c *cache.Cache, treeID object.ID, dest string, fs *filterState) error {
	t, err := c.ReadTree(treeID)
	if err != nil {
		return fmt.Errorf("tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range t.Entries {
		childFS, include := stepInto(fs, []string{string(e.Name)})
		if !include {
			continue
		}
		name := string(e.Name)
		child, err := joinSafe(dest, name)
		if err != nil {
			return err
		}
		switch e.EntryType {
		case format.EntryTypeDirectory:
			sub, ok, err := ensureDir(dest, []string{name}, m.wp)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := m.walkDir(c, object.ID(e.ContentID), sub, childFS); err != nil {
				return err
			}
			m.dirsForMeta = append(m.dirsForMeta, dirMeta{sub, e})
		case format.EntryTypeRegular:
			if err := m.addFile(c, child, object.ID(e.ContentID), e); err != nil {
				return err
			}
		case format.EntryTypeSymlink:
			target, err := symlinkTarget(e)
			if err != nil {
				return err
			}
			if err := restoreSymlink(child, target, e, m.wp); err != nil {
				m.wp.failed(child, err)
			}
		default:
			m.wp.unsupported(child, e.EntryType)
		}
	}
	return nil
}

// addFile registers one regular file. A killed disc-swap restore can
// leave a file already fully written from an earlier run; with overwrite
// not requested, such a file counts as resumed rather than skipped, so a
// rerun reports success once every other file is in place. A file whose
// size, mtime, or content disagrees with the tree entry counts as
// skipped, the ordinary conflict a caller resolves with --overwrite.
func (m *Manifest) addFile(c *cache.Cache, dest string, blobID object.ID, e format.TreeEntry) error {
	blob, err := c.ReadBlob(blobID)
	if err != nil {
		return fmt.Errorf("blob %s: not held by the cache; disc-swap restore needs every blob cached: %w", blobID.TextForm(), err)
	}
	entries := append([]format.BlobEntry(nil), blob.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].FileOffset < entries[j].FileOffset })

	if !m.wp.overwrite {
		if resumed, found := existingFileStatus(dest, e, entries); found {
			if resumed {
				m.wp.resume()
			} else {
				m.wp.skip(dest)
			}
			return nil
		}
	}

	pf := &pendingFile{path: dest, entries: entries, remaining: make(map[object.ID]bool), treeEntry: e}
	for _, be := range entries {
		id := object.ID(be.ContentID)
		pf.remaining[id] = true
		m.filesByChunk[id] = append(m.filesByChunk[id], pf)
	}
	m.files = append(m.files, pf)
	return nil
}

// NeedsChunk reports whether some still-unwritten file is still waiting
// on id specifically: a chunk already spooled for a file that also
// needs other, not-yet-spooled chunks reports false here, so a resumed
// run does not re-fetch what it already has.
func (m *Manifest) NeedsChunk(id object.ID) bool {
	for _, pf := range m.filesByChunk[id] {
		if !pf.written && pf.remaining[id] {
			return true
		}
	}
	return false
}

// MarkSpooled records that id's chunk payload is now in spoolDir.
func (m *Manifest) MarkSpooled(id object.ID) {
	for _, pf := range m.filesByChunk[id] {
		delete(pf.remaining, id)
	}
}

// WriteReady writes every pending file whose chunks are all spooled,
// reading each chunk from spoolDir, and frees a spooled chunk once
// every file that needed it is written. It reports how many files it
// wrote and how many spool bytes it freed, so a caller enforcing
// restore.staging_budget can keep a running total without re-statting
// spoolDir.
func (m *Manifest) WriteReady(spoolDir string, prog *progress.Reporter) (written int, freedBytes uint64, err error) {
	for _, pf := range m.files {
		if pf.written || len(pf.remaining) > 0 {
			continue
		}
		freed, err := m.writeFile(spoolDir, pf, prog)
		if err != nil {
			return written, freedBytes, err
		}
		written++
		freedBytes += freed
	}
	return written, freedBytes, nil
}

func (m *Manifest) writeFile(spoolDir string, pf *pendingFile, prog *progress.Reporter) (freedBytes uint64, err error) {
	f, skipped, err := openForWrite(pf.path, pf.treeEntry, pf.entries, m.wp)
	if err != nil {
		return 0, err
	}
	if !skipped {
		// A file that fails here is recorded and the assembly goes on
		// to the next file, the same rule the disc-root walk follows.
		err := writeChunksFrom(f, pf.entries, prog, spoolDir)
		if err != nil {
			m.wp.failed(pf.path, err)
		} else {
			applyMetadata(pf.path, pf.treeEntry, m.wp)
		}
	}
	pf.written = true
	for _, be := range pf.entries {
		id := object.ID(be.ContentID)
		if !m.NeedsChunk(id) {
			_ = os.Remove(SpoolObjectPath(spoolDir, id))
			freedBytes += be.Length
		}
	}
	return freedBytes, nil
}

// writeChunksFrom writes every blob entry's spooled payload into f, and
// closes f.
func writeChunksFrom(f *os.File, entries []format.BlobEntry, prog *progress.Reporter, spoolDir string) error {
	_, err := writeChunks(f, entries, prog, func(id object.ID) ([]byte, bool, error) {
		data, err := os.ReadFile(SpoolObjectPath(spoolDir, id))
		if err != nil {
			return nil, false, fmt.Errorf("chunk %s: %w", id.TextForm(), err)
		}
		return data, true, nil
	})
	return err
}

// FileExceedingBudget returns the path and total chunk bytes of the
// first not-yet-written pending file whose chunks alone add up to more
// than budget, in the manifest's build order. It reports ok=false when
// budget is 0 (unlimited) or every file fits, so a caller can check this
// once, before any disc is read: no split of that file's own chunks
// across passes could keep the spool under budget.
func (m *Manifest) FileExceedingBudget(budget uint64) (path string, bytes uint64, ok bool) {
	if budget == 0 {
		return "", 0, false
	}
	for _, pf := range m.files {
		if pf.written {
			continue
		}
		var total uint64
		for _, be := range pf.entries {
			total += be.Length
		}
		if total > budget {
			return pf.path, total, true
		}
	}
	return "", 0, false
}

// Finish applies directory metadata in a deferred pass, deepest
// directory first, matching OPERATIONS.md's restore pipeline.
func (m *Manifest) Finish() {
	for _, d := range slices.Backward(m.dirsForMeta) {
		applyMetadata(d.path, d.e, m.wp)
	}
}

// Report is what this manifest's restore did not do, in the one form
// every restore mode reports through.
func (m *Manifest) Report() Report { return m.wp.report }

// fileAlreadyRestored reports whether dest, an existing regular file,
// already holds e's data: either its size and mtime match e exactly, the
// way applyMetadata leaves a file this restore wrote itself, or its
// bytes hash to the same chunk ids entries names, checked straight from
// dest with no disc access needed.
func fileAlreadyRestored(dest string, fi os.FileInfo, e format.TreeEntry, entries []format.BlobEntry) bool {
	if uint64(fi.Size()) != e.Size {
		return false
	}
	sec, nsec := statMtime(fi)
	if sec == e.MtimeSec && uint32(nsec) == e.MtimeNsec {
		return true
	}
	return contentMatches(dest, entries)
}

// statMtime returns fi's mtime as seconds and nanoseconds, using the raw
// stat when available for the same precision applyMetadata restores.
func statMtime(fi os.FileInfo) (sec, nsec int64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Mtim.Sec, st.Mtim.Nsec
	}
	return fi.ModTime().Unix(), int64(fi.ModTime().Nanosecond())
}

// contentMatches reports whether dest's bytes, split at entries' own
// offsets and lengths, hash to the content id each entry names. It reads
// dest, never a disc, so a resumed check never needs the drive back.
func contentMatches(dest string, entries []format.BlobEntry) bool {
	f, err := os.Open(dest)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 0, 1<<20)
	for _, be := range entries {
		if cap(buf) < int(be.Length) {
			buf = make([]byte, be.Length)
		}
		chunk := buf[:be.Length]
		if _, err := f.ReadAt(chunk, int64(be.FileOffset)); err != nil {
			return false
		}
		if object.ComputeID(chunk) != object.ID(be.ContentID) {
			return false
		}
	}
	return true
}

// Pending reports whether any file is still waiting on chunk data.
func (m *Manifest) Pending() bool {
	for _, pf := range m.files {
		if !pf.written {
			return true
		}
	}
	return false
}

// MissingObjects returns the content id of every chunk at least one
// unwritten file is still waiting on, deduplicated and sorted.
func (m *Manifest) MissingObjects() []object.ID {
	seen := make(map[object.ID]bool)
	var out []object.ID
	for _, pf := range m.files {
		if pf.written {
			continue
		}
		for id := range pf.remaining {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TextForm() < out[j].TextForm() })
	return out
}

// SpoolObjectPath returns the path a chunk's payload is spooled to
// under spoolDir.
func SpoolObjectPath(spoolDir string, id object.ID) string {
	return filepath.Join(spoolDir, id.TextForm())
}

// ReadChunkFromRoot reads and verifies one chunk object from a mounted
// disc root or unpacked NOAHSARK tree, the same on-disc layout Restore
// reads.
func ReadChunkFromRoot(root string, id object.ID) ([]byte, error) {
	names := image.NewNameCache()
	base, err := findNoahsark(root, names)
	if err != nil {
		return nil, err
	}
	_, payload, err := readVerified(base, id, false, names)
	return payload, err
}

// ReadDiscUUID reads and decodes DISC.bin from a mounted disc root or
// unpacked NOAHSARK tree, returning the disc's uuid.
func ReadDiscUUID(root string) ([16]byte, error) {
	names := image.NewNameCache()
	base, err := findNoahsark(root, names)
	if err != nil {
		return [16]byte{}, err
	}
	buf, err := os.ReadFile(filepath.Join(base, names.Resolve(base, "DISC.bin")))
	if err != nil {
		return [16]byte{}, err
	}
	var disc format.Disc
	if err := disc.Decode(buf); err != nil {
		return [16]byte{}, err
	}
	return disc.DiscUUID, nil
}
