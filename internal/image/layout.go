package image

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// SnapshotRef binds a name to a snapshot id, for one REFS record. Name is
// typically "LATEST".
type SnapshotRef struct {
	Name string
	ID   object.ID
	Time time.Time
}

// BuildOptions holds everything Build needs to lay out one run.
type BuildOptions struct {
	// StagingDir is the staging directory objects and snapshots come
	// from, in the layout internal/object writes: objects/<ab>/<name>
	// and snapshots/<name>.
	StagingDir string
	// Snapshots names every snapshot this run stores, and the REFS
	// records that point at them. Phase 1 writes one run per disc, so
	// this run stores every object every listed snapshot reaches.
	Snapshots []SnapshotRef
	// TargetCapacitySectors is the pack limit. Build refuses to run
	// without it.
	TargetCapacitySectors uint64
	// PhysicalCapacitySectors is the disc's reported capacity. For an
	// image build this is the value the caller passes.
	PhysicalCapacitySectors uint64
	// OutputDir receives the NOAHSARK tree.
	OutputDir string
	RepoUUID  [16]byte
	DiscUUID  [16]byte
	Label     string
	MediaType format.MediaType
	// Now returns the pack time. Defaults to time.Now.
	Now func() time.Time
}

// Result summarizes one Build call.
type Result struct {
	RunSeq       uint64
	DiscSeq      uint64
	ObjectCount  int
	FileCount    int
	StreamBlocks uint64
	StripeCount  uint64
}

// toolVersion is registry id 1, the reference implementation, version 1.
const toolVersion = uint32(1)<<24 | 1

// runSeq and discSeq are fixed in Phase 1: one run on one disc, no
// append.
const (
	buildRunSeq  uint64 = 1
	buildDiscSeq uint64 = 0
)

// fileRow is one row-to-be of INDEX's Files table, plus the bytes to
// write to the output tree and whether the row's bytes enter the FEC
// stream.
type fileRow struct {
	role    uint8
	byteLen uint64
	hash    [32]byte // zero for a row whose bytes are not final until
	// after INDEX itself is built; see docs/decisions.md.
	data []byte // bytes to write; nil when filled in later (RUN,
	// RUN2, checksum, parity, and INDEX itself).
	path     string // path under OutputDir, relative, forward slashes.
	inStream bool
}

// Build lays out one run over the snapshots opts names and writes the
// full NOAHSARK tree under opts.OutputDir as ordinary files.
func Build(opts BuildOptions) (*Result, error) {
	if opts.StagingDir == "" || opts.OutputDir == "" {
		return nil, fmt.Errorf("image: staging directory and output directory are required")
	}
	if len(opts.Snapshots) == 0 {
		return nil, fmt.Errorf("image: at least one snapshot is required")
	}
	if opts.TargetCapacitySectors == 0 {
		return nil, fmt.Errorf("image: target capacity is required and must not be zero")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	packTime := now()

	snapIDs := make([]object.ID, len(opts.Snapshots))
	for i, s := range opts.Snapshots {
		snapIDs[i] = s.ID
	}
	reachable, err := collectReachable(opts.StagingDir, snapIDs)
	if err != nil {
		return nil, err
	}
	// Sort object rows by their whole-file hash, the order section 11.1
	// gives for role 13 rows.
	type hashedObject struct {
		reachableObject
		hash [32]byte
	}
	hashed := make([]hashedObject, len(reachable))
	for i, r := range reachable {
		hashed[i] = hashedObject{reachableObject: r, hash: sha256.Sum256(r.Bytes)}
	}
	sort.Slice(hashed, func(i, j int) bool {
		return lessBytes(hashed[i].hash[:], hashed[j].hash[:])
	})

	// Snapshot object copies under catalog/snapobj, one per snapshot this
	// run stores, ordered by snapshot content id ascending.
	snapObjOrder := append([]object.ID(nil), snapIDs...)
	sort.Slice(snapObjOrder, func(i, j int) bool {
		return lessBytes(snapObjOrder[i][:], snapObjOrder[j][:])
	})
	snapBytes := make(map[object.ID][]byte, len(hashed))
	for _, h := range hashed {
		if h.Kind == format.ObjectKindSnapshot {
			snapBytes[h.ID] = h.Bytes
		}
	}

	discBuf, discHash, err := buildDisc(opts, packTime)
	if err != nil {
		return nil, err
	}
	refsBuf, refsHash, err := buildRefs(opts)
	if err != nil {
		return nil, err
	}
	discsBuf, discsHash, err := buildDiscs(opts, packTime)
	if err != nil {
		return nil, err
	}
	decoderHash := sha256.Sum256(DecoderPy)

	var rows []fileRow
	fileIndex := make(map[object.ID]int)

	indexRowIdx := len(rows)
	rows = append(rows, fileRow{role: format.FileRoleIndex, path: "NOAHSARK/runs/%RUNSEQ%/INDEX.bin", inStream: true})
	runRowIdx := len(rows)
	rows = append(rows, fileRow{role: format.FileRoleRun, byteLen: RunFileLen, path: "NOAHSARK/runs/%RUNSEQ%/RUN.bin"})
	rows = append(rows, fileRow{role: format.FileRoleDisc, byteLen: uint64(len(discBuf)), hash: discHash, data: discBuf, path: "NOAHSARK/DISC.bin", inStream: true})
	rows = append(rows, fileRow{role: format.FileRoleReference, byteLen: uint64(len(DecoderPy)), hash: decoderHash, data: DecoderPy, path: "NOAHSARK/REFERENCE/decoder.py", inStream: true})
	rows = append(rows, fileRow{role: format.FileRoleRefs, byteLen: uint64(len(refsBuf)), hash: refsHash, data: refsBuf, path: "NOAHSARK/runs/%RUNSEQ%/catalog/REFS.bin", inStream: true})
	rows = append(rows, fileRow{role: format.FileRoleDiscs, byteLen: uint64(len(discsBuf)), hash: discsHash, data: discsBuf, path: "NOAHSARK/runs/%RUNSEQ%/catalog/DISCS.bin", inStream: true})

	for _, id := range snapObjOrder {
		data := snapBytes[id]
		h := sha256.Sum256(data)
		rows = append(rows, fileRow{
			role: format.FileRoleSnapobj, byteLen: uint64(len(data)), hash: h, data: data,
			path: filepath.ToSlash(filepath.Join("NOAHSARK/runs/%RUNSEQ%/catalog/snapobj", id.TextForm())), inStream: true,
		})
	}

	for _, h := range hashed {
		var p string
		if h.Kind == format.ObjectKindSnapshot {
			p = filepath.ToSlash(filepath.Join("NOAHSARK/snapshots", h.ID.TextForm()))
		} else {
			p = filepath.ToSlash(filepath.Join("NOAHSARK/objects", h.ID.FanoutByte(), h.ID.TextForm()))
		}
		fileIndex[h.ID] = len(rows)
		rows = append(rows, fileRow{role: format.FileRoleObject, byteLen: uint64(len(h.Bytes)), hash: h.hash, data: h.Bytes, path: p, inStream: true})
	}

	// Stream sizes, in row order, for every row that is part of the FEC
	// stream. INDEX's own size is computed by formula: its content is
	// not needed to know its length, only the row and table counts,
	// which are already fixed at this point.
	objectCount := len(hashed)
	fileCount := len(rows) + 1 /* checksum */ + fec.M /* parity */ + 1 /* RUN2 */
	indexLen := format.IndexHeaderLen + fileCount*format.IndexFileRecordLen +
		objectCount*format.IndexObjectRecordLen
	rows[indexRowIdx].byteLen = uint64(indexLen)

	var streamSizes []uint64
	for _, r := range rows {
		if r.inStream {
			streamSizes = append(streamSizes, r.byteLen)
		}
	}
	layout, err := fec.NewStreamLayout(streamSizes, fec.K)
	if err != nil {
		return nil, err
	}
	L := layout.StripeCount()

	checksumLen := L * fec.BlockSize
	parityFileLen := (L + 1) * fec.BlockSize

	checksumRowIdx := len(rows)
	rows = append(rows, fileRow{role: format.FileRoleChecksum, byteLen: checksumLen, path: "NOAHSARK/runs/%RUNSEQ%/checksum.bin"})
	parityRowStart := len(rows)
	for j := 0; j < fec.M; j++ {
		rows = append(rows, fileRow{
			role: format.FileRoleParity, byteLen: parityFileLen,
			path: fmt.Sprintf("NOAHSARK/runs/%%RUNSEQ%%/parity/p%04d.bin", fec.K+1+j),
		})
	}
	run2RowIdx := len(rows)
	rows = append(rows, fileRow{role: format.FileRoleRun2, byteLen: RunFileLen, path: "NOAHSARK/runs/%RUNSEQ%/RUN2.bin"})

	streamBytesTotal := streamTotal(streamSizes)
	if err := CheckCapacity(streamBytesTotal, checksumLen, uint64(fec.M)*parityFileLen, 2*RunFileLen, len(rows), opts.TargetCapacitySectors); err != nil {
		return nil, err
	}

	// Objects table, sorted by content id.
	objRows := make([]format.IndexObjectRecord, objectCount)
	for i, h := range hashed {
		var storedLen, payloadLen uint64
		var compression format.Compression
		if err := readObjectHeader(h.Bytes, &storedLen, &payloadLen, &compression); err != nil {
			return nil, fmt.Errorf("image: %s: %w", h.ID.TextForm(), err)
		}
		var flags uint16
		if h.Kind != format.ObjectKindChunk {
			flags |= 0x2
		}
		objRows[i] = format.IndexObjectRecord{
			ContentID:   h.ID,
			FileIndex:   uint32(fileIndex[h.ID]),
			StoredLen:   storedLen,
			PayloadLen:  payloadLen,
			Kind:        h.Kind,
			Compression: compression,
			Flags:       flags,
		}
	}
	sort.Slice(objRows, func(i, j int) bool {
		return lessBytes(objRows[i].ContentID[:], objRows[j].ContentID[:])
	})

	idxFiles := make([]format.IndexFileRecord, len(rows))
	for i, r := range rows {
		idxFiles[i] = format.IndexFileRecord{FileHash: r.hash, ByteLen: r.byteLen, Role: r.role}
	}

	idx := format.Index{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex,
			VersionMajor: 1, VersionMinor: 0, HeaderLen: format.IndexHeaderLen,
		},
		RunSeq: buildRunSeq, FileCount: uint32(len(rows)), ObjectCount: uint32(objectCount),
		PrereqCount: 0, FileRecordSize: format.IndexFileRecordLen,
		ObjectRecordSize: format.IndexObjectRecordLen, PrereqRecordSize: format.IndexPrereqRecordLen,
		HashAlgo: format.HashAlgoSHA256, DigestLen: 32,
		Files: idxFiles, Objects: objRows,
	}
	indexBuf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(indexBuf); err != nil {
		return nil, err
	}
	if len(indexBuf) != indexLen {
		return nil, fmt.Errorf("image: internal error: index length mismatch, predicted %d, actual %d", indexLen, len(indexBuf))
	}
	rows[indexRowIdx].data = indexBuf
	indexHash := sha256.Sum256(indexBuf)

	runBuf, err := buildRun(opts, packTime, indexBuf, indexHash, streamBytesTotal, uint64(objectCount))
	if err != nil {
		return nil, err
	}
	rows[runRowIdx].data = runBuf
	rows[run2RowIdx].data = runBuf

	// Build the FEC stream bytes: every in-stream row's bytes, zero
	// padded to a block boundary, concatenated in row order.
	var stream []byte
	for _, r := range rows {
		if !r.inStream {
			continue
		}
		stream = append(stream, r.data...)
		if pad := padLen(len(r.data)); pad > 0 {
			stream = append(stream, make([]byte, pad)...)
		}
	}

	checksumBuf, parityBufs, err := buildFEC(stream, layout, runBuf)
	if err != nil {
		return nil, err
	}
	rows[checksumRowIdx].data = checksumBuf
	for j := 0; j < fec.M; j++ {
		rows[parityRowStart+j].data = parityBufs[j]
	}

	seqDir := fmt.Sprintf("%010d", buildRunSeq)
	for _, r := range rows {
		path := filepath.FromSlash(replaceRunSeq(r.path, seqDir))
		full := filepath.Join(opts.OutputDir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, r.data, 0o644); err != nil {
			return nil, err
		}
	}

	return &Result{
		RunSeq: buildRunSeq, DiscSeq: buildDiscSeq, ObjectCount: objectCount,
		FileCount: len(rows), StreamBlocks: layout.BlockCount(), StripeCount: L,
	}, nil
}

func replaceRunSeq(path, seqDir string) string {
	out := make([]byte, 0, len(path))
	const tok = "%RUNSEQ%"
	for {
		i := indexOf(path, tok)
		if i < 0 {
			out = append(out, path...)
			break
		}
		out = append(out, path[:i]...)
		out = append(out, seqDir...)
		path = path[i+len(tok):]
	}
	return string(out)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func padLen(n int) int {
	rem := n % SectorSize
	if rem == 0 {
		return 0
	}
	return SectorSize - rem
}

func streamTotal(sizes []uint64) uint64 {
	var total uint64
	for _, s := range sizes {
		total += s + uint64(padLen(int(s)))
	}
	return total
}

func lessBytes(a, b []byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// readObjectHeader reads the stored length, payload length and
// compression id out of an already-staged object file's common and
// object header, without a type-specific decode.
func readObjectHeader(data []byte, storedLen, payloadLen *uint64, compression *format.Compression) error {
	var h format.CommonHeader
	if err := h.Decode(data); err != nil {
		return err
	}
	var oh format.ObjectHeader
	if err := oh.Decode(data[format.CommonHeaderLen:]); err != nil {
		return err
	}
	*storedLen = oh.StoredLen
	*payloadLen = oh.PayloadLen
	*compression = oh.Compression
	return nil
}
