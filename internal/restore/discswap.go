package restore

import (
	"errors"
	"fmt"
	"io/fs"
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

// partSuffix ends the name of the hidden file a restore writes a file's
// bytes into until the file is complete.
const partSuffix = ".noahsark-part"

// DiscChunks is one inserted disc, as the assembler reads it. Has
// answers from the disc's own object list, with no disc access; Read
// returns one verified chunk payload and may make the operator insert
// the disc first.
type DiscChunks interface {
	Has(id object.ID) bool
	Read(id object.ID) ([]byte, error)
}

// FatalDiscError marks a disc read error that stops the whole restore,
// such as a prompt for the disc that cannot be answered. Every other
// read error fails one file and the walk goes on.
type FatalDiscError struct{ Err error }

func (e *FatalDiscError) Error() string { return e.Err.Error() }
func (e *FatalDiscError) Unwrap() error { return e.Err }

// Assembler restores one snapshot from the local cache and one disc at
// a time, with no spool: it walks the snapshot's tree one time for each
// disc and writes each chunk of that disc straight into the part file
// of the file that holds it.
//
// It reads every tree and blob from the cache. Only chunk payloads come
// from a disc.
type Assembler struct {
	c           *cache.Cache
	snap        *format.Snapshot
	outDir      string
	includes    []string
	wp          *writePolicy
	dirsForMeta []dirMeta
	// pending holds one record for each file in scope that is not
	// complete yet, by destination path. A file that the first walk
	// finished, resumed or skipped is absent, and a later walk then
	// reads neither its blob nor its bytes.
	pending map[string]*pendingFile
	// firstDisc is true until the first walk ends. The first walk
	// creates the directories and the symlinks, and decides every
	// existing destination; a later walk creates nothing.
	firstDisc bool
	// chunkBuf backs the content check of a resumed part file. It grows
	// to the largest chunk the restore meets, never past the maximum
	// chunk size.
	chunkBuf []byte
}

// pendingFile is what the assembler keeps for one file it has not
// finished: how many blob entries still owe their bytes, and whether a
// part file from an earlier run was already on disk.
// Nothing here grows with the file's size or with the snapshot's.
type pendingFile struct {
	remaining int
	resume    bool
}

// dirMeta is one directory the walk created, recorded so its metadata
// can be applied in a deferred pass, deepest first, after the last
// disc.
type dirMeta struct {
	path string
	e    format.TreeEntry
}

// errIncomplete names a file no disc of this restore could finish.
var errIncomplete = errors.New("not every chunk of this file was read; the part file is kept for a later run")

// NewAssembler prepares a disc-swap restore of snap into outDir,
// restricted to includes (the whole snapshot when includes is empty).
// It creates outDir and reads nothing else until the first Disc call.
func NewAssembler(c *cache.Cache, snap *format.Snapshot, outDir string, includes []string, overwrite bool) (*Assembler, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	return &Assembler{
		c:         c,
		snap:      snap,
		outDir:    outDir,
		includes:  includes,
		wp:        &writePolicy{overwrite: overwrite},
		pending:   make(map[string]*pendingFile),
		firstDisc: true,
	}, nil
}

// Disc walks the snapshot one time against d, and writes every chunk d
// holds into the part file of the file that holds it. A file whose last
// chunk lands here gets its final name before Disc moves to the next
// file.
//
// A read error on one chunk fails that file alone and the walk goes on.
// Only a *FatalDiscError, and a failure that stops the walk itself, is
// returned.
func (a *Assembler) Disc(d DiscChunks, prog *progress.Reporter) error {
	if !a.firstDisc && len(a.pending) == 0 {
		return nil
	}
	rootTree, err := a.c.ReadTree(object.ID(a.snap.RootTree))
	if err != nil {
		return err
	}
	filter, err := newFilterState(a.includes)
	if err != nil {
		return err
	}
	for _, e := range rootTree.Entries {
		if e.EntryType != format.EntryTypeDirectory {
			continue
		}
		rootPath := rootPathOf(e)
		if rootPath == "" {
			continue
		}
		childFilter, include := stepInto(filter, splitPath(rootPath))
		if !include {
			continue
		}
		dest, ok, err := a.dir(a.outDir, splitPath(rootPath))
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := a.walkDir(object.ID(e.ContentID), dest, childFilter, d, prog); err != nil {
			return err
		}
		if a.firstDisc {
			a.dirsForMeta = append(a.dirsForMeta, dirMeta{dest, e})
		}
	}
	if a.firstDisc {
		if unmatched := unmatchedIncludes(filter, a.includes); len(unmatched) > 0 {
			return &UnmatchedIncludeError{Paths: unmatched}
		}
	}
	a.firstDisc = false
	return nil
}

// dir makes or enters every component under parent. The first walk
// creates what is missing and reports a path it cannot use; a later
// walk enters the same directories again, with no second report of a
// path the first walk already reported.
func (a *Assembler) dir(parent string, components []string) (string, bool, error) {
	if a.firstDisc {
		return ensureDir(parent, components, a.wp)
	}
	return ensureDir(parent, components, nil)
}

func (a *Assembler) walkDir(treeID object.ID, dest string, filter *filterState, d DiscChunks, prog *progress.Reporter) error {
	t, err := a.c.ReadTree(treeID)
	if err != nil {
		return fmt.Errorf("tree %s: %w", treeID.TextForm(), err)
	}
	taken := make(map[string]bool, len(t.Entries))
	for _, e := range t.Entries {
		taken[string(e.Name)] = true
	}
	for _, e := range t.Entries {
		childFilter, include := stepInto(filter, []string{string(e.Name)})
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
			sub, ok, err := a.dir(dest, []string{name})
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := a.walkDir(object.ID(e.ContentID), sub, childFilter, d, prog); err != nil {
				return err
			}
			if a.firstDisc {
				a.dirsForMeta = append(a.dirsForMeta, dirMeta{sub, e})
			}
		case format.EntryTypeRegular:
			part, err := joinSafe(dest, partName(name, taken))
			if err != nil {
				return err
			}
			if err := a.file(child, part, object.ID(e.ContentID), e, d, prog); err != nil {
				return err
			}
		case format.EntryTypeSymlink:
			if !a.firstDisc {
				continue
			}
			target, err := symlinkTarget(e)
			if err != nil {
				return err
			}
			if err := restoreSymlink(child, target, e, a.wp); err != nil {
				a.wp.failed(child, err)
			}
		default:
			if a.firstDisc {
				a.wp.unsupported(child, e.EntryType)
			}
		}
	}
	return nil
}

// partName returns the part-file name of a file called name in a
// directory whose own entry names are taken. The snapshot itself can
// hold a file of the plain part name; the suffix then carries a number,
// so a restore never writes into a path the snapshot owns. The names
// come from the tree, thus every run picks the same one.
func partName(name string, taken map[string]bool) string {
	candidate := "." + name + partSuffix
	for i := 2; taken[candidate]; i++ {
		candidate = fmt.Sprintf(".%s%s%d", name, partSuffix, i)
	}
	return candidate
}

// file restores one regular file as far as d can take it. The first
// walk decides an existing destination and registers the file; a later
// walk works only on a file that is still pending.
func (a *Assembler) file(dest, part string, blobID object.ID, e format.TreeEntry, d DiscChunks, prog *progress.Reporter) error {
	pf, known := a.pending[dest]
	if !known && !a.firstDisc {
		return nil
	}
	blob, err := a.c.ReadBlob(blobID)
	if err != nil {
		return fmt.Errorf("blob %s: not held by the cache; disc-swap restore needs every blob cached: %w", blobID.TextForm(), err)
	}
	entries := placeChunks(blob.Entries)

	if !known {
		if !a.wp.overwrite {
			if resumed, found := existingFileStatus(dest, e, entries); found {
				if resumed {
					a.wp.resume()
					// A run killed between link and unlink is the one way
					// the final name and the part file both exist.
					removePart(part)
				} else {
					a.wp.skip(dest)
				}
				return nil
			}
		}
		pf = a.register(part, entries)
		a.pending[dest] = pf
	}

	var wanted []placedChunk
	for _, be := range entries {
		if d.Has(object.ID(be.ContentID)) {
			wanted = append(wanted, be)
		}
	}
	if len(wanted) == 0 && pf.remaining > 0 {
		return nil
	}
	if err := a.writePart(part, pf, e, wanted, d, prog); err != nil {
		if _, fatal := errors.AsType[*FatalDiscError](err); fatal {
			return err
		}
		a.wp.failed(dest, err)
		delete(a.pending, dest)
		return nil
	}
	if pf.remaining == 0 {
		finishPart(dest, part, e, a.wp)
		delete(a.pending, dest)
	}
	return nil
}

// register counts how many blob entries of one file still owe their
// bytes. A part file a killed run left behind is read one time here,
// and every chunk it already holds is taken out of the count, whatever
// disc that chunk came from. A later walk therefore finishes the file
// even when the restore never asks for that disc again.
func (a *Assembler) register(part string, entries []placedChunk) *pendingFile {
	f, err := os.Open(part)
	if err != nil {
		return &pendingFile{remaining: len(entries)}
	}
	defer func() { _ = f.Close() }()
	remaining := 0
	for _, be := range entries {
		if !a.chunkInPlace(f, be) {
			remaining++
		}
	}
	return &pendingFile{remaining: remaining, resume: true}
}

// writePart opens the part file, sets its final size, and writes every
// chunk of this disc into it at the chunk's own offset. A chunk whose
// bytes are already in the part file, from a run that was killed, is
// checked against its content id and skipped.
func (a *Assembler) writePart(part string, pf *pendingFile, e format.TreeEntry, wanted []placedChunk, d DiscChunks, prog *progress.Reporter) error {
	f, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(int64(e.Size)); err != nil {
		return err
	}
	for _, be := range wanted {
		id := object.ID(be.ContentID)
		if pf.resume && a.chunkInPlace(f, be) {
			// register already took this chunk out of remaining.
			continue
		}
		payload, err := d.Read(id)
		if err != nil {
			return err
		}
		if uint64(len(payload)) != be.Length {
			return fmt.Errorf("chunk %s: length %d, blob entry says %d", id.TextForm(), len(payload), be.Length)
		}
		if _, err := f.WriteAt(payload, int64(be.Offset)); err != nil {
			return err
		}
		prog.Add(int64(len(payload)))
		pf.remaining--
	}
	return f.Close()
}

// chunkInPlace reports whether f already holds be's own bytes at be's
// offset, by the same content id check a restore uses everywhere else.
func (a *Assembler) chunkInPlace(f *os.File, be placedChunk) bool {
	if uint64(cap(a.chunkBuf)) < be.Length {
		a.chunkBuf = make([]byte, be.Length)
	}
	buf := a.chunkBuf[:be.Length]
	if _, err := f.ReadAt(buf, int64(be.Offset)); err != nil {
		return false
	}
	return object.ComputeID(buf) == object.ID(be.ContentID)
}

// finishPart gives a complete part file its final name, then applies
// the file's metadata. link fails when the final name exists, so the
// no-overwrite rule holds with no race; with overwrite, the path in the
// way is unlinked first, and a directory that holds entries is never
// removed.
func finishPart(dest, part string, e format.TreeEntry, wp *writePolicy) {
	if wp.overwrite {
		if _, err := os.Lstat(dest); err == nil {
			if err := unlinkExisting("file", dest); err != nil {
				wp.blocked("file", dest, err)
				removePart(part)
				return
			}
		}
	}
	if err := linkPart(part, dest); err != nil {
		if errors.Is(err, fs.ErrExist) {
			wp.skip(dest)
		} else {
			wp.failed(dest, err)
		}
		removePart(part)
		return
	}
	removePart(part)
	applyMetadata(dest, e, wp)
}

// linkFile is os.Link, a seam a test drives the no-hard-link fallback
// through.
var linkFile = os.Link

// linkPart makes dest a second name for part. A filesystem with no hard
// link falls back to a check and a rename; that fallback has a small
// race, because another process can create dest between the check and
// the rename.
func linkPart(part, dest string) error {
	err := linkFile(part, dest)
	if err == nil || !linkUnsupported(err) {
		return err
	}
	if _, statErr := os.Lstat(dest); statErr == nil {
		return fs.ErrExist
	}
	return os.Rename(part, dest)
}

// linkUnsupported reports whether err says the filesystem has no hard
// link. EXDEV is not among them: the part file sits in the destination
// directory itself.
func linkUnsupported(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOSYS)
}

// removePart unlinks a part file this restore wrote. It removes a
// regular file only, so a symlink or a directory that stands at the
// part name is left exactly as found.
func removePart(part string) {
	fi, err := os.Lstat(part)
	if err != nil || !fi.Mode().IsRegular() {
		return
	}
	_ = os.Remove(part)
}

// Finish applies directory metadata in a deferred pass, deepest
// directory first, and names every file no disc could complete. Its
// part file stays for a later run.
func (a *Assembler) Finish() {
	for _, d := range slices.Backward(a.dirsForMeta) {
		applyMetadata(d.path, d.e, a.wp)
	}
	paths := make([]string, 0, len(a.pending))
	for path := range a.pending {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		a.wp.failed(path, errIncomplete)
	}
}

// Report is what this restore did not do, in the one form every restore
// mode reports through.
func (a *Assembler) Report() Report { return a.wp.report }

// fileAlreadyRestored reports whether dest, an existing regular file,
// already holds e's data: either its size and mtime match e exactly, the
// way applyMetadata leaves a file this restore wrote itself, or its
// bytes hash to the same chunk ids entries names, checked straight from
// dest with no disc access needed.
func fileAlreadyRestored(dest string, fi os.FileInfo, e format.TreeEntry, entries []placedChunk) bool {
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
func contentMatches(dest string, entries []placedChunk) bool {
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
		if _, err := f.ReadAt(chunk, int64(be.Offset)); err != nil {
			return false
		}
		if object.ComputeID(chunk) != object.ID(be.ContentID) {
			return false
		}
	}
	return true
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

// DiscIdentity is how a disc names itself to the operator: its number,
// its label and its uuid, all read from its own DISC.bin.
type DiscIdentity struct {
	Seq   uint64
	Label string
	UUID  [16]byte
}

// ReadDiscIdentity reads and decodes DISC.bin from a mounted disc root
// or unpacked NOAHSARK tree.
func ReadDiscIdentity(root string) (DiscIdentity, error) {
	names := image.NewNameCache()
	base, err := findNoahsark(root, names)
	if err != nil {
		return DiscIdentity{}, err
	}
	buf, err := os.ReadFile(filepath.Join(base, names.Resolve(base, "DISC.bin")))
	if err != nil {
		return DiscIdentity{}, err
	}
	var disc format.Disc
	if err := disc.Decode(buf); err != nil {
		return DiscIdentity{}, err
	}
	n := min(int(disc.LabelLen), len(disc.Label))
	return DiscIdentity{Seq: disc.DiscSeq, Label: string(disc.Label[:n]), UUID: disc.DiscUUID}, nil
}

// ReadDiscUUID returns the uuid of the disc at root.
func ReadDiscUUID(root string) ([16]byte, error) {
	id, err := ReadDiscIdentity(root)
	return id.UUID, err
}
