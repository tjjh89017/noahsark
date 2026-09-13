package object

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/tjjh89017/noahsark/internal/chunker"
	"github.com/tjjh89017/noahsark/internal/format"
)

// Fixed header lengths of the four object kinds: the common header, the
// object header, and each kind's own fixed body, before any variable
// area. These match the header_len a Phase 1 writer records.
const (
	chunkHeaderLen    = format.CommonHeaderLen + format.ObjectHeaderLen
	blobBodyLen       = 24
	blobHeaderLen     = format.CommonHeaderLen + format.ObjectHeaderLen + blobBodyLen
	treeBodyLen       = 8
	treeHeaderLen     = format.CommonHeaderLen + format.ObjectHeaderLen + treeBodyLen
	snapshotHeaderLen = format.CommonHeaderLen + format.ObjectHeaderLen + format.SnapshotFixedLen
)

// Summary counts the objects one Commit call wrote or found already
// staged. It covers chunks, blobs, trees and the snapshot together.
type Summary struct {
	NewObjects      int
	ExistingObjects int
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

	reachable map[ID]uint64
}

// NewWriter returns a Writer that stages objects under stagingDir using
// the default chunker profile and the system clock.
func NewWriter(stagingDir string) *Writer {
	return &Writer{
		StagingDir: stagingDir,
		Profile:    chunker.DefaultProfile,
		Now:        time.Now,
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
	var sum Summary

	rootDirTree, err := w.commitDir(absRoot, &sum)
	if err != nil {
		return ID{}, Summary{}, err
	}

	// The root tree is synthetic: it holds exactly one entry, for this
	// source root, pointing at the root directory's own tree.
	rootEntry := format.TreeEntry{
		EntryType: format.EntryTypeDirectory,
		Mode:      permBits(rootInfo),
		Name:      []byte(encodeRootName(absRoot)),
		ContentID: rootDirTree,
		TLVs: []format.TLV{
			{Type: format.TLVTypeRootPath, Payload: []byte(absRoot)},
		},
	}
	fillTimes(&rootEntry, rootInfo)

	rootTreeID, err := w.writeTree([]format.TreeEntry{rootEntry}, &sum)
	if err != nil {
		return ID{}, Summary{}, err
	}

	snapID, err := w.writeSnapshot(rootTreeID, &sum)
	if err != nil {
		return ID{}, Summary{}, err
	}
	return snapID, sum, nil
}

// commitDir writes one tree object for the contents of dirPath and
// returns its id.
func (w *Writer) commitDir(dirPath string, sum *Summary) (ID, error) {
	des, err := os.ReadDir(dirPath)
	if err != nil {
		return ID{}, err
	}

	entries := make([]format.TreeEntry, 0, len(des))
	for _, de := range des {
		childPath := filepath.Join(dirPath, de.Name())
		te, err := w.commitEntry(childPath, de.Name(), sum)
		if err != nil {
			return ID{}, fmt.Errorf("%s: %w", childPath, err)
		}
		entries = append(entries, te)
	}
	sortTreeEntries(entries)
	return w.writeTree(entries, sum)
}

// commitEntry builds the tree entry for one directory child, writing
// whatever chunk, blob or tree objects its content needs.
func (w *Writer) commitEntry(path, name string, sum *Summary) (format.TreeEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return format.TreeEntry{}, err
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
		id, err := w.commitDir(path, sum)
		if err != nil {
			return te, err
		}
		te.ContentID = id
	case mode.IsRegular():
		te.EntryType = format.EntryTypeRegular
		id, size, err := w.commitFile(path, sum)
		if err != nil {
			return te, err
		}
		te.ContentID = id
		te.Size = uint64(size)
	case mode&os.ModeSymlink != 0:
		te.EntryType = format.EntryTypeSymlink
		target, err := os.Readlink(path)
		if err != nil {
			return te, err
		}
		te.TLVs = []format.TLV{{Type: format.TLVTypeSymlinkTarget, Payload: []byte(target)}}
	case mode&os.ModeNamedPipe != 0:
		te.EntryType = format.EntryTypeFIFO
	case mode&os.ModeSocket != 0:
		te.EntryType = format.EntryTypeSocket
	case mode&os.ModeDevice != 0:
		if mode&os.ModeCharDevice != 0 {
			te.EntryType = format.EntryTypeCharDev
		} else {
			te.EntryType = format.EntryTypeBlockDev
		}
		te.RdevMajor, te.RdevMinor = rdevMajorMinor(info)
	default:
		return te, fmt.Errorf("object: unsupported entry type for %s", path)
	}
	return te, nil
}

// commitFile chunks path, writes every new chunk and one blob over their
// ids, and returns the blob id and the file's size.
func (w *Writer) commitFile(path string, sum *Summary) (ID, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return ID{}, 0, err
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
			return ID{}, 0, err
		}
		id, err := w.writeChunk(chunk, sum)
		if err != nil {
			return ID{}, 0, err
		}
		entries = append(entries, format.BlobEntry{
			ContentID:  id,
			Length:     uint64(len(chunk)),
			FileOffset: offset,
		})
		offset += uint64(len(chunk))
	}

	blobID, err := w.writeBlob(entries, offset, sum)
	return blobID, int64(offset), err
}

// writeChunk writes one chunk object for payload, applying the
// minimum-gain compression rule, and returns its content id.
func (w *Writer) writeChunk(payload []byte, sum *Summary) (ID, error) {
	id := ComputeID(payload)
	w.recordReachable(id, uint64(len(payload)))

	stored, code, storedLen := Compress(payload)
	c := format.Chunk{
		Header: commonHeader(format.MagicChunk, chunkHeaderLen),
		ObjectHeader: format.ObjectHeader{
			Kind:        format.ObjectKindChunk,
			HashAlgo:    format.HashAlgoSHA256,
			DigestLen:   32,
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
	countObject(sum, isNew)
	return id, nil
}

// writeBlob writes one blob object over entries and returns its content
// id. A blob is always stored uncompressed: its bytes are the structured
// entry table, not an opaque payload a generic compressor can shrink.
func (w *Writer) writeBlob(entries []format.BlobEntry, totalSize uint64, sum *Summary) (ID, error) {
	b := format.Blob{
		EntryCount: uint64(len(entries)),
		TotalSize:  totalSize,
		EntrySize:  format.BlobEntryLen,
		HashAlgo:   format.HashAlgoSHA256,
		DigestLen:  32,
		Entries:    entries,
	}
	b.Header = commonHeader(format.MagicBlob, blobHeaderLen)
	b.ObjectHeader = format.ObjectHeader{Kind: format.ObjectKindBlob, HashAlgo: format.HashAlgoSHA256, DigestLen: 32}

	buf := make([]byte, b.EncodedLen())
	if _, err := b.Encode(buf); err != nil {
		return ID{}, err
	}
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := ComputeID(payload)
	w.recordReachable(id, uint64(len(payload)))

	b.ObjectHeader.PayloadLen = uint64(len(payload))
	b.ObjectHeader.StoredLen = uint64(len(payload))
	if _, err := b.Encode(buf); err != nil {
		return ID{}, err
	}
	isNew, err := writeObjectFile(w.objectPath(id), buf)
	if err != nil {
		return ID{}, err
	}
	countObject(sum, isNew)
	return id, nil
}

// writeTree writes one tree object over entries, already in canonical
// order, and returns its content id. A tree is always stored
// uncompressed, for the same reason as a blob.
func (w *Writer) writeTree(entries []format.TreeEntry, sum *Summary) (ID, error) {
	t := format.Tree{
		Header:       commonHeader(format.MagicTree, treeHeaderLen),
		ObjectHeader: format.ObjectHeader{Kind: format.ObjectKindTree, HashAlgo: format.HashAlgoSHA256, DigestLen: 32},
		EntryCount:   uint32(len(entries)),
		Entries:      entries,
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return ID{}, err
	}
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := ComputeID(payload)
	w.recordReachable(id, uint64(len(payload)))

	t.ObjectHeader.PayloadLen = uint64(len(payload))
	t.ObjectHeader.StoredLen = uint64(len(payload))
	if _, err := t.Encode(buf); err != nil {
		return ID{}, err
	}
	isNew, err := writeObjectFile(w.objectPath(id), buf)
	if err != nil {
		return ID{}, err
	}
	countObject(sum, isNew)
	return id, nil
}

// writeSnapshot writes the snapshot object for this commit and returns
// its content id. Phase 1 has no parent-chaining input, so every commit
// writes a root snapshot: generation 1, an all-zero parent.
func (w *Writer) writeSnapshot(rootTreeID ID, sum *Summary) (ID, error) {
	now := w.Now()
	_, tzOffset := now.Zone()

	s := format.Snapshot{
		Common:               commonHeader(format.MagicSnapshot, snapshotHeaderLen),
		Object:               format.ObjectHeader{Kind: format.ObjectKindSnapshot, HashAlgo: format.HashAlgoSHA256, DigestLen: 32},
		RootTree:             rootTreeID,
		Generation:           1,
		TimeSec:              now.Unix(),
		TimeNsec:             uint32(now.Nanosecond()),
		TzOffsetSec:          int32(tzOffset),
		TotalSize:            w.totalReachableSize(),
		ReachableObjectCount: uint64(len(w.reachable)),
		HashAlgo:             format.HashAlgoSHA256,
		ChunkerProfile:       format.ChunkerProfileP4,
		SourceType:           format.SnapshotSourceLocal,
		// The writer does not probe SEEK_HOLE, so it never claims sparse
		// detection happened.
		SourceFlags: format.SnapshotFlagNoSparse,
	}

	buf := make([]byte, s.EncodedLen())
	if _, err := s.Encode(buf); err != nil {
		return ID{}, err
	}
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := ComputeID(payload)

	s.Object.PayloadLen = uint64(len(payload))
	s.Object.StoredLen = uint64(len(payload))
	if _, err := s.Encode(buf); err != nil {
		return ID{}, err
	}
	isNew, err := writeObjectFile(w.snapshotPath(id), buf)
	if err != nil {
		return ID{}, err
	}
	countObject(sum, isNew)
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
		VersionMinor: 0,
		HeaderLen:    uint16(headerLen),
	}
}

// writeObjectFile writes data to path through a temp file and a rename,
// so a crash leaves no partial object. It does nothing and reports false
// when an object with this name already exists: two objects sharing a
// content id share identical bytes, so there is nothing to add.
func writeObjectFile(path string, data []byte) (isNew bool, err error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
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

func countObject(sum *Summary, isNew bool) {
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

// encodeRootName escapes a source root's absolute path into the one path
// component a root tree entry's name must be: '/' becomes "%2F", '\'
// becomes "%5C", NUL becomes "%00", and '%' becomes "%25".
func encodeRootName(path string) string {
	var b bytes.Buffer
	for i := 0; i < len(path); i++ {
		switch c := path[i]; c {
		case '/':
			b.WriteString("%2F")
		case '\\':
			b.WriteString("%5C")
		case 0:
			b.WriteString("%00")
		case '%':
			b.WriteString("%25")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// fillTimes sets a tree entry's mtime and ctime from info, and marks
// atime and btime absent. Phase 1 defaults never store atime or btime.
func fillTimes(te *format.TreeEntry, info os.FileInfo) {
	te.EntryFlags |= format.EntryFlagAtimeAbsent | format.EntryFlagBtimeAbsent
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

// permBits returns mode bits 0 to 11: rwxrwxrwx plus setuid, setgid and
// sticky. The file type never enters this field.
func permBits(info os.FileInfo) uint32 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return uint32(info.Mode().Perm())
	}
	return uint32(st.Mode) & 07777
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
