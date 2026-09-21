package object

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/tjjh89017/noahsark/internal/chunker"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// defaultRetryUnstable is commit.retry_unstable's default.
const defaultRetryUnstable = 1

// skipErr marks a source path the walker could not commit: gone since
// its parent directory was listed, or blocked by an open, read or
// readdir error such as EACCES or EIO. The caller skips the path and
// reports reason; it does not abort the commit. An error on the source
// root itself is never wrapped this way, so it stays a hard error.
type skipErr struct {
	reason string
}

func (e *skipErr) Error() string { return e.reason }

// asSkip reports whether err is a skip-marked source-read failure, and
// its reason text.
func asSkip(err error) (*skipErr, bool) {
	return errors.AsType[*skipErr](err)
}

// Summary counts the objects one Commit call wrote or found already
// staged. It covers chunks, blobs, trees and the snapshot together.
type Summary struct {
	NewObjects      int
	ExistingObjects int
	// Unstable lists every regular file the in-flight change detection
	// caught: the file changed while it was being read, and the writer
	// stored the content it read and set the UNSTABLE flag.
	Unstable []UnstablePath
	// Skipped lists every path the writer could not commit: it vanished
	// between being listed and being opened, or an open, read or
	// readdir error blocked it (for example EACCES or EIO). The commit
	// continues without it. Any chunk already staged for a skipped
	// file's content stays in staging as an orphan; gc reclaims it like
	// any other object nothing references.
	Skipped []SkippedPath
	// Special lists every FIFO, socket and device node the commit
	// recorded in a tree. The tree entry keeps the name, the kind and
	// the mode, but no content: such a path carries no bytes to back
	// up, and a restore does not create it again. commit warns about
	// each one, where the operator can still act on it.
	Special []SpecialPath
	// Reachable lists every chunk, blob and tree id this commit's root
	// tree reaches, whether or not the writer actually staged its file.
	// A caller that needs the commit's full object graph must read it
	// here rather than walking the graph back off disk: an object
	// OnDisc reported as already on a disc never gets a staging file to
	// walk into.
	Reachable []ID
	// Excluded counts every path an exclude pattern kept out of the
	// tree: a file, or a directory whose contents were never walked.
	Excluded int
	// MountPoints lists every directory the walker did not cross into
	// because OneFileSystem was set and the directory's device differed
	// from the source root's. Each one is recorded as an empty
	// directory in the tree.
	MountPoints []string
}

// UnstablePath names one path the in-flight change detection flagged, and
// which branch of the rule was taken. This build has no parent snapshot, so
// the branch is always "flagged": the writer stores the content it read
// and sets UNSTABLE.
type UnstablePath struct {
	Path   string
	Branch string
}

// SpecialPath names one FIFO, socket or device node a commit recorded
// without content, and the kind word a warning prints for it.
type SpecialPath struct {
	Path string
	Kind string
}

// SkippedPath names one path the writer could not commit, and why.
type SkippedPath struct {
	Path   string
	Reason string
}

// Writer commits one source directory tree into a staging directory as
// chunk, blob, tree and snapshot object files.
type Writer struct {
	// StagingDir is the staging directory objects and snapshots are
	// written under.
	StagingDir string
	// Profile is the chunker profile applied to every regular file.
	Profile chunker.Profile
	// Now returns the snapshot time. Tests set it to a fixed clock so a
	// commit is reproducible.
	Now func() time.Time
	// RestatAfterRead enables in-flight change detection: stat a regular
	// file before and after reading it, and treat a size or mtime
	// difference as the file having changed during the read. This must
	// never be turned off against a live source.
	RestatAfterRead bool
	// RetryUnstable is how many times an unstable file is re-read before
	// the writer accepts the last read, keeps its content, and sets the
	// entry's UNSTABLE flag.
	RetryUnstable int

	// Stat is the seam every in-flight-change stat goes through. A
	// caller replaces it to make a stat differ deterministically,
	// without touching the real filesystem clock. Defaults to os.Lstat.
	Stat func(path string) (os.FileInfo, error)

	// Open is the seam every regular file's content read goes through.
	// A caller replaces it to inject an open or mid-read error, without
	// touching the real filesystem. Defaults to os.Open.
	Open func(path string) (io.ReadCloser, error)

	// Progress reports bytes of regular-file content chunked during
	// Commit. A nil Progress reports nothing.
	Progress *progress.Reporter

	// Message is the commit message, stored as the snapshot's
	// SnapshotMetaMessage TLV. Empty means no message TLV is written.
	Message string

	// Exclude, when set, keeps a matched path out of the tree entirely.
	// A matched directory is not walked. A nil Exclude excludes nothing.
	Exclude *Matcher

	// OneFileSystem, when true, does not cross a mount point: a
	// directory whose device differs from the source root's is recorded
	// as an empty directory instead of being walked.
	OneFileSystem bool

	// DeviceID returns a path's device id, for OneFileSystem. Defaults
	// to deviceID, which reads syscall.Stat_t.Dev. A caller replaces
	// this to simulate a mount point without a real one, or to detect
	// a platform that carries no device id.
	DeviceID func(info os.FileInfo) (dev uint64, ok bool)

	// Known reports whether id already belongs to the repository: the
	// staging state log carries a record for it. A nil Known leaves an
	// object's Summary count to writeObjectFile's own on-disk check
	// alone. A non-nil Known counts an object as existing whenever it
	// reports true, even when recover left no local staging file
	// for a Packed object, so a re-commit after recover reports
	// the object as existing, not new.
	Known func(id ID) bool

	// OnDisc reports whether id already has its data on some disc:
	// stage.State.OnDisc is true for its staging state log record.
	// writeChunk, writeBlob and writeTree consult it before writing a
	// staging file, and skip the write when it reports true. A commit
	// that re-references an object gc already freed must never refill
	// staging with it; the object stays on the disc that holds it.
	OnDisc func(id ID) bool

	reachable  map[ID]uint64
	rootAbs    string
	rootDev    uint64
	rootDevOK  bool
	ownerNames *nameCache
}

// NewWriter returns a Writer that stages objects under stagingDir using
// the default chunker profile and the system clock.
func NewWriter(stagingDir string) *Writer {
	return &Writer{
		StagingDir:      stagingDir,
		Profile:         chunker.DefaultProfile,
		Now:             time.Now,
		RestatAfterRead: true,
		RetryUnstable:   defaultRetryUnstable,
		Stat:            os.Lstat,
		Open:            func(path string) (io.ReadCloser, error) { return os.Open(path) },
		DeviceID:        deviceID,
	}
}

// Commit walks sourceDir, writes every chunk, blob and tree object it
// needs, writes one snapshot object over the whole tree, and returns the
// snapshot id and the object counts.
func (w *Writer) Commit(sourceDir string) (ID, Summary, error) {
	absRoot, err := filepath.Abs(sourceDir)
	if err != nil {
		return ID{}, Summary{}, err
	}
	rootInfo, err := os.Lstat(absRoot)
	if err != nil {
		return ID{}, Summary{}, err
	}
	if !rootInfo.IsDir() {
		return ID{}, Summary{}, fmt.Errorf("object: source %s is not a directory", absRoot)
	}

	w.reachable = make(map[ID]uint64)
	w.ownerNames = newNameCache()
	w.rootAbs = absRoot
	w.rootDev, w.rootDevOK = 0, false
	if w.OneFileSystem {
		deviceIDFn := w.DeviceID
		if deviceIDFn == nil {
			deviceIDFn = deviceID
		}
		dev, ok := deviceIDFn(rootInfo)
		if !ok {
			return ID{}, Summary{}, fmt.Errorf("object: one-file-system: this platform reports no device id")
		}
		w.rootDev, w.rootDevOK = dev, true
	}
	var sum Summary

	total := regularFileBytes(absRoot)
	w.Progress.Start("commit", total)
	defer w.Progress.Done()

	rootDirTree, err := w.commitDir(absRoot, &sum)
	if err != nil {
		return ID{}, Summary{}, err
	}

	// The root tree is synthetic: it holds exactly one entry, for this
	// source root, pointing at the root directory's own tree.
	rootEntry := format.TreeEntry{
		EntryType: format.EntryTypeDirectory,
		Mode:      permBits(rootInfo),
		Name:      []byte(format.EncodeRootName(absRoot)),
		ContentID: rootDirTree,
	}
	fillTimes(&rootEntry, rootInfo)
	w.fillOwner(&rootEntry, rootInfo)

	rootTreeID, err := w.writeTree([]format.TreeEntry{rootEntry}, &sum)
	if err != nil {
		return ID{}, Summary{}, err
	}

	snapID, err := w.writeSnapshot(rootTreeID, &sum)
	if err != nil {
		return ID{}, Summary{}, err
	}
	sum.Reachable = make([]ID, 0, len(w.reachable))
	for id := range w.reachable {
		sum.Reachable = append(sum.Reachable, id)
	}
	return snapID, sum, nil
}

// regularFileBytes sums the size of every regular file under root, for
// the commit progress total. It is a best-effort pass: a path that
// vanishes or errors out here is simply left out of the total, since
// commitDir itself is the source of truth for what actually gets
// committed.
func regularFileBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// commitDir writes one tree object for the contents of dirPath and
// returns its id. A dirPath that vanished since its parent listed it
// (removed between listing and open) is reported to the caller as
// vanished, not as a commit failure.
func (w *Writer) commitDir(dirPath string, sum *Summary) (ID, error) {
	des, err := os.ReadDir(dirPath)
	if err != nil {
		return ID{}, &skipErr{reason: err.Error()}
	}

	entries := make([]format.TreeEntry, 0, len(des))
	for _, de := range des {
		childPath := filepath.Join(dirPath, de.Name())
		if w.Exclude != nil && w.Exclude.Match(filepath.ToSlash(w.relPath(childPath)), de.IsDir()) {
			sum.Excluded++
			continue
		}
		te, err := w.commitEntry(childPath, de.Name(), sum)
		if se, ok := asSkip(err); ok {
			sum.Skipped = append(sum.Skipped, SkippedPath{Path: w.relPath(childPath), Reason: se.reason})
			continue
		}
		if err != nil {
			return ID{}, fmt.Errorf("%s: %w", childPath, err)
		}
		entries = append(entries, te)
	}
	sortTreeEntries(entries)
	return w.writeTree(entries, sum)
}

// commitEntry builds the tree entry for one directory child, writing
// whatever chunk, blob or tree objects its content needs. A path that
// vanished after the directory listing named it, or that an open, read
// or readdir error blocked, comes back as a skipErr; the caller skips it
// rather than failing the commit.
func (w *Writer) commitEntry(path, name string, sum *Summary) (format.TreeEntry, error) {
	info, err := w.Stat(path)
	if err != nil {
		return format.TreeEntry{}, &skipErr{reason: err.Error()}
	}

	te := format.TreeEntry{
		Name: []byte(name),
		Mode: permBits(info),
	}
	fillTimes(&te, info)

	mode := info.Mode()
	switch {
	case mode.IsDir():
		te.EntryType = format.EntryTypeDirectory
		if w.rootDevOK && w.crossesMount(info) {
			sum.MountPoints = append(sum.MountPoints, w.relPath(path))
			id, err := w.writeTree(nil, sum)
			if err != nil {
				return te, err
			}
			te.ContentID = id
			break
		}
		id, err := w.commitDir(path, sum)
		if err != nil {
			return te, err
		}
		te.ContentID = id
	case mode.IsRegular():
		te.EntryType = format.EntryTypeRegular
		id, size, unstable, err := w.commitFile(path, info, sum)
		if err != nil {
			return te, err
		}
		te.ContentID = id
		te.Size = uint64(size)
		if unstable {
			te.EntryFlags |= format.EntryFlagUnstable
			sum.Unstable = append(sum.Unstable, UnstablePath{Path: w.relPath(path), Branch: "flagged"})
		}
	case mode&os.ModeSymlink != 0:
		te.EntryType = format.EntryTypeSymlink
		target, err := os.Readlink(path)
		if err != nil {
			return te, &skipErr{reason: err.Error()}
		}
		te.TLVs = []format.TLV{{Type: format.TLVTypeSymlinkTarget, Payload: []byte(target)}}
	case mode&os.ModeNamedPipe != 0:
		te.EntryType = format.EntryTypeFIFO
		sum.Special = append(sum.Special, SpecialPath{Path: w.relPath(path), Kind: "FIFO"})
	case mode&os.ModeSocket != 0:
		te.EntryType = format.EntryTypeSocket
		sum.Special = append(sum.Special, SpecialPath{Path: w.relPath(path), Kind: "socket"})
	case mode&os.ModeDevice != 0:
		kind := "block device"
		if mode&os.ModeCharDevice != 0 {
			te.EntryType = format.EntryTypeCharDev
			kind = "character device"
		} else {
			te.EntryType = format.EntryTypeBlockDev
		}
		te.RdevMajor, te.RdevMinor = rdevMajorMinor(info)
		sum.Special = append(sum.Special, SpecialPath{Path: w.relPath(path), Kind: kind})
	default:
		return te, fmt.Errorf("object: unsupported entry type for %s", path)
	}
	// The owner names go in last, because a TLV area is sorted by type
	// and only the symlink target has a lower type.
	w.fillOwner(&te, info)
	return te, nil
}

// commitFile reads and chunks path, applying in-flight change detection:
// it compares the stat before the read against a fresh stat after. A
// difference in size or mtime means the file changed while it was being
// read. The file is re-read up to RetryUnstable times; if it still
// differs, the writer keeps the last read content and reports it
// unstable. Detection never changes the chunk ids a stable file produces,
// since a stable file always takes the no-difference return before any
// retry runs.
func (w *Writer) commitFile(path string, before os.FileInfo, sum *Summary) (id ID, size int64, unstable bool, err error) {
	maxRetries := 0
	if w.RestatAfterRead {
		maxRetries = w.RetryUnstable
	}

	for attempt := 0; ; attempt++ {
		id, size, err = w.readAndChunk(path, sum)
		if err != nil {
			return ID{}, 0, false, err
		}
		if !w.RestatAfterRead {
			return id, size, false, nil
		}

		after, statErr := w.Stat(path)
		changed := statErr != nil || statDiffers(before, after)
		if !changed {
			return id, size, false, nil
		}
		if attempt >= maxRetries {
			return id, size, true, nil
		}
		if statErr != nil {
			// The file is gone between reads; nothing left to restat
			// against. Keep retrying the read itself against the same
			// baseline until the retry budget runs out.
			continue
		}
		before = after
	}
}

// readAndChunk chunks path, writes every new chunk and one blob over
// their ids, and returns the blob id and the file's size. An open error,
// or a read error partway through the file (for example a file that
// vanished after its caller opened it, or an EIO mid-read), is reported
// as a skipErr: the caller drops the whole entry rather than staging a
// blob over truncated content. Any chunk already written to staging
// before the failure stays there as an orphan.
func (w *Writer) readAndChunk(path string, sum *Summary) (ID, int64, error) {
	f, err := w.Open(path)
	if err != nil {
		return ID{}, 0, &skipErr{reason: err.Error()}
	}
	defer func() { _ = f.Close() }()

	ck := chunker.New(f, w.Profile)
	var entries []format.BlobEntry
	var offset uint64
	for {
		chunk, err := ck.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ID{}, 0, &skipErr{reason: err.Error()}
		}
		id, err := w.writeChunk(chunk, sum)
		if err != nil {
			return ID{}, 0, err
		}
		entries = append(entries, format.BlobEntry{
			ContentID: id,
			Length:    uint64(len(chunk)),
		})
		offset += uint64(len(chunk))
		w.Progress.Add(int64(len(chunk)))
	}

	blobID, err := w.writeBlob(entries, offset, sum)
	return blobID, int64(offset), err
}

// writeChunk writes one chunk object for payload, applying the
// minimum-gain compression rule, and returns its content id.
func (w *Writer) writeChunk(payload []byte, sum *Summary) (ID, error) {
	id := ComputeID(format.ObjectKindChunk, payload)
	w.recordReachable(id, uint64(len(payload)))

	if w.OnDisc != nil && w.OnDisc(id) {
		w.countObject(sum, id, false)
		return id, nil
	}

	stored, code, storedLen := Compress(payload)
	c := format.Chunk{
		Header: commonHeader(format.MagicChunk, format.ChunkHeaderLen),
		ObjectHeader: format.ObjectHeader{
			Kind:        format.ObjectKindChunk,
			HashAlgo:    format.HashAlgoSHA256,
			Compression: code,
			PayloadLen:  uint64(len(payload)),
			StoredLen:   storedLen,
		},
		Payload: stored,
	}
	buf := make([]byte, c.EncodedLen())
	if _, err := c.Encode(buf); err != nil {
		return ID{}, err
	}
	isNew, err := writeObjectFile(w.objectPath(id), buf)
	if err != nil {
		return ID{}, err
	}
	w.countObject(sum, id, isNew)
	return id, nil
}

// writeBlob writes one blob object over entries and returns its content
// id. A blob is always stored uncompressed: its bytes are the structured
// entry table, not an opaque payload a generic compressor can shrink.
func (w *Writer) writeBlob(entries []format.BlobEntry, totalSize uint64, sum *Summary) (ID, error) {
	b := format.Blob{
		EntryCount: uint64(len(entries)),
		Entries:    entries,
	}
	b.Header = commonHeader(format.MagicBlob, format.BlobHeaderLen)
	b.ObjectHeader = format.ObjectHeader{Kind: format.ObjectKindBlob, HashAlgo: format.HashAlgoSHA256}

	buf := make([]byte, b.EncodedLen())
	if _, err := b.Encode(buf); err != nil {
		return ID{}, err
	}
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := ComputeID(format.ObjectKindBlob, payload)
	w.recordReachable(id, uint64(len(payload)))

	if w.OnDisc != nil && w.OnDisc(id) {
		w.countObject(sum, id, false)
		return id, nil
	}

	b.ObjectHeader.PayloadLen = uint64(len(payload))
	b.ObjectHeader.StoredLen = uint64(len(payload))
	if _, err := b.Encode(buf); err != nil {
		return ID{}, err
	}
	isNew, err := writeObjectFile(w.objectPath(id), buf)
	if err != nil {
		return ID{}, err
	}
	w.countObject(sum, id, isNew)
	return id, nil
}

// writeTree writes one tree object over entries, already in canonical
// order, and returns its content id. A tree is always stored
// uncompressed, for the same reason as a blob.
func (w *Writer) writeTree(entries []format.TreeEntry, sum *Summary) (ID, error) {
	t := format.Tree{
		Header:       commonHeader(format.MagicTree, format.TreeHeaderLen),
		ObjectHeader: format.ObjectHeader{Kind: format.ObjectKindTree, HashAlgo: format.HashAlgoSHA256},
		EntryCount:   uint32(len(entries)),
		Entries:      entries,
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return ID{}, err
	}
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := ComputeID(format.ObjectKindTree, payload)
	w.recordReachable(id, uint64(len(payload)))

	if w.OnDisc != nil && w.OnDisc(id) {
		w.countObject(sum, id, false)
		return id, nil
	}

	t.ObjectHeader.PayloadLen = uint64(len(payload))
	t.ObjectHeader.StoredLen = uint64(len(payload))
	if _, err := t.Encode(buf); err != nil {
		return ID{}, err
	}
	isNew, err := writeObjectFile(w.objectPath(id), buf)
	if err != nil {
		return ID{}, err
	}
	w.countObject(sum, id, isNew)
	return id, nil
}

// writeSnapshot writes the snapshot object for this commit and returns
// its content id. This build has no parent-chaining input, so every commit
// writes a root snapshot with an all-zero parent.
func (w *Writer) writeSnapshot(rootTreeID ID, sum *Summary) (ID, error) {
	now := w.Now()
	_, tzOffset := now.Zone()

	s := format.Snapshot{
		Common:      commonHeader(format.MagicSnapshot, format.SnapshotHeaderLen),
		Object:      format.ObjectHeader{Kind: format.ObjectKindSnapshot, HashAlgo: format.HashAlgoSHA256},
		RootTree:    rootTreeID,
		TimeSec:     now.Unix(),
		TimeNsec:    uint32(now.Nanosecond()),
		TzOffsetSec: int32(tzOffset),
		TotalSize:   w.totalReachableSize(),
	}
	if w.Message != "" {
		s.Meta = []format.SnapshotMeta{{Tag: format.SnapshotMetaMessage, Value: []byte(w.Message)}}
		s.MetaCount = uint16(len(s.Meta))
	}

	buf := make([]byte, s.EncodedLen())
	if _, err := s.Encode(buf); err != nil {
		return ID{}, err
	}
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := ComputeID(format.ObjectKindSnapshot, payload)

	s.Object.PayloadLen = uint64(len(payload))
	s.Object.StoredLen = uint64(len(payload))
	if _, err := s.Encode(buf); err != nil {
		return ID{}, err
	}
	isNew, err := writeObjectFile(w.snapshotPath(id), buf)
	if err != nil {
		return ID{}, err
	}
	w.countObject(sum, id, isNew)
	return id, nil
}

// recordReachable adds id to the set of objects reachable from this
// commit's root tree, keyed by id so a shared chunk or tree is counted
// once.
func (w *Writer) recordReachable(id ID, payloadLen uint64) {
	if _, ok := w.reachable[id]; !ok {
		w.reachable[id] = payloadLen
	}
}

// totalReachableSize sums the payload length of every distinct reachable
// object recorded so far.
func (w *Writer) totalReachableSize() uint64 {
	var total uint64
	for _, n := range w.reachable {
		total += n
	}
	return total
}

func (w *Writer) objectPath(id ID) string {
	return filepath.Join(w.StagingDir, "objects", id.FanoutByte(), id.TextForm())
}

func (w *Writer) snapshotPath(id ID) string {
	return filepath.Join(w.StagingDir, "snapshots", id.TextForm())
}

// commonHeader builds the common header every object file starts with.
func commonHeader(kind format.Magic, headerLen int) format.CommonHeader {
	return format.CommonHeader{
		MagicProject: format.ProjectMagic,
		MagicKind:    kind,
		VersionMajor: 1,
		HeaderLen:    uint16(headerLen),
	}
}

// writeObjectFile writes data to path through a temp file and a rename,
// so a crash leaves no partial object. When an object with this name
// already exists and its size matches data, it is trusted without a
// write: two objects sharing a content id share identical bytes, and
// pack checks every object's content id before placing it onto a run,
// the real guard against a corrupt staging file. A file that exists
// under the right name but a different size (for example truncated by a
// prior crash) is rewritten through the same temp-file-and-rename path.
func writeObjectFile(path string, data []byte) (isNew bool, err error) {
	if fi, statErr := os.Stat(path); statErr == nil {
		if fi.Size() == int64(len(data)) {
			return false, nil
		}
	} else if !os.IsNotExist(statErr) {
		return false, statErr
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return false, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return false, err
	}
	return true, nil
}

// countObject adds id to sum as new or existing. id counts as existing
// when writeObjectFile found it already on disk, or when w.Known
// reports it as already Staged or Packed in the repository's state
// log; the state log answers for an object that recover marked
// Packed without restoring its local staging file.
func (w *Writer) countObject(sum *Summary, id ID, wroteNew bool) {
	isNew := wroteNew
	if isNew && w.Known != nil && w.Known(id) {
		isNew = false
	}
	if isNew {
		sum.NewObjects++
	} else {
		sum.ExistingObjects++
	}
}

// sortTreeEntries orders entries by raw name bytes ascending, a directory
// name compared with a trailing '/'.
func sortTreeEntries(entries []format.TreeEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entrySortKey(entries[i]), entrySortKey(entries[j])) < 0
	})
}

func entrySortKey(e format.TreeEntry) []byte {
	if e.EntryType == format.EntryTypeDirectory {
		return append(append([]byte(nil), e.Name...), '/')
	}
	return e.Name
}

// relPath renders path relative to the commit's source root for
// reporting. It falls back to the absolute path if the relation cannot be
// computed, which never happens for a path this writer built itself.
func (w *Writer) relPath(path string) string {
	rel, err := filepath.Rel(w.rootAbs, path)
	if err != nil {
		return path
	}
	return rel
}

// statDiffers reports whether a and b disagree on size or mtime, the two
// fields the in-flight change detection compares.
func statDiffers(a, b os.FileInfo) bool {
	if a.Size() != b.Size() {
		return true
	}
	asec, ansec := mtimeOf(a)
	bsec, bnsec := mtimeOf(b)
	return asec != bsec || ansec != bnsec
}

// mtimeOf returns a's mtime as seconds and nanoseconds, using the raw
// stat when available for full precision.
func mtimeOf(info os.FileInfo) (sec int64, nsec int64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Mtim.Sec, st.Mtim.Nsec
	}
	return info.ModTime().Unix(), int64(info.ModTime().Nanosecond())
}

// fillTimes sets a tree entry's mtime and ctime from info. Access time
// and birth time are never stored.
func fillTimes(te *format.TreeEntry, info os.FileInfo) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		te.MtimeSec = info.ModTime().Unix()
		te.MtimeNsec = uint32(info.ModTime().Nanosecond())
		te.EntryFlags |= format.EntryFlagCtimeAbsent
		return
	}
	te.MtimeSec = st.Mtim.Sec
	te.MtimeNsec = uint32(st.Mtim.Nsec)
	te.CtimeSec = st.Ctim.Sec
	te.CtimeNsec = uint32(st.Ctim.Nsec)
}

// fillOwner sets a tree entry's uid and gid from info, and adds the user
// name and the group name TLVs when the host can name the ids. A
// platform whose os.FileInfo carries no *syscall.Stat_t keeps 0 for
// both ids and adds no name. A lookup that fails adds no name; it is
// not an error.
func (w *Writer) fillOwner(te *format.TreeEntry, info os.FileInfo) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	te.UID = st.Uid
	te.GID = st.Gid
	if name := w.ownerNames.user(st.Uid); name != "" {
		te.TLVs = append(te.TLVs, format.TLV{Type: format.TLVTypeUserName, Payload: []byte(name)})
	}
	if name := w.ownerNames.group(st.Gid); name != "" {
		te.TLVs = append(te.TLVs, format.TLV{Type: format.TLVTypeGroupName, Payload: []byte(name)})
	}
}

// nameCache holds the name of each uid and each gid one commit meets, so
// a commit looks up one owner one time. It holds one entry for each
// distinct owner of the source tree, never one for each file. An empty
// name means the lookup failed, and is cached too.
type nameCache struct {
	users  map[uint32]string
	groups map[uint32]string
}

func newNameCache() *nameCache {
	return &nameCache{users: make(map[uint32]string), groups: make(map[uint32]string)}
}

func (n *nameCache) user(uid uint32) string {
	if name, ok := n.users[uid]; ok {
		return name
	}
	name := ""
	if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
		name = u.Username
	}
	n.users[uid] = name
	return name
}

func (n *nameCache) group(gid uint32) string {
	if name, ok := n.groups[gid]; ok {
		return name
	}
	name := ""
	if g, err := user.LookupGroupId(strconv.FormatUint(uint64(gid), 10)); err == nil {
		name = g.Name
	}
	n.groups[gid] = name
	return name
}

// permBits returns mode bits 0 to 11: rwxrwxrwx plus setuid, setgid and
// sticky. The file type never enters this field.
func permBits(info os.FileInfo) uint32 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return uint32(info.Mode().Perm())
	}
	return uint32(st.Mode) & 07777
}

// crossesMount reports whether info's directory sits on a different
// device than the source root, using the Writer's DeviceID seam. A
// platform that reports no device id for info never crosses: Commit
// already refused OneFileSystem for such a platform before the walk
// started, so this only runs where a device id is available.
func (w *Writer) crossesMount(info os.FileInfo) bool {
	deviceIDFn := w.DeviceID
	if deviceIDFn == nil {
		deviceIDFn = deviceID
	}
	dev, ok := deviceIDFn(info)
	return ok && dev != w.rootDev
}

// deviceID reads a path's device id from syscall.Stat_t.Dev. It reports
// ok false on a platform whose os.FileInfo carries no *syscall.Stat_t.
func deviceID(info os.FileInfo) (dev uint64, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// rdevMajorMinor decodes a device entry's major and minor numbers from
// its raw rdev, using the standard Linux encoding.
func rdevMajorMinor(info os.FileInfo) (major, minor uint32) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	dev := uint64(st.Rdev)
	major = uint32((dev>>8)&0xfff) | uint32((dev>>32)&^uint64(0xfff))
	minor = uint32(dev&0xff) | uint32((dev>>12)&^uint64(0xff))
	return major, minor
}
