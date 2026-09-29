package restore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
	"github.com/tjjh89017/noahsark/internal/progress"
)

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

// Assembler restores one selection of a snapshot from the catalog and
// one disc at a time, with no spool: it walks the selection one time for
// each disc and writes each chunk of that disc straight into the part
// file of the file that holds it.
//
// It reads every tree and blob from the catalog. Only chunk payloads come
// from a disc. It keeps the state of each file in a temporary file, not
// in memory, so its memory does not grow with the number of files.
type Assembler struct {
	c      *catalog.Catalog
	sel    *plan.Selection
	outDir string
	wp     *writePolicy
	// states holds a record for each file in scope that is not complete
	// yet. A file that the first walk finished, resumed or skipped has no
	// record, and a later walk then reads neither its blob nor its bytes.
	states *fileStates
	// fileNo is the number of the next regular file of the current walk.
	fileNo int64
	// firstDisc is true until the first walk ends. The first walk
	// creates the directories and the symlinks, and decides every
	// existing destination; a later walk creates nothing.
	firstDisc bool
	// chunkBuf backs the content check of a resumed part file. It grows
	// to the largest chunk the restore meets, never past the maximum
	// chunk size.
	chunkBuf []byte
}

// errIncomplete names a file that needs a chunk that no disc of this
// restore gave: a chunk on a lost disc, or a chunk that no catalog INDEX
// lists.
var errIncomplete = errors.New("file not restored: a chunk of this file is on a lost disc or on no disc known to the catalog; the part file stays")

// NewAssembler prepares a disc-swap restore of sel into outDir. It
// creates outDir and the temporary state file, and reads nothing else
// until the first Disc call. Close frees the state file.
func NewAssembler(c *catalog.Catalog, sel *plan.Selection, outDir string, overwrite bool) (*Assembler, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	states, err := newFileStates()
	if err != nil {
		return nil, err
	}
	return &Assembler{
		c:         c,
		sel:       sel,
		outDir:    outDir,
		wp:        &writePolicy{overwrite: overwrite},
		states:    states,
		firstDisc: true,
	}, nil
}

// Close frees the temporary state file.
func (a *Assembler) Close() { a.states.close() }

// Disc walks the selection one time against d, and writes every chunk d
// holds into the part file of the file that holds it. A file whose last
// chunk lands here gets its final name before Disc moves to the next
// file.
//
// A read error on one chunk fails that file alone and the walk goes on.
// Only a *FatalDiscError, and a failure that stops the walk itself, is
// returned.
func (a *Assembler) Disc(d DiscChunks, prog *progress.Reporter) error {
	if !a.firstDisc && a.states.pending == 0 {
		return nil
	}
	err := a.walk(&discWalk{a: a, d: d, prog: prog})
	a.firstDisc = false
	return err
}

// walk walks the selection with v and numbers its regular files. A walk
// after the first must meet as many files as the first.
func (a *Assembler) walk(v plan.Visitor) error {
	a.fileNo = 0
	if err := a.sel.Walk(a.outDir, v); err != nil {
		return err
	}
	if a.firstDisc {
		a.states.files = a.fileNo
		return nil
	}
	if a.fileNo != a.states.files {
		return errDestChanged
	}
	return nil
}

// nextFile returns the number of the next regular file of the walk.
func (a *Assembler) nextFile() int64 {
	no := a.fileNo
	a.fileNo++
	return no
}

// discWalk is the walk of one disc.
type discWalk struct {
	a    *Assembler
	d    DiscChunks
	prog *progress.Reporter
}

// Dir makes or enters every component under parent. The first walk
// creates what is missing and reports a path it cannot use; a later
// walk enters the same directories again, with no second report of a
// path the first walk already reported.
func (w *discWalk) Dir(parent string, names []string) (string, bool, error) {
	if w.a.firstDisc {
		return ensureDir(parent, names, w.a.wp)
	}
	return ensureDir(parent, names, nil)
}

func (w *discWalk) DirDone(string, format.TreeEntry) {}

func (w *discWalk) File(dest, part string, e format.TreeEntry) error {
	return w.a.file(w.a.nextFile(), dest, part, e, w.d, w.prog)
}

// Other restores a symlink, and reports a special file, on the first
// walk only.
func (w *discWalk) Other(dest string, e format.TreeEntry) error {
	if !w.a.firstDisc {
		return nil
	}
	if e.EntryType != format.EntryTypeSymlink {
		w.a.wp.unsupported(dest, e.EntryType)
		return nil
	}
	target, err := symlinkTarget(e)
	if err == nil {
		err = restoreSymlink(dest, target, e, w.a.wp)
	}
	if err != nil {
		w.a.wp.failed(dest, err)
	}
	return nil
}

// blobError reports a blob that the catalog cannot give.
func blobError(id object.ID, err error) error {
	return fmt.Errorf("blob %s is not in the catalog; run recover with the disc that holds it: %w", id.TextForm(), err)
}

// file restores regular file no as far as d can take it. The first walk
// decides an existing destination and registers the file; a later walk
// works only on a file that is still pending.
func (a *Assembler) file(no int64, dest, part string, e format.TreeEntry, d DiscChunks, prog *progress.Reporter) error {
	var st fileState
	pending := false
	if !a.firstDisc {
		var err error
		st, pending, err = a.states.get(no, dest)
		if err != nil || !pending {
			return err
		}
	}
	blobID := object.ID(e.ContentID)
	blob, err := a.c.ReadBlob(blobID)
	if err != nil {
		a.wp.failed(dest, blobError(blobID, err))
		return a.states.clear(no, pending)
	}
	entries := placeChunks(blob.Entries)

	if a.firstDisc {
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
		st = a.register(part, entries)
	}

	var wanted []placedChunk
	for _, be := range entries {
		if d.Has(object.ID(be.ContentID)) {
			wanted = append(wanted, be)
		}
	}
	if len(wanted) > 0 || st.remaining == 0 {
		if err := a.writePart(part, &st, e, wanted, d, prog); err != nil {
			if _, fatal := errors.AsType[*FatalDiscError](err); fatal {
				return err
			}
			a.wp.failed(dest, err)
			return a.states.clear(no, pending)
		}
	}
	if st.remaining == 0 {
		finishPart(dest, part, e, a.wp)
		return a.states.clear(no, pending)
	}
	if pending && len(wanted) == 0 {
		return nil
	}
	return a.states.put(no, dest, st, pending)
}

// register counts how many blob entries of one file still owe their
// bytes. A part file a killed run left behind is read one time here,
// and every chunk it already holds is taken out of the count, whatever
// disc that chunk came from. A later walk therefore finishes the file
// even when the restore never asks for that disc again.
func (a *Assembler) register(part string, entries []placedChunk) fileState {
	f, err := os.Open(part)
	if err != nil {
		return fileState{remaining: uint64(len(entries))}
	}
	defer func() { _ = f.Close() }()
	var remaining uint64
	for _, be := range entries {
		if !a.chunkInPlace(f, be) {
			remaining++
		}
	}
	return fileState{remaining: remaining, resume: true}
}

// writePart opens the part file, sets its final size, and writes every
// chunk of this disc into it at the chunk's own offset. A chunk whose
// bytes are already in the part file, from a run that was killed, is
// checked against its content id and skipped.
func (a *Assembler) writePart(part string, st *fileState, e format.TreeEntry, wanted []placedChunk, d DiscChunks, prog *progress.Reporter) error {
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
		if st.resume && a.chunkInPlace(f, be) {
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
		st.remaining--
	}
	return f.Close()
}

// chunkInPlace reports whether f already holds be's own bytes at be's
// offset, by the same content id check a restore uses everywhere else.
func (a *Assembler) chunkInPlace(f *os.File, be placedChunk) bool {
	if uint64(cap(a.chunkBuf)) < be.Length {
		a.chunkBuf = make([]byte, be.Length)
	}
	return chunkAt(f, be, a.chunkBuf[:be.Length])
}

// chunkAt reports whether f holds the bytes of be at the offset of be.
// buf has the length of be.
func chunkAt(f *os.File, be placedChunk, buf []byte) bool {
	if _, err := f.ReadAt(buf, int64(be.Offset)); err != nil {
		return false
	}
	return object.ComputeID(format.ObjectKindChunk, buf) == object.ID(be.ContentID)
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

// Finish walks the selection one last time. It names every file no disc
// could complete, whose part file stays for a later run, and applies the
// metadata of each directory after every entry below it. It follows at
// least one Disc call.
func (a *Assembler) Finish() error {
	return a.walk(finishWalk{a})
}

// finishWalk is the last walk of the selection. It creates nothing and
// reads no blob.
type finishWalk struct{ a *Assembler }

func (w finishWalk) Dir(parent string, names []string) (string, bool, error) {
	return ensureDir(parent, names, nil)
}

// DirDone follows every entry below the directory, so the deepest
// directory gets its metadata first.
func (w finishWalk) DirDone(path string, e format.TreeEntry) {
	applyMetadata(path, e, w.a.wp)
}

func (w finishWalk) File(dest, _ string, _ format.TreeEntry) error {
	_, pending, err := w.a.states.get(w.a.nextFile(), dest)
	if pending {
		w.a.wp.failed(dest, errIncomplete)
	}
	return err
}

func (w finishWalk) Other(string, format.TreeEntry) error { return nil }

// Report is what this restore did not do.
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
		if !chunkAt(f, be, buf[:be.Length]) {
			return false
		}
	}
	return true
}

// ReadChunkFromRoot reads and verifies one chunk object from a mounted
// disc root or a copy of a disc root.
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
// or a copy of a disc root.
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
