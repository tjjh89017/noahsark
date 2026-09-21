package image

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// discsLedgerName is the local repository ledger of every disc pack has
// already built. It reuses DISCS.bin's own container format directly,
// with run_hash filled in as soon as it is known, since that is the
// exact same information a real DISCS table carries for an earlier run
// and this build has no burn step to read it back from a drive. See
// docs/decisions.md, "12. Disc lifecycle, closing and appending".
const discsLedgerName = "discs.bin"

// DiscsLedgerName is discsLedgerName, exported for recover, which
// reports the ledger path it rewrote.
const DiscsLedgerName = discsLedgerName

// refsLedgerName is the local repository ledger of every ref pack has
// already written into a run's refs.bin. It reuses REFS's own container
// format directly, the same way discsLedgerName mirrors DISCS: a fresh
// pack loads it, merges in the refs named on its own command line, and
// saves the merged set back, so every run's refs.bin carries every ref
// the repository knows, not only the ones packed this time.
const refsLedgerName = "refslog.bin"

// RefsLedgerName is refsLedgerName, exported for recover, which
// reports the ledger path it rewrote.
const RefsLedgerName = refsLedgerName

// PackOptions holds everything Pack needs to select the next run's
// objects from the staging store and lay it out.
type PackOptions struct {
	// StagingDir is the staging directory objects, snapshots and the
	// state log live under.
	StagingDir string
	// Snapshots names the refs this run's REFS table carries.
	Snapshots []SnapshotRef
	// TargetCapacitySectors is the pack limit. Pack refuses to run
	// without it.
	TargetCapacitySectors uint64
	// PhysicalCapacitySectors is the disc's reported capacity.
	PhysicalCapacitySectors uint64
	// OutputDir receives the NOAHSARK tree.
	OutputDir string
	RepoUUID  [16]byte
	DiscUUID  [16]byte
	Label     string
	MediaType format.MediaType
	// FECEnabled writes a Reed-Solomon checksum column and parity for
	// this run when true (fec_scheme 1). When false, the default, the
	// run carries no FEC (fec_scheme 0).
	FECEnabled bool
	// Now returns the pack time. Defaults to time.Now.
	Now func() time.Time
	// StageLog is the repository's staging state log. Pack reads it to
	// select STAGED objects and appends a Packed record for every
	// object this run stores.
	StageLog *stage.Log
	// Progress reports bytes of object content placed into the run's
	// tree, and FEC stripes encoded when FECEnabled. A nil Progress
	// reports nothing.
	Progress *progress.Reporter
}

// PackResult summarizes one Pack call: the run it built, plus what is
// left STAGED afterward.
type PackResult struct {
	Result
	// ObjectBytes is the staged byte length of every object this run
	// placed, the number the operator sees leave staging for the disc.
	ObjectBytes      uint64
	RemainingObjects int
	RemainingBytes   uint64
}

// ErrCapacityTooSmall reports a target capacity that cannot place even
// one object: the run's own fixed files (INDEX, RUN, DISC, README,
// FORMAT, decoder, REFS, DISCS and every snapshot object) already use
// the whole budget, or the smallest candidate object still does not
// fit what is left. Pack returns this instead of an internal error
// whenever selectRun places nothing. NeededSectors, when nonzero, is
// the smallest target capacity that would let this same run place its
// first object.
//
// A capacity that holds some of the staged data is not an error: pack
// places what fits and leaves the rest staged for the next disc. Only a
// capacity that holds nothing at all comes here, so the error names the
// smallest staged object, the one object the capacity must grow to
// hold, and not the size of all the staged data.
type ErrCapacityTooSmall struct {
	TargetSectors uint64
	NeededSectors uint64
	SmallestID    object.ID
	SmallestKind  format.ObjectKind
	SmallestBytes uint64
}

func (e *ErrCapacityTooSmall) Error() string {
	return fmt.Sprintf("target capacity of %d sectors (%d bytes) holds not one object; the smallest staged object is %s %s, %d bytes",
		e.TargetSectors, e.TargetSectors*SectorSize,
		kindName(e.SmallestKind), e.SmallestID.TextForm(), e.SmallestBytes)
}

// ErrCapacityExceedsPhysical reports a target capacity above the disc's
// physical capacity. The disc is write-once, so a target above the
// physical size can never fit. Pack returns this before it does any
// other work, so no output directory is written and no state changes.
type ErrCapacityExceedsPhysical struct {
	TargetSectors   uint64
	PhysicalSectors uint64
}

func (e *ErrCapacityExceedsPhysical) Error() string {
	return fmt.Sprintf("target capacity of %d sectors (%d bytes) exceeds physical capacity of %d sectors (%d bytes)",
		e.TargetSectors, e.TargetSectors*SectorSize, e.PhysicalSectors, e.PhysicalSectors*SectorSize)
}

// refNamesText joins snapshots' ref names for an error message, as
// "ref X" for one snapshot or "refs X, Y" for several.
func refNamesText(snapshots []SnapshotRef) string {
	names := make([]string, len(snapshots))
	for i, s := range snapshots {
		names[i] = s.Name
	}
	if len(names) == 1 {
		return "ref " + names[0]
	}
	return "refs " + strings.Join(names, ", ")
}

// packUnit is one candidate object in dependency order: a tree, blob,
// chunk or snapshot, with the direct child ids a metadata object (tree,
// blob or snapshot) references, and the object's own file bytes when
// already read.
type packUnit struct {
	ID       object.ID
	Kind     format.ObjectKind
	Children []object.ID
	Bytes    []byte // nil for a chunk: its payload stays on staging disk.
	ByteLen  uint64 // set once selectRun has sized the candidate.
}

// Pack selects the STAGED objects for exactly one run within
// opts.TargetCapacitySectors, in this build's locality order (a snapshot's
// tree and blob objects with their chunks, where the budget allows),
// writes the run's NOAHSARK tree the same way Build does, and appends
// every object it packed as Packed to opts.StageLog. Every snapshot
// object in the repository is always written to this run's catalog,
// whatever its own state.
func Pack(opts PackOptions) (*PackResult, error) {
	if opts.StagingDir == "" || opts.OutputDir == "" {
		return nil, fmt.Errorf("staging directory and output directory are required")
	}
	if opts.TargetCapacitySectors == 0 {
		return nil, fmt.Errorf("target capacity is required and must not be zero")
	}
	if opts.PhysicalCapacitySectors != 0 && opts.TargetCapacitySectors > opts.PhysicalCapacitySectors {
		return nil, &ErrCapacityExceedsPhysical{TargetSectors: opts.TargetCapacitySectors, PhysicalSectors: opts.PhysicalCapacitySectors}
	}
	if opts.StageLog == nil {
		return nil, fmt.Errorf("a staging state log is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	packTime := now()

	allSnapshotIDs, err := listSnapshots(opts.StagingDir)
	if err != nil {
		return nil, err
	}
	if len(allSnapshotIDs) == 0 && len(opts.Snapshots) == 0 {
		// Nothing staged, and no ref resolved to a snapshot either: this
		// repository has never had a commit. gc can also empty
		// staging/snapshots once every object of an old, fully packed
		// snapshot goes CLEAN and is deleted; opts.Snapshots, resolved
		// from refs.txt before Pack runs, still names that snapshot
		// then, so this case is left to the "nothing to pack" message
		// below instead of being reported as never committed.
		return nil, fmt.Errorf("no snapshot has been committed")
	}

	onDisc := func(id object.ID) bool {
		rec, ok := opts.StageLog.Get(id)
		return ok && rec.State.OnDisc()
	}
	order, snapshotBytes, err := buildPackOrder(opts.StagingDir, allSnapshotIDs, onDisc)
	if err != nil {
		return nil, err
	}

	var candidates []packUnit
	for _, u := range order {
		rec, ok := opts.StageLog.Get(u.ID)
		if ok && rec.State.OnDisc() {
			continue
		}
		if !ok {
			// Defensive: every committed object should already carry a
			// Staged record. Treat an unrecorded object as staged
			// rather than silently dropping it from selection.
			if err := opts.StageLog.EnsureStaged(u.ID); err != nil {
				return nil, err
			}
		}
		candidates = append(candidates, u)
	}
	if len(candidates) == 0 {
		if len(opts.Snapshots) == 0 {
			return nil, fmt.Errorf("nothing to pack: no staged object remains")
		}
		return nil, fmt.Errorf("nothing to pack: every object of %s is already on a disc", refNamesText(opts.Snapshots))
	}

	ledger, err := LoadDiscsLedger(opts.StagingDir, opts.RepoUUID)
	if err != nil {
		return nil, err
	}
	runSeq, discSeq := NextSeqNumbers(ledger.Rows)

	discBuf, discHash, err := buildDisc(opts.asBuildOptions(), packTime, discSeq)
	if err != nil {
		return nil, err
	}
	refsLedger, err := LoadRefsLedger(opts.StagingDir, opts.RepoUUID)
	if err != nil {
		return nil, err
	}
	newRefRecords := refRecordsFromSnapshots(opts.Snapshots, runSeq)
	mergedRefRecords := mergeRefRecords(refsLedger.Records, newRefRecords)
	refsBuf, refsHash, err := encodeRefsTable(opts.RepoUUID, mergedRefRecords)
	if err != nil {
		return nil, err
	}
	discsBuf, discsHash, err := buildDiscs(opts.asBuildOptions(), packTime, runSeq, discSeq, ledger.Rows)
	if err != nil {
		return nil, err
	}
	var label [64]byte
	labelLen := copy(label[:], opts.Label)
	readmeBuf := buildReadme(opts.asBuildOptions(), packTime, label[:labelLen], discSeq)
	readmeHash := sha256.Sum256(readmeBuf)
	formatHash := sha256.Sum256(FormatTxt)
	decoderHash := sha256.Sum256(DecoderPy)

	snapObjOrder := append([]object.ID(nil), allSnapshotIDs...)
	sort.Slice(snapObjOrder, func(i, j int) bool { return lessBytes(snapObjOrder[i][:], snapObjOrder[j][:]) })
	var snapobjBlocks uint64
	for _, id := range snapObjOrder {
		snapobjBlocks += blockCount(uint64(len(snapshotBytes[id])))
	}

	fixedBlocksExclIndex := blockCount(uint64(len(discBuf))) +
		blockCount(uint64(len(readmeBuf))) +
		blockCount(uint64(len(FormatTxt))) +
		blockCount(uint64(len(DecoderPy))) +
		blockCount(uint64(len(refsBuf))) +
		blockCount(uint64(len(discsBuf))) +
		snapobjBlocks
	fixedFileCount := 8 + len(snapObjOrder) // INDEX,RUN,DISC,README,FORMAT,decoder,REFS,DISCS

	selected, prereqIDs, err := selectRun(opts, candidates, fixedBlocksExclIndex, fixedFileCount)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		needed, needErr := minimumSectorsToPlaceOne(opts, candidates, fixedBlocksExclIndex, fixedFileCount)
		if needErr != nil {
			return nil, needErr
		}
		smallest, err := smallestCandidate(opts.StagingDir, candidates)
		if err != nil {
			return nil, err
		}
		return nil, &ErrCapacityTooSmall{
			TargetSectors: opts.TargetCapacitySectors, NeededSectors: needed,
			SmallestID: smallest.ID, SmallestKind: smallest.Kind, SmallestBytes: smallest.ByteLen,
		}
	}

	// A selected chunk's hash is read by streaming its staged file; its
	// bytes are never held whole in memory. Tree, blob and snapshot
	// bytes are already cached from buildPackOrder, small metadata
	// bounded by the tree shape rather than by data size.
	type hashedUnit struct {
		packUnit
		hash [32]byte
	}
	hashed := make([]hashedUnit, len(selected))
	for i, u := range selected {
		var h [32]byte
		if u.Bytes != nil {
			h = sha256.Sum256(u.Bytes)
		} else {
			var err error
			h, err = hashFile(StagedPath(opts.StagingDir, u.ID, u.Kind))
			if err != nil {
				return nil, fmt.Errorf("chunk %s: %w", u.ID.TextForm(), err)
			}
		}
		hashed[i] = hashedUnit{packUnit: u, hash: h}
	}
	sort.Slice(hashed, func(i, j int) bool { return lessBytes(hashed[i].hash[:], hashed[j].hash[:]) })

	prereqs := make([]format.IndexPrereqRecord, 0, len(prereqIDs))
	for id := range prereqIDs {
		rec, ok := opts.StageLog.Get(id)
		if !ok || !rec.State.OnDisc() {
			return nil, fmt.Errorf("internal error: prerequisite %s is not packed", id.TextForm())
		}
		prereqs = append(prereqs, format.IndexPrereqRecord{ContentID: id, RunSeq: rec.RunSeq})
	}
	sort.Slice(prereqs, func(i, j int) bool { return lessBytes(prereqs[i].ContentID[:], prereqs[j].ContentID[:]) })

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
		data := snapshotBytes[id]
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
			row.srcID = h.ID
		}
		rows = append(rows, row)
	}

	objectCount := len(hashed)
	fileCount := len(rows) + extraFixedRowCount(opts.FECEnabled)
	indexLen := format.IndexHeaderLen + fileCount*format.IndexFileRecordLen +
		objectCount*format.IndexObjectRecordLen + len(prereqs)*format.IndexPrereqRecordLen
	rows[indexRowIdx].byteLen = uint64(indexLen)

	plan, err := appendFECRows(rows, opts.FECEnabled)
	if err != nil {
		return nil, err
	}
	rows = plan.rows
	run2RowIdx := plan.run2RowIdx

	if err := CheckCapacity(plan.streamBytesTotal, plan.checksumLen, uint64(fec.M)*plan.parityFileLen, 2*RunFileLen, len(rows), opts.TargetCapacitySectors); err != nil {
		return nil, fmt.Errorf("internal error: selected run does not fit after all: %w", err)
	}

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
			if errors.Is(err, errShortStagedHeader) {
				return nil, stagedDamaged(h.ID, h.Kind)
			}
			return nil, fmt.Errorf("%s: %w", h.ID.TextForm(), err)
		}
		var flags uint16
		if h.Kind != format.ObjectKindChunk {
			flags |= 0x2
		}
		objRows[i] = format.IndexObjectRecord{
			ContentID: h.ID, FileIndex: uint32(fileIndex[h.ID]), StoredLen: storedLen,
			PayloadLen: payloadLen, Kind: h.Kind, Compression: compression, Flags: flags,
		}
	}
	sort.Slice(objRows, func(i, j int) bool { return lessBytes(objRows[i].ContentID[:], objRows[j].ContentID[:]) })

	idxFiles := make([]format.IndexFileRecord, len(rows))
	for i, r := range rows {
		idxFiles[i] = format.IndexFileRecord{FileHash: r.hash, ByteLen: r.byteLen, Role: r.role}
	}

	idx := format.Index{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex,
			VersionMajor: 1, VersionMinor: 0, HeaderLen: format.IndexHeaderLen,
		},
		RunSeq: runSeq, FileCount: uint32(len(rows)), ObjectCount: uint32(objectCount),
		PrereqCount: uint32(len(prereqs)), FileRecordSize: format.IndexFileRecordLen,
		ObjectRecordSize: format.IndexObjectRecordLen, PrereqRecordSize: format.IndexPrereqRecordLen,
		HashAlgo: format.HashAlgoSHA256, DigestLen: 32,
		Files: idxFiles, Objects: objRows, Prereqs: prereqs,
	}
	indexBuf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(indexBuf); err != nil {
		return nil, err
	}
	if len(indexBuf) != indexLen {
		return nil, fmt.Errorf("internal error: index length mismatch, predicted %d, actual %d", indexLen, len(indexBuf))
	}
	rows[indexRowIdx].data = indexBuf
	indexHash := sha256.Sum256(indexBuf)

	runBuf, err := buildRun(opts.asBuildOptions(), packTime, indexBuf, indexHash, plan.streamBytesTotal, uint64(objectCount), runSeq, discSeq, opts.FECEnabled)
	if err != nil {
		return nil, err
	}
	rows[runRowIdx].data = runBuf
	rows[run2RowIdx].data = runBuf
	plan.rows = rows

	if err := writeRunTree(opts.OutputDir, runSeq, plan, runBuf, opts.Progress); err != nil {
		// A staged object that does not match its own content is caught
		// while its bytes are copied, so the output tree is already part
		// written when this fails. Take the part-written tree away again:
		// nothing below has run yet, so no object is recorded Packed, no
		// ledger is saved, and this run and disc sequence number stay
		// free for the next pack.
		if rmErr := os.RemoveAll(filepath.Join(opts.OutputDir, "NOAHSARK")); rmErr != nil {
			return nil, fmt.Errorf("%w; the part-written run could not be removed either: %v", err, rmErr)
		}
		return nil, err
	}

	// Record every packed object as Packed, and this disc's row into the
	// local ledger, only once the run's files are all on local disk.
	for _, h := range hashed {
		if err := opts.StageLog.MarkPacked(h.ID, runSeq, opts.DiscUUID); err != nil {
			return nil, err
		}
	}
	newRow := newDiscsRow(opts.asBuildOptions(), packTime, runSeq, discSeq)
	newRow.RunHash = sha256.Sum256(runBuf[:format.RunLen])
	// The on-disc DISCS row this run carries for itself still reads
	// zero, matching run_hash: the run's own final size is not known
	// until the run is written. The local ledger row, and so every
	// later run's copy of DISCS, carries the real value from here on.
	newRow.UsedSectors = blockCount(plan.streamBytesTotal)
	ledger.Rows = append(ledger.Rows, newRow)
	if err := SaveDiscsLedger(opts.StagingDir, opts.RepoUUID, ledger.Rows); err != nil {
		return nil, err
	}
	if err := SaveRefsLedger(opts.StagingDir, opts.RepoUUID, mergedRefRecords); err != nil {
		return nil, err
	}

	remainingObjects, remainingBytes := 0, uint64(0)
	selectedSet := make(map[object.ID]bool, len(selected))
	for _, u := range selected {
		selectedSet[u.ID] = true
	}
	for _, u := range candidates {
		if selectedSet[u.ID] {
			continue
		}
		remainingObjects++
		n, err := objectByteLen(opts.StagingDir, u)
		if err != nil {
			return nil, err
		}
		remainingBytes += n
	}

	var objectBytes uint64
	for _, u := range selected {
		objectBytes += u.ByteLen
	}

	return &PackResult{
		ObjectBytes: objectBytes,
		RunSeq:      runSeq, DiscSeq: discSeq, ObjectCount: objectCount,
		FileCount: len(rows), StreamBlocks: blockCount(plan.streamBytesTotal), StripeCount: plan.stripeCount,
		RemainingObjects: remainingObjects,
		RemainingBytes:   remainingBytes,
	}, nil
}

// DryRunDisc is one disc DryRun predicts: the disc number and label it
// will carry, and the object count and byte length pack will place on
// it.
type DryRunDisc struct {
	DiscSeq     uint64
	Label       string
	ObjectCount int
	ObjectBytes uint64
}

// DryRun predicts the discs Pack would write, at opts's capacity, to
// place every object still STAGED, without writing anything: no output
// tree, no state record, no cache entry, no ledger row, and no sequence
// number is consumed. It calls the same selectRun that Pack uses, once
// for each disc it predicts, against a shrinking in-memory candidate
// list, so the prediction never depends on a second packing rule.
//
// Each predicted disc gets the number the ledger will hand it and the
// label labelFor builds for that number, and the DISCS table grows by
// one row for each predicted disc, exactly as a real pack grows it. The
// predicted fixed per-disc overhead is therefore the overhead the real
// pack of that disc will have.
//
// A capacity that holds no further object stops the prediction. DryRun
// then returns the discs it predicted so far together with the error,
// because a loop of real packs writes exactly those discs and then
// refuses in the same way.
func DryRun(opts PackOptions, labelFor func(discSeq uint64) string) ([]DryRunDisc, error) {
	if opts.StagingDir == "" {
		return nil, fmt.Errorf("staging directory is required")
	}
	if opts.TargetCapacitySectors == 0 {
		return nil, fmt.Errorf("target capacity is required and must not be zero")
	}
	if opts.PhysicalCapacitySectors != 0 && opts.TargetCapacitySectors > opts.PhysicalCapacitySectors {
		return nil, &ErrCapacityExceedsPhysical{TargetSectors: opts.TargetCapacitySectors, PhysicalSectors: opts.PhysicalCapacitySectors}
	}
	if opts.StageLog == nil {
		return nil, fmt.Errorf("a staging state log is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	packTime := now()

	allSnapshotIDs, err := listSnapshots(opts.StagingDir)
	if err != nil {
		return nil, err
	}
	if len(allSnapshotIDs) == 0 && len(opts.Snapshots) == 0 {
		return nil, fmt.Errorf("no snapshot has been committed")
	}

	onDisc := func(id object.ID) bool {
		rec, ok := opts.StageLog.Get(id)
		return ok && rec.State.OnDisc()
	}
	order, snapshotBytes, err := buildPackOrder(opts.StagingDir, allSnapshotIDs, onDisc)
	if err != nil {
		return nil, err
	}

	var candidates []packUnit
	for _, u := range order {
		if rec, ok := opts.StageLog.Get(u.ID); ok && rec.State.OnDisc() {
			continue
		}
		// An object with no state log record at all is treated as
		// staged here too, matching Pack's own defensive rule, but
		// DryRun never writes the record: a read-only prediction must
		// not change repository state.
		candidates = append(candidates, u)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	ledger, err := LoadDiscsLedger(opts.StagingDir, opts.RepoUUID)
	if err != nil {
		return nil, err
	}
	refsLedger, err := LoadRefsLedger(opts.StagingDir, opts.RepoUUID)
	if err != nil {
		return nil, err
	}

	snapObjOrder := append([]object.ID(nil), allSnapshotIDs...)
	sort.Slice(snapObjOrder, func(i, j int) bool { return lessBytes(snapObjOrder[i][:], snapObjOrder[j][:]) })
	var snapobjBlocks uint64
	for _, id := range snapObjOrder {
		snapobjBlocks += blockCount(uint64(len(snapshotBytes[id])))
	}
	fixedFileCount := 8 + len(snapObjOrder)

	rows := append([]format.DiscsRow(nil), ledger.Rows...)
	var discs []DryRunDisc
	for len(candidates) > 0 {
		runSeq, discSeq := NextSeqNumbers(rows)
		discOpts := opts
		if labelFor != nil {
			discOpts.Label = labelFor(discSeq)
		}

		discBuf, _, err := buildDisc(discOpts.asBuildOptions(), packTime, discSeq)
		if err != nil {
			return discs, err
		}
		newRefRecords := refRecordsFromSnapshots(discOpts.Snapshots, runSeq)
		refsBuf, _, err := encodeRefsTable(discOpts.RepoUUID, mergeRefRecords(refsLedger.Records, newRefRecords))
		if err != nil {
			return discs, err
		}
		discsBuf, _, err := buildDiscs(discOpts.asBuildOptions(), packTime, runSeq, discSeq, rows)
		if err != nil {
			return discs, err
		}
		var label [64]byte
		labelLen := copy(label[:], discOpts.Label)
		readmeBuf := buildReadme(discOpts.asBuildOptions(), packTime, label[:labelLen], discSeq)

		fixedBlocksExclIndex := blockCount(uint64(len(discBuf))) +
			blockCount(uint64(len(readmeBuf))) +
			blockCount(uint64(len(FormatTxt))) +
			blockCount(uint64(len(DecoderPy))) +
			blockCount(uint64(len(refsBuf))) +
			blockCount(uint64(len(discsBuf))) +
			snapobjBlocks

		selected, _, err := selectRun(discOpts, candidates, fixedBlocksExclIndex, fixedFileCount)
		if err != nil {
			return discs, err
		}
		if len(selected) == 0 {
			needed, needErr := minimumSectorsToPlaceOne(discOpts, candidates, fixedBlocksExclIndex, fixedFileCount)
			if needErr != nil {
				return discs, needErr
			}
			smallest, err := smallestCandidate(discOpts.StagingDir, candidates)
			if err != nil {
				return discs, err
			}
			return discs, &ErrCapacityTooSmall{
				TargetSectors: discOpts.TargetCapacitySectors, NeededSectors: needed,
				SmallestID: smallest.ID, SmallestKind: smallest.Kind, SmallestBytes: smallest.ByteLen,
			}
		}

		selectedSet := make(map[object.ID]bool, len(selected))
		var objectBytes uint64
		for _, u := range selected {
			selectedSet[u.ID] = true
			objectBytes += u.ByteLen
		}
		discs = append(discs, DryRunDisc{
			DiscSeq: discSeq, Label: discOpts.Label,
			ObjectCount: len(selected), ObjectBytes: objectBytes,
		})

		remaining := candidates[:0:0]
		for _, u := range candidates {
			if !selectedSet[u.ID] {
				remaining = append(remaining, u)
			}
		}
		candidates = remaining
		rows = append(rows, newDiscsRow(discOpts.asBuildOptions(), packTime, runSeq, discSeq))
	}
	return discs, nil
}

// asBuildOptions adapts PackOptions to the fields buildDisc, buildRun,
// buildRefs, buildDiscs and buildReadme read from BuildOptions.
func (opts PackOptions) asBuildOptions() BuildOptions {
	return BuildOptions{
		StagingDir: opts.StagingDir, Snapshots: opts.Snapshots,
		TargetCapacitySectors: opts.TargetCapacitySectors, PhysicalCapacitySectors: opts.PhysicalCapacitySectors,
		OutputDir: opts.OutputDir, RepoUUID: opts.RepoUUID, DiscUUID: opts.DiscUUID,
		Label: opts.Label, MediaType: opts.MediaType,
	}
}

// selectRunMaxIterations bounds the fixed-point search selectRun runs
// over the run's own file count: the filesystem overhead estimate
// shrinks the data budget as more objects are selected, and a smaller
// budget can select fewer objects in turn. Each round only moves the
// object count by a handful, so a handful of rounds always settles;
// the bound is only a backstop against an unbroken back-and-forth.
const selectRunMaxIterations = 20

// selectRun walks candidates in dependency order and greedily takes the
// longest prefix whose stream blocks (the run's fixed files, the INDEX,
// and every selected object, each padded to a whole fec.BlockSize
// block) stay within the run's data budget: the whole FEC stripes that
// fit opts.TargetCapacitySectors once the filesystem overhead of the
// run's own file count is set aside, matching OPERATIONS.md's capacity
// budget rules. Because the file count that sets the overhead is itself
// the count of objects selected, selectRun iterates to a fixed point.
// It returns the selected units and the set of external ids they
// reference that this run does not store.
func selectRun(opts PackOptions, candidates []packUnit, fixedBlocksExclIndex uint64, fixedFileCount int) ([]packUnit, map[object.ID]bool, error) {
	// sizeKnown and blocks cache each candidate's staged byte length and
	// block count the first time a round reaches it, so a later round
	// never re-stats a candidate and a candidate past every round's
	// stopping point is never stat'd at all.
	sizeKnown := make([]bool, len(candidates))
	blocks := make([]uint64, len(candidates))

	stripeWidth := fec.K + fec.M + 1
	extraRows := extraFixedRowCount(opts.FECEnabled)

	var selected []packUnit
	prereqSet := make(map[object.ID]bool)
	objectCount := 0
	for range selectRunMaxIterations {
		fileCount := fixedFileCount + objectCount + extraRows
		var dataBudget uint64
		if opts.FECEnabled {
			dataBudget = DataBudgetBlocks(opts.TargetCapacitySectors, fileCount, fec.K, stripeWidth)
		} else {
			dataBudget = DataBudgetBlocksNoFEC(opts.TargetCapacitySectors, fileCount)
		}

		var round []packUnit
		selectedSet := make(map[object.ID]bool)
		roundPrereqs := make(map[object.ID]bool)
		var selectedBlocks uint64

		for i, cand := range candidates {
			if !sizeKnown[i] {
				size, err := objectByteLen(opts.StagingDir, cand)
				if err != nil {
					return nil, nil, err
				}
				candidates[i].ByteLen = size
				blocks[i] = blockCount(size)
				sizeKnown[i] = true
			}
			cand.ByteLen = candidates[i].ByteLen
			candBlocks := blocks[i]

			var newPrereqs []object.ID
			for _, c := range cand.Children {
				if !selectedSet[c] && !roundPrereqs[c] {
					newPrereqs = append(newPrereqs, c)
				}
			}

			trialObjectCount := len(round) + 1
			trialPrereqCount := len(roundPrereqs) + len(newPrereqs)
			trialFileCount := fixedFileCount + trialObjectCount + extraRows
			trialIndexLen := format.IndexHeaderLen + trialFileCount*format.IndexFileRecordLen +
				trialObjectCount*format.IndexObjectRecordLen + trialPrereqCount*format.IndexPrereqRecordLen
			trialIndexBlocks := blockCount(uint64(trialIndexLen))
			trialTotalBlocks := fixedBlocksExclIndex + trialIndexBlocks + selectedBlocks + candBlocks

			if trialTotalBlocks > dataBudget {
				break
			}

			round = append(round, cand)
			selectedSet[cand.ID] = true
			for _, p := range newPrereqs {
				roundPrereqs[p] = true
			}
			selectedBlocks += candBlocks
		}

		selected = round
		prereqSet = roundPrereqs
		if len(round) == objectCount {
			break
		}
		objectCount = len(round)
	}
	return selected, prereqSet, nil
}

// minimumSectorsToPlaceOne finds the smallest target capacity at which
// selectRun, run over these same candidates and fixed sizes, places at
// least one object. It calls selectRun itself at each trial capacity,
// rather than re-deriving its fixed-point budget algebraically, since
// selectRun's own iteration can dip back to nothing at a capacity right
// at the edge before settling; only selectRun's own answer is
// authoritative. A binary search assumes that answer turns and stays
// positive once capacity grows enough, which holds once the trial
// capacity clears the edge region.
func minimumSectorsToPlaceOne(opts PackOptions, candidates []packUnit, fixedBlocksExclIndex uint64, fixedFileCount int) (uint64, error) {
	placesOne := func(targetSectors uint64) (bool, error) {
		trial := opts
		trial.TargetCapacitySectors = targetSectors
		selected, _, err := selectRun(trial, candidates, fixedBlocksExclIndex, fixedFileCount)
		if err != nil {
			return false, err
		}
		return len(selected) > 0, nil
	}

	lo, hi := uint64(1), uint64(1)
	for {
		ok, err := placesOne(hi)
		if err != nil {
			return 0, err
		}
		if ok {
			break
		}
		hi *= 2
	}
	for lo < hi {
		mid := lo + (hi-lo)/2
		ok, err := placesOne(mid)
		if err != nil {
			return 0, err
		}
		if ok {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return hi, nil
}

// smallestCandidate returns the candidate with the fewest bytes, the
// object a refused capacity must first grow to hold. A tie goes to the
// first in dependency order, so the answer never depends on map order.
func smallestCandidate(stagingDir string, candidates []packUnit) (packUnit, error) {
	var best packUnit
	for i, u := range candidates {
		n, err := objectByteLen(stagingDir, u)
		if err != nil {
			return packUnit{}, err
		}
		u.ByteLen = n
		if i == 0 || n < best.ByteLen {
			best = u
		}
	}
	return best, nil
}

// objectByteLen returns the encoded byte length of unit's own staged
// file: the cached bytes for a tree, blob or snapshot, or a stat of the
// chunk file otherwise.
func objectByteLen(stagingDir string, u packUnit) (uint64, error) {
	if u.Bytes != nil {
		return uint64(len(u.Bytes)), nil
	}
	fi, err := os.Stat(stagedObjectPath(filepath.Join(stagingDir, "objects"), u.ID))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", u.ID.TextForm(), err)
	}
	return uint64(fi.Size()), nil
}

// StagedTotals sums the repository-wide STAGED objects stageLog knows:
// how many, and their total on-disk byte length. It is the same count
// the next pack would still have left to place.
func StagedTotals(stagingDir string, stageLog *stage.Log) (objects int, bytes uint64, err error) {
	objectsRoot := filepath.Join(stagingDir, "objects")
	snapshotsRoot := filepath.Join(stagingDir, "snapshots")
	for _, id := range stageLog.IDsInState(stage.Staged) {
		fi, statErr := os.Stat(stagedObjectPath(objectsRoot, id))
		if os.IsNotExist(statErr) {
			fi, statErr = os.Stat(filepath.Join(snapshotsRoot, id.TextForm()))
		}
		if statErr != nil {
			return 0, 0, fmt.Errorf("%s: %w", id.TextForm(), statErr)
		}
		objects++
		bytes += uint64(fi.Size())
	}
	return objects, bytes, nil
}

// listSnapshots returns every snapshot id in stagingDir/snapshots,
// sorted ascending by id bytes.
func listSnapshots(stagingDir string) ([]object.ID, error) {
	entries, err := os.ReadDir(filepath.Join(stagingDir, "snapshots"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("staging snapshots directory: %w", err)
	}
	ids := make([]object.ID, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, err := object.ParseID(e.Name())
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return lessBytes(ids[i][:], ids[j][:]) })
	return ids, nil
}

// buildPackOrder walks every snapshot in the repository, children before
// parent (post-order), so that a straight prefix of the result always
// has every selected metadata object's staged-but-not-yet-packed
// children ahead of it. It returns that order and every snapshot's own
// bytes, keyed by id.
//
// A tree or blob onDisc already reports OnDisc is never read: rebuild-
// cache records an object OnDisc without restoring its staging file, and
// a fresh commit that dedups against an on-disc object never restages
// it either, so the walk must not need that file to exist. Such an
// object's own children are on the same disc that already holds it, so
// the walk stops there instead of descending; its id still reaches its
// parent's Children list for prereq detection.
func buildPackOrder(stagingDir string, snapshotIDs []object.ID, onDisc func(object.ID) bool) ([]packUnit, map[object.ID][]byte, error) {
	objectsRoot := filepath.Join(stagingDir, "objects")
	snapshotsRoot := filepath.Join(stagingDir, "snapshots")

	seen := make(map[object.ID]bool)
	var order []packUnit
	snapshotBytes := make(map[object.ID][]byte, len(snapshotIDs))

	var visitTree func(id object.ID) error
	visitTree = func(id object.ID) error {
		if seen[id] {
			return nil
		}
		if onDisc(id) {
			seen[id] = true
			return nil
		}
		data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
		if err != nil {
			return fmt.Errorf("tree %s: %w", id.TextForm(), err)
		}
		var tree format.Tree
		if _, err := tree.Decode(data); err != nil {
			return stagedDamaged(id, format.ObjectKindTree)
		}
		if err := verifyObjectID(id, format.ObjectKindTree, data); err != nil {
			return err
		}
		var children []object.ID
		for _, entry := range tree.Entries {
			switch entry.EntryType {
			case format.EntryTypeDirectory:
				children = append(children, object.ID(entry.ContentID))
				if err := visitTree(object.ID(entry.ContentID)); err != nil {
					return err
				}
			case format.EntryTypeRegular:
				children = append(children, object.ID(entry.ContentID))
				if err := visitBlob(objectsRoot, object.ID(entry.ContentID), seen, &order, onDisc); err != nil {
					return err
				}
			}
		}
		seen[id] = true
		order = append(order, packUnit{ID: id, Kind: format.ObjectKindTree, Children: children, Bytes: data})
		return nil
	}

	for _, snapID := range snapshotIDs {
		data, err := readObjectFile(filepath.Join(snapshotsRoot, snapID.TextForm()))
		if err != nil {
			return nil, nil, fmt.Errorf("snapshot %s: %w", snapID.TextForm(), err)
		}
		snapshotBytes[snapID] = data
		var snap format.Snapshot
		if _, err := snap.Decode(data); err != nil {
			return nil, nil, stagedDamaged(snapID, format.ObjectKindSnapshot)
		}
		if err := verifyObjectID(snapID, format.ObjectKindSnapshot, data); err != nil {
			return nil, nil, err
		}
		if err := visitTree(object.ID(snap.RootTree)); err != nil {
			return nil, nil, err
		}
		if !seen[snapID] {
			seen[snapID] = true
			order = append(order, packUnit{ID: snapID, Kind: format.ObjectKindSnapshot, Children: []object.ID{object.ID(snap.RootTree)}, Bytes: data})
		}
	}
	return order, snapshotBytes, nil
}

// visitBlob adds id's blob object and every chunk it lists, children
// (the chunks) before the blob itself. A blob onDisc already reports
// OnDisc is never read, the same way visitTree treats one.
//
// A chunk's own staged file is never read here. A chunk carries no
// child, so the walk needs nothing out of it, and reading it here would
// read every chunk a second time. Its content id is checked instead
// while the run copies it, the one pass that must read it anyway.
func visitBlob(objectsRoot string, id object.ID, seen map[object.ID]bool, order *[]packUnit, onDisc func(object.ID) bool) error {
	if seen[id] {
		return nil
	}
	if onDisc(id) {
		seen[id] = true
		return nil
	}
	data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
	if err != nil {
		return fmt.Errorf("blob %s: %w", id.TextForm(), err)
	}
	var blob format.Blob
	if _, err := blob.Decode(data); err != nil {
		return stagedDamaged(id, format.ObjectKindBlob)
	}
	if err := verifyObjectID(id, format.ObjectKindBlob, data); err != nil {
		return err
	}
	children := make([]object.ID, 0, len(blob.Entries))
	for _, e := range blob.Entries {
		chunkID := object.ID(e.ContentID)
		children = append(children, chunkID)
		if !seen[chunkID] {
			seen[chunkID] = true
			*order = append(*order, packUnit{ID: chunkID, Kind: format.ObjectKindChunk})
		}
	}
	seen[id] = true
	*order = append(*order, packUnit{ID: id, Kind: format.ObjectKindBlob, Children: children, Bytes: data})
	return nil
}

// NextSeqNumbers derives the run_seq and disc_seq a new run must get
// from the highest numbers the ledger already holds, not from its row
// count. A ledger rebuilt from surviving discs after the repository was
// lost can hold fewer rows than the newest sequence number seen, since
// recover may not have been fed every disc a lost repository once
// knew; counting rows would then hand out a number already in use. An
// empty ledger gives run_seq 1 and disc_seq 0, matching a fresh repository.
// recover calls this too, to report the numbers the next pack
// will use.
func NextSeqNumbers(rows []format.DiscsRow) (runSeq, discSeq uint64) {
	if len(rows) == 0 {
		return 1, 0
	}
	var maxRunSeq, maxDiscSeq uint64
	haveMaxRunSeq := false
	for _, row := range rows {
		if !haveMaxRunSeq || row.RunSeq > maxRunSeq {
			maxRunSeq = row.RunSeq
			haveMaxRunSeq = true
		}
		if row.DiscSeq > maxDiscSeq {
			maxDiscSeq = row.DiscSeq
		}
	}
	return maxRunSeq + 1, maxDiscSeq + 1
}

// LoadDiscsLedger reads the local disc ledger, or returns an empty one
// for a repository with no disc packed yet. recover also calls
// this to inspect the ledger it is about to replace.
func LoadDiscsLedger(stagingDir string, repoUUID [16]byte) (format.DiscsTable, error) {
	data, err := os.ReadFile(filepath.Join(stagingDir, discsLedgerName))
	if err != nil {
		if os.IsNotExist(err) {
			return format.DiscsTable{RepoUUID: repoUUID}, nil
		}
		return format.DiscsTable{}, fmt.Errorf("%s: %w", discsLedgerName, err)
	}
	var t format.DiscsTable
	if _, err := t.Decode(data); err != nil {
		return format.DiscsTable{}, fmt.Errorf("%s: %w", discsLedgerName, err)
	}
	return t, nil
}

// SaveDiscsLedger writes the local disc ledger: the rows a later Pack
// call reads back as prior rows for its own DISCS table. recover
// also calls this to restore the ledger from discs.
func SaveDiscsLedger(stagingDir string, repoUUID [16]byte, rows []format.DiscsRow) error {
	t := format.DiscsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs,
			VersionMajor: 1, VersionMinor: 0, HeaderLen: format.DiscsHeaderLen,
		},
		RepoUUID: repoUUID, RecordCount: uint64(len(rows)), RecordSize: format.DiscsRowLen,
		HashAlgo: format.HashAlgoSHA256, DigestLen: 32, Rows: rows,
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stagingDir, discsLedgerName), buf, 0o644)
}

// LoadRefsLedger reads the local refs ledger, or returns an empty one
// for a repository with no ref packed yet.
func LoadRefsLedger(stagingDir string, repoUUID [16]byte) (format.RefsTable, error) {
	data, err := os.ReadFile(filepath.Join(stagingDir, refsLedgerName))
	if err != nil {
		if os.IsNotExist(err) {
			return format.RefsTable{RepoUUID: repoUUID}, nil
		}
		return format.RefsTable{}, fmt.Errorf("%s: %w", refsLedgerName, err)
	}
	var t format.RefsTable
	if _, err := t.Decode(data); err != nil {
		return format.RefsTable{}, fmt.Errorf("%s: %w", refsLedgerName, err)
	}
	return t, nil
}

// SaveRefsLedger writes the local refs ledger: the records a later Pack
// call reads back as the refs earlier runs already carry, so it can
// carry them into its own refs.bin unchanged. recover also calls
// this to restore the ledger from discs.
func SaveRefsLedger(stagingDir string, repoUUID [16]byte, recs []format.RefRecord) error {
	buf, _, err := encodeRefsTable(repoUUID, append([]format.RefRecord(nil), recs...))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stagingDir, refsLedgerName), buf, 0o644)
}

// mergeRefRecords unions carried and fresh REFS records by name: a name
// in both keeps only the fresh record, since a ref packed again on this
// run points at a newer snapshot and this run's run_seq. A name found
// only in carried keeps its own run_seq from the run that packed it.
// The result is unsorted; encodeRefsTable orders it before writing.
func mergeRefRecords(carried, fresh []format.RefRecord) []format.RefRecord {
	byName := make(map[string]format.RefRecord, len(carried)+len(fresh))
	for _, r := range carried {
		byName[string(r.Name[:r.NameLen])] = r
	}
	for _, r := range fresh {
		byName[string(r.Name[:r.NameLen])] = r
	}
	merged := make([]format.RefRecord, 0, len(byName))
	for _, r := range byName {
		merged = append(merged, r)
	}
	return merged
}

// blockCount returns the number of fec.BlockSize blocks that hold n
// bytes, zero padded to a block boundary; the same rule
// fec.NewStreamLayout applies per stream file.
func blockCount(n uint64) uint64 {
	return (n + fec.BlockSize - 1) / fec.BlockSize
}
