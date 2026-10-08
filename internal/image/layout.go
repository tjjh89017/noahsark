package image

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// SnapshotRef binds a name to a snapshot id, for one REFS record. Name
// is typically the commit date, as YYYY-MM-DD.
type SnapshotRef struct {
	Name string
	ID   object.ID
	Time time.Time
}

// BuildOptions holds everything Build needs to lay out one run.
type BuildOptions struct {
	// ObjectPath gives the file path of an object from its kind and id.
	ObjectPath ObjectPathFunc
	// Snapshots names every snapshot this run stores, and the REFS
	// records that point at them. This build writes one run per disc, so
	// this run stores every object every listed snapshot reaches.
	Snapshots []SnapshotRef
	// TargetCapacitySectors is the pack limit. Build refuses to run
	// without it.
	TargetCapacitySectors uint64
	// OutputDir receives the NOAHSARK tree.
	OutputDir string
	RepoUUID  [16]byte
	DiscUUID  [16]byte
	Label     string
	// Now returns the pack time. Defaults to time.Now.
	Now func() time.Time
	// Progress reports bytes of object content placed into the run's
	// tree. A nil Progress reports nothing.
	Progress *progress.Reporter
}

// Result summarizes one Build call.
type Result struct {
	RunSeq      uint64
	DiscSeq     uint64
	ObjectCount int
	FileCount   int
}

// toolVersion is registry id 1, the reference implementation, version 1.
const toolVersion = uint32(1)<<24 | 1

// runSeq and discSeq are fixed: one run on one disc, no
// append.
const (
	buildRunSeq  uint64 = 1
	buildDiscSeq uint64 = 0
)

// fileRow is one row-to-be of INDEX's Files table, plus the bytes to
// write to the output tree.
type fileRow struct {
	role    uint8
	byteLen uint64
	hash    [32]byte // zero for a row whose bytes are not final until
	// after INDEX itself is built; see docs/decisions.md, "Pack".
	data []byte // bytes to write; nil when filled in later (RUN,
	// RUN2 and INDEX itself) or when srcPath names the bytes instead.
	srcPath string    // staged file to stream-copy from; set instead of data for a chunk-sized object.
	srcID   object.ID // content id the copy of srcPath must hash to.
	path    string    // path under OutputDir, relative, forward slashes.
}

// Build lays out one run over the snapshots opts names and writes the
// full NOAHSARK tree under opts.OutputDir as ordinary files.
func Build(opts BuildOptions) (*Result, error) {
	if opts.ObjectPath == nil || opts.OutputDir == "" {
		return nil, fmt.Errorf("an object path function and an output directory are required")
	}
	if len(opts.Snapshots) == 0 {
		return nil, fmt.Errorf("at least one snapshot is required")
	}
	if opts.TargetCapacitySectors == 0 {
		return nil, errNoCapacity
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
	reachable, err := CollectReachable(opts.ObjectPath, snapIDs)
	if err != nil {
		return nil, err
	}
	// The role 13 rows and the Objects rows pair by position, and both
	// are in ascending content id order.
	sort.Slice(reachable, func(i, j int) bool {
		return lessBytes(reachable[i].ID[:], reachable[j].ID[:])
	})

	discBuf, discHash, err := buildDisc(opts, packTime, buildDiscSeq)
	if err != nil {
		return nil, err
	}
	var label [64]byte
	labelLen := copy(label[:], opts.Label)
	readmeBuf := buildReadme(opts, packTime, label[:labelLen], buildDiscSeq)
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

	var rows []fileRow

	indexRowIdx := len(rows)
	rows = append(rows, fileRow{role: format.FileRoleIndex, path: "NOAHSARK/runs/%RUNSEQ%/INDEX.bin"})
	runRowIdx := len(rows)
	rows = append(rows, fileRow{role: format.FileRoleRun, byteLen: RunFileLen, path: "NOAHSARK/runs/%RUNSEQ%/RUN.bin"})
	rows = append(rows, fileRow{role: format.FileRoleDisc, byteLen: uint64(len(discBuf)), hash: discHash, data: discBuf, path: "NOAHSARK/DISC.bin"})
	rows = append(rows, fileRow{role: format.FileRoleReadme, byteLen: uint64(len(readmeBuf)), hash: readmeHash, data: readmeBuf, path: "NOAHSARK/README.txt"})
	rows = append(rows, fileRow{role: format.FileRoleFormat, byteLen: uint64(len(FormatTxt)), hash: formatHash, data: FormatTxt, path: "NOAHSARK/FORMAT.txt"})
	rows = append(rows, fileRow{role: format.FileRoleRefs, byteLen: uint64(len(refsBuf)), hash: refsHash, data: refsBuf, path: "NOAHSARK/runs/%RUNSEQ%/catalog/REFS.bin"})
	rows = append(rows, fileRow{role: format.FileRoleDiscs, byteLen: uint64(len(discsBuf)), hash: discsHash, data: discsBuf, path: "NOAHSARK/runs/%RUNSEQ%/catalog/DISCS.bin"})

	for _, h := range reachable {
		row := fileRow{role: format.FileRoleObject, byteLen: h.ByteLen, path: objectDiscPath(h.ID, h.Kind)}
		if h.Bytes != nil {
			row.data = h.Bytes
		} else {
			row.srcPath = opts.ObjectPath(h.Kind, h.ID)
			row.srcID = h.ID
		}
		rows = append(rows, row)
	}

	// INDEX's own size is computed by formula: its content is not needed
	// to know its length, only the row and table counts, which are
	// already fixed at this point.
	objectCount := len(reachable)
	fileCount := len(rows) + run2RowCount
	indexLen := format.IndexHeaderLen + fileCount*format.IndexFileRecordLen +
		objectCount*format.IndexObjectRecordLen
	rows[indexRowIdx].byteLen = uint64(indexLen)

	rows, run2RowIdx := appendRun2Row(rows)

	if err := CheckCapacity(fileBytes(rows), 2*RunFileLen, len(rows), opts.TargetCapacitySectors); err != nil {
		return nil, err
	}

	// Objects table, in the content id order of the role 13 rows.
	objRows := make([]format.IndexObjectRecord, objectCount)
	for i, h := range reachable {
		objRows[i] = format.IndexObjectRecord{ContentID: h.ID, Kind: h.Kind}
	}

	idxFiles := make([]format.IndexFileRecord, len(rows))
	for i, r := range rows {
		idxFiles[i] = format.IndexFileRecord{FileHash: r.hash, ByteLen: r.byteLen, Role: r.role}
	}

	idx := format.Index{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex,
			VersionMajor: 1, HeaderLen: format.IndexHeaderLen,
		},
		RunSeq: buildRunSeq, FileCount: uint32(len(rows)), ObjectCount: uint32(objectCount),
		PrereqCount: 0,
		Files:       idxFiles, Objects: objRows,
	}
	indexBuf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(indexBuf); err != nil {
		return nil, err
	}
	if len(indexBuf) != indexLen {
		return nil, indexLengthError(indexLen, len(indexBuf))
	}
	rows[indexRowIdx].data = indexBuf
	indexHash := sha256.Sum256(indexBuf)

	runBuf, err := buildRun(opts, packTime, indexBuf, indexHash, buildRunSeq, buildDiscSeq)
	if err != nil {
		return nil, err
	}
	rows[runRowIdx].data = runBuf
	rows[run2RowIdx].data = runBuf

	if err := writeRunTree(opts.OutputDir, buildRunSeq, rows, opts.Progress); err != nil {
		return nil, err
	}

	return &Result{
		RunSeq: buildRunSeq, DiscSeq: buildDiscSeq, ObjectCount: objectCount,
		FileCount: len(rows),
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

// run2RowCount is the number of Files rows a run adds after its INDEX,
// its fixed named files and its object rows: RUN2.bin, the last file of
// the fill order.
const run2RowCount = 1

// appendRun2Row appends the RUN2 row, the last row of the fill order, and
// returns the rows with the index of that row. Build and Pack share it,
// so the two lay out a run's file order identically.
func appendRun2Row(rows []fileRow) ([]fileRow, int) {
	idx := len(rows)
	return append(rows, fileRow{role: format.FileRoleRun2, byteLen: RunFileLen, path: "NOAHSARK/runs/%RUNSEQ%/RUN2.bin"}), idx
}

// fileBytes returns the file_bytes term of the capacity budget: the
// length of every file of rows other than the two run header copies,
// each rounded up to a whole sector.
func fileBytes(rows []fileRow) uint64 {
	var total uint64
	for _, r := range rows {
		if r.role == format.FileRoleRun || r.role == format.FileRoleRun2 {
			continue
		}
		total += sectorCount(r.byteLen) * SectorSize
	}
	return total
}

// writeRunTree writes every row of rows to its final path under
// outputDir. The bytes of the RUN and RUN2 rows must already be set.
// prog reports bytes of object rows placed; a nil prog reports nothing.
//
// Every file it writes, and every directory that received a new entry,
// is flushed to stable storage before it returns. Pack records the run
// PACKED right after this call, and that record must never outlive the
// bytes it claims.
func writeRunTree(outputDir string, runSeq uint64, rows []fileRow, prog *progress.Reporter) error {
	seqDir := fmt.Sprintf("%010d", runSeq)

	var objectBytesTotal int64
	for _, r := range rows {
		if r.role == format.FileRoleObject {
			objectBytesTotal += int64(r.byteLen)
		}
	}
	prog.Start("pack: objects placed", objectBytesTotal)
	for _, r := range rows {
		full := filepath.Join(outputDir, filepath.FromSlash(replaceRunSeq(r.path, seqDir)))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if r.srcPath != "" {
			if err := copyFileStream(r.srcPath, full, 0o644, r.srcID, r.byteLen); err != nil {
				return err
			}
		} else if err := os.WriteFile(full, r.data, 0o644); err != nil {
			return err
		}
		if r.role == format.FileRoleObject {
			prog.Add(int64(r.byteLen))
		}
	}
	prog.Done()
	return syncTree(outputDir)
}

func lessBytes(a, b []byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// objectDiscPath is the path of one object file inside the run tree:
// snapshots/ for a snapshot, objects/<ab>/ for every other kind.
func objectDiscPath(id object.ID, kind format.ObjectKind) string {
	if kind == format.ObjectKindSnapshot {
		return filepath.ToSlash(filepath.Join("NOAHSARK/snapshots", id.TextForm()))
	}
	return filepath.ToSlash(filepath.Join("NOAHSARK/objects", id.FanoutByte(), id.TextForm()))
}
