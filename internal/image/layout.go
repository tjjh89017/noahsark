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
	// FECEnabled writes a Reed-Solomon checksum column and parity for
	// this run when true (fec_scheme 1). When false, the default, the
	// run carries no FEC (fec_scheme 0): burning two identical discs is
	// the primary redundancy this project relies on.
	FECEnabled bool
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
	// RUN2, checksum, parity, and INDEX itself) or when srcPath names
	// the bytes instead.
	srcPath  string // staged file to stream-copy from; set instead of data for a chunk-sized object.
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
	reachable, err := CollectReachable(opts.StagingDir, snapIDs)
	if err != nil {
		return nil, err
	}
	// Sort object rows by their whole-file hash, the order INDEX's Files
	// table gives for role 13 rows. A chunk's hash is read by streaming
	// its staged file; its bytes are never held whole in memory here.
	type hashedObject struct {
		ReachableObject
		hash [32]byte
	}
	hashed := make([]hashedObject, len(reachable))
	for i, r := range reachable {
		var h [32]byte
		if r.Bytes != nil {
			h = sha256.Sum256(r.Bytes)
		} else {
			h, err = hashFile(StagedPath(opts.StagingDir, r.ID, r.Kind))
			if err != nil {
				return nil, fmt.Errorf("image: %s: %w", r.ID.TextForm(), err)
			}
		}
		hashed[i] = hashedObject{ReachableObject: r, hash: h}
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

	discBuf, discHash, err := buildDisc(opts, packTime, buildDiscSeq)
	if err != nil {
		return nil, err
	}
	var label [64]byte
	labelLen := copy(label[:], opts.Label)
	readmeBuf := buildReadme(opts, packTime, label[:labelLen])
	readmeHash := sha256.Sum256(readmeBuf)
	formatHash := sha256.Sum256(FormatTxt)
	refsBuf, refsHash, err := buildRefs(opts)
	if err != nil {
		return nil, err
	}
	discsBuf, discsHash, err := buildDiscs(opts, packTime, buildRunSeq, buildDiscSeq, nil)
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
	rows = append(rows, fileRow{role: format.FileRoleReadme, byteLen: uint64(len(readmeBuf)), hash: readmeHash, data: readmeBuf, path: "NOAHSARK/README.txt", inStream: true})
	rows = append(rows, fileRow{role: format.FileRoleFormat, byteLen: uint64(len(FormatTxt)), hash: formatHash, data: FormatTxt, path: "NOAHSARK/FORMAT.txt", inStream: true})
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
		row := fileRow{role: format.FileRoleObject, byteLen: h.ByteLen, hash: h.hash, path: p, inStream: true}
		if h.Bytes != nil {
			row.data = h.Bytes
		} else {
			row.srcPath = StagedPath(opts.StagingDir, h.ID, h.Kind)
		}
		rows = append(rows, row)
	}

	// Stream sizes, in row order, for every row that is part of the FEC
	// stream. INDEX's own size is computed by formula: its content is
	// not needed to know its length, only the row and table counts,
	// which are already fixed at this point.
	objectCount := len(hashed)
	fileCount := len(rows) + extraFixedRowCount(opts.FECEnabled)
	indexLen := format.IndexHeaderLen + fileCount*format.IndexFileRecordLen +
		objectCount*format.IndexObjectRecordLen
	rows[indexRowIdx].byteLen = uint64(indexLen)

	plan, err := appendFECRows(rows, opts.FECEnabled)
	if err != nil {
		return nil, err
	}
	rows = plan.rows
	run2RowIdx := plan.run2RowIdx

	if err := CheckCapacity(plan.streamBytesTotal, plan.checksumLen, uint64(fec.M)*plan.parityFileLen, 2*RunFileLen, len(rows), opts.TargetCapacitySectors); err != nil {
		return nil, err
	}

	// Objects table, sorted by content id.
	objRows := make([]format.IndexObjectRecord, objectCount)
	for i, h := range hashed {
		var storedLen, payloadLen uint64
		var compression format.Compression
		var err error
		if h.Bytes != nil {
			err = readObjectHeader(h.Bytes, &storedLen, &payloadLen, &compression)
		} else {
			storedLen, payloadLen, compression, err = readObjectHeaderFile(StagedPath(opts.StagingDir, h.ID, h.Kind))
		}
		if err != nil {
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

	runBuf, err := buildRun(opts, packTime, indexBuf, indexHash, plan.streamBytesTotal, uint64(objectCount), buildRunSeq, buildDiscSeq, opts.FECEnabled)
	if err != nil {
		return nil, err
	}
	rows[runRowIdx].data = runBuf
	rows[run2RowIdx].data = runBuf
	plan.rows = rows

	if err := writeRunTree(opts.OutputDir, buildRunSeq, plan, runBuf); err != nil {
		return nil, err
	}

	return &Result{
		RunSeq: buildRunSeq, DiscSeq: buildDiscSeq, ObjectCount: objectCount,
		FileCount: len(rows), StreamBlocks: blockCount(plan.streamBytesTotal), StripeCount: plan.stripeCount,
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

// extraFixedRowCount is the number of Files rows a run adds on top of
// its INDEX, its fixed named files and its object rows: RUN2.bin always,
// plus checksum.bin and fec.M parity files when the run carries FEC.
func extraFixedRowCount(fecEnabled bool) int {
	if fecEnabled {
		return 1 + fec.M + 1 // checksum, parity, RUN2
	}
	return 1 // RUN2 only
}

// fecPlan holds what appending the checksum, parity and RUN2 rows
// produced: the extended rows slice, the row indices buildFECToDisk and
// the writer need, and the geometry a Result reports. checksumRowIdx and
// parityRowStart are -1 when the run carries no FEC.
type fecPlan struct {
	rows             []fileRow
	checksumRowIdx   int
	parityRowStart   int
	run2RowIdx       int
	streamBytesTotal uint64
	checksumLen      uint64
	parityFileLen    uint64
	stripeCount      uint64
	layout           *fec.StreamLayout // nil when the run carries no FEC
}

// appendFECRows appends the checksum, parity and RUN2 rows to rows,
// when fecEnabled, and always appends RUN2. It is shared by Build and
// Pack so the two lay out a run's file order identically.
func appendFECRows(rows []fileRow, fecEnabled bool) (fecPlan, error) {
	var streamSizes []uint64
	for _, r := range rows {
		if r.inStream {
			streamSizes = append(streamSizes, r.byteLen)
		}
	}
	plan := fecPlan{checksumRowIdx: -1, parityRowStart: -1}
	if fecEnabled {
		layout, err := fec.NewStreamLayout(streamSizes, fec.K)
		if err != nil {
			return fecPlan{}, err
		}
		L := layout.StripeCount()
		plan.layout = layout
		plan.stripeCount = L
		plan.checksumLen = L * fec.BlockSize
		plan.parityFileLen = (L + 1) * fec.BlockSize

		plan.checksumRowIdx = len(rows)
		rows = append(rows, fileRow{role: format.FileRoleChecksum, byteLen: plan.checksumLen, path: "NOAHSARK/runs/%RUNSEQ%/checksum.bin"})
		plan.parityRowStart = len(rows)
		for j := range fec.M {
			rows = append(rows, fileRow{
				role: format.FileRoleParity, byteLen: plan.parityFileLen,
				path: fmt.Sprintf("NOAHSARK/runs/%%RUNSEQ%%/parity/p%04d.bin", fec.K+1+j),
			})
		}
	}
	plan.run2RowIdx = len(rows)
	rows = append(rows, fileRow{role: format.FileRoleRun2, byteLen: RunFileLen, path: "NOAHSARK/runs/%RUNSEQ%/RUN2.bin"})
	plan.rows = rows
	plan.streamBytesTotal = streamTotal(streamSizes)
	return plan, nil
}

// writeRunTree writes every row of plan.rows to its final path under
// outputDir (runRowIdx and run2RowIdx's bytes must already be set in
// rows), skipping the checksum and parity rows, then, when the run
// carries FEC, computes the checksum column and the parity straight to
// disk from the files just written.
func writeRunTree(outputDir string, runSeq uint64, plan fecPlan, runBuf []byte) error {
	rows := plan.rows
	seqDir := fmt.Sprintf("%010d", runSeq)
	finalPaths := make([]string, len(rows))
	for i, r := range rows {
		path := filepath.FromSlash(replaceRunSeq(r.path, seqDir))
		full := filepath.Join(outputDir, path)
		finalPaths[i] = full
		if i == plan.checksumRowIdx || (plan.parityRowStart >= 0 && i >= plan.parityRowStart && i < plan.parityRowStart+fec.M) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if r.srcPath != "" {
			if err := copyFileStream(r.srcPath, full, 0o644); err != nil {
				return err
			}
		} else if err := os.WriteFile(full, r.data, 0o644); err != nil {
			return err
		}
	}

	if plan.layout == nil {
		return nil
	}

	var sources []streamSource
	for i, r := range rows {
		if !r.inStream {
			continue
		}
		sources = append(sources, streamSource{path: finalPaths[i], size: r.byteLen})
	}
	if err := os.MkdirAll(filepath.Dir(finalPaths[plan.checksumRowIdx]), 0o755); err != nil {
		return err
	}
	parityPaths := make([]string, fec.M)
	for j := range fec.M {
		parityPaths[j] = finalPaths[plan.parityRowStart+j]
	}
	if err := os.MkdirAll(filepath.Dir(parityPaths[0]), 0o755); err != nil {
		return err
	}
	return buildFECToDisk(sources, plan.layout, runBuf, finalPaths[plan.checksumRowIdx], parityPaths)
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
