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
	"github.com/tjjh89017/noahsark/internal/stage"
)

// discsLedgerName is the local repository ledger of every disc pack has
// already built. It reuses DISCS.bin's own container format directly,
// with run_hash filled in as soon as it is known, since that is the
// exact same information a real DISCS table carries for an earlier run
// and this build has no burn step to read it back from a drive. See
// docs/decisions.md, "12. Disc lifecycle, closing and appending".
const discsLedgerName = "discs.bin"

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
	// Now returns the pack time. Defaults to time.Now.
	Now func() time.Time
	// StageLog is the repository's staging state log. Pack reads it to
	// select STAGED objects and appends a Packed record for every
	// object this run stores.
	StageLog *stage.Log
}

// PackResult summarizes one Pack call: the run it built, plus what is
// left STAGED afterward.
type PackResult struct {
	Result
	RemainingObjects int
	RemainingBytes   uint64
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
// opts.TargetCapacitySectors, in Phase 1 locality order (a snapshot's
// tree and blob objects with their chunks, where the budget allows),
// writes the run's NOAHSARK tree the same way Build does, and appends
// every object it packed as Packed to opts.StageLog. Every snapshot
// object in the repository is always written to this run's catalog,
// whatever its own state.
func Pack(opts PackOptions) (*PackResult, error) {
	if opts.StagingDir == "" || opts.OutputDir == "" {
		return nil, fmt.Errorf("image: staging directory and output directory are required")
	}
	if opts.TargetCapacitySectors == 0 {
		return nil, fmt.Errorf("image: target capacity is required and must not be zero")
	}
	if opts.StageLog == nil {
		return nil, fmt.Errorf("image: a staging state log is required")
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
	if len(allSnapshotIDs) == 0 {
		return nil, fmt.Errorf("image: no snapshot has been committed")
	}

	order, snapshotBytes, err := buildPackOrder(opts.StagingDir, allSnapshotIDs)
	if err != nil {
		return nil, err
	}

	var candidates []packUnit
	for _, u := range order {
		rec, ok := opts.StageLog.Get(u.ID)
		if ok && rec.State == stage.Packed {
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

	ledger, err := loadDiscsLedger(opts.StagingDir, opts.RepoUUID)
	if err != nil {
		return nil, err
	}
	runSeq := uint64(len(ledger.Rows)) + 1
	discSeq := uint64(len(ledger.Rows))

	discBuf, discHash, err := buildDisc(opts.asBuildOptions(), packTime, discSeq)
	if err != nil {
		return nil, err
	}
	refsBuf, refsHash, err := buildRefs(opts.asBuildOptions())
	if err != nil {
		return nil, err
	}
	discsBuf, discsHash, err := buildDiscs(opts.asBuildOptions(), packTime, runSeq, discSeq, ledger.Rows)
	if err != nil {
		return nil, err
	}
	var label [64]byte
	labelLen := copy(label[:], opts.Label)
	readmeBuf := buildReadme(opts.asBuildOptions(), packTime, label[:labelLen])
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
	if len(selected) == 0 && len(candidates) > 0 {
		return nil, fmt.Errorf("image: target capacity %d sectors is too small to hold even one object", opts.TargetCapacitySectors)
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
				return nil, fmt.Errorf("image: chunk %s: %w", u.ID.TextForm(), err)
			}
		}
		hashed[i] = hashedUnit{packUnit: u, hash: h}
	}
	sort.Slice(hashed, func(i, j int) bool { return lessBytes(hashed[i].hash[:], hashed[j].hash[:]) })

	prereqs := make([]format.IndexPrereqRecord, 0, len(prereqIDs))
	for id := range prereqIDs {
		rec, ok := opts.StageLog.Get(id)
		if !ok || rec.State != stage.Packed {
			return nil, fmt.Errorf("image: internal error: prerequisite %s is not packed", id.TextForm())
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
		}
		rows = append(rows, row)
	}

	objectCount := len(hashed)
	fileCount := len(rows) + 1 + fec.M + 1
	indexLen := format.IndexHeaderLen + fileCount*format.IndexFileRecordLen +
		objectCount*format.IndexObjectRecordLen + len(prereqs)*format.IndexPrereqRecordLen
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
	for j := range fec.M {
		rows = append(rows, fileRow{
			role: format.FileRoleParity, byteLen: parityFileLen,
			path: fmt.Sprintf("NOAHSARK/runs/%%RUNSEQ%%/parity/p%04d.bin", fec.K+1+j),
		})
	}
	run2RowIdx := len(rows)
	rows = append(rows, fileRow{role: format.FileRoleRun2, byteLen: RunFileLen, path: "NOAHSARK/runs/%RUNSEQ%/RUN2.bin"})

	streamBytesTotal := streamTotal(streamSizes)
	if err := CheckCapacity(streamBytesTotal, checksumLen, uint64(fec.M)*parityFileLen, 2*RunFileLen, len(rows), opts.TargetCapacitySectors); err != nil {
		return nil, fmt.Errorf("image: internal error: selected run does not fit after all: %w", err)
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
			return nil, fmt.Errorf("image: %s: %w", h.ID.TextForm(), err)
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
		return nil, fmt.Errorf("image: internal error: index length mismatch, predicted %d, actual %d", indexLen, len(indexBuf))
	}
	rows[indexRowIdx].data = indexBuf
	indexHash := sha256.Sum256(indexBuf)

	runBuf, err := buildRun(opts.asBuildOptions(), packTime, indexBuf, indexHash, streamBytesTotal, uint64(objectCount), runSeq, discSeq)
	if err != nil {
		return nil, err
	}
	rows[runRowIdx].data = runBuf
	rows[run2RowIdx].data = runBuf

	// Write every row to its final path first: an in-memory row by
	// WriteFile, a chunk-sized row by a streaming copy from its staged
	// file. The checksum and parity rows are written by buildFECToDisk
	// below instead, one stripe at a time.
	seqDir := fmt.Sprintf("%010d", runSeq)
	finalPaths := make([]string, len(rows))
	for i, r := range rows {
		path := filepath.FromSlash(replaceRunSeq(r.path, seqDir))
		full := filepath.Join(opts.OutputDir, path)
		finalPaths[i] = full
		if i == checksumRowIdx || (i >= parityRowStart && i < parityRowStart+fec.M) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if r.srcPath != "" {
			if err := copyFileStream(r.srcPath, full, 0o644); err != nil {
				return nil, err
			}
		} else if err := os.WriteFile(full, r.data, 0o644); err != nil {
			return nil, err
		}
	}

	var sources []streamSource
	for i, r := range rows {
		if !r.inStream {
			continue
		}
		sources = append(sources, streamSource{path: finalPaths[i], size: r.byteLen})
	}
	if err := os.MkdirAll(filepath.Dir(finalPaths[checksumRowIdx]), 0o755); err != nil {
		return nil, err
	}
	parityPaths := make([]string, fec.M)
	for j := range fec.M {
		parityPaths[j] = finalPaths[parityRowStart+j]
	}
	if err := os.MkdirAll(filepath.Dir(parityPaths[0]), 0o755); err != nil {
		return nil, err
	}
	if err := buildFECToDisk(sources, layout, runBuf, finalPaths[checksumRowIdx], parityPaths); err != nil {
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
	ledger.Rows = append(ledger.Rows, newRow)
	if err := saveDiscsLedger(opts.StagingDir, opts.RepoUUID, ledger.Rows); err != nil {
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

	return &PackResult{
		RunSeq: runSeq, DiscSeq: discSeq, ObjectCount: objectCount,
		FileCount: len(rows), StreamBlocks: layout.BlockCount(), StripeCount: L,
		RemainingObjects: remainingObjects,
		RemainingBytes:   remainingBytes,
	}, nil
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

// selectRun walks candidates in dependency order and greedily takes the
// longest prefix that fits opts.TargetCapacitySectors, given the run's
// fixed (non-object, non-index) block cost and file count. It returns
// the selected units and the set of external ids they reference that
// this run does not store.
func selectRun(opts PackOptions, candidates []packUnit, fixedBlocksExclIndex uint64, fixedFileCount int) ([]packUnit, map[object.ID]bool, error) {
	var selected []packUnit
	selectedSet := make(map[object.ID]bool)
	prereqSet := make(map[object.ID]bool)
	var selectedBlocks uint64

	for _, cand := range candidates {
		size, err := objectByteLen(opts.StagingDir, cand)
		if err != nil {
			return nil, nil, err
		}
		candBlocks := blockCount(size)

		var newPrereqs []object.ID
		for _, c := range cand.Children {
			if !selectedSet[c] && !prereqSet[c] {
				newPrereqs = append(newPrereqs, c)
			}
		}

		trialObjectCount := len(selected) + 1
		trialPrereqCount := len(prereqSet) + len(newPrereqs)
		trialFileCount := fixedFileCount + trialObjectCount + 1 + fec.M + 1
		trialIndexLen := format.IndexHeaderLen + trialFileCount*format.IndexFileRecordLen +
			trialObjectCount*format.IndexObjectRecordLen + trialPrereqCount*format.IndexPrereqRecordLen
		trialIndexBlocks := blockCount(uint64(trialIndexLen))
		trialTotalBlocks := fixedBlocksExclIndex + trialIndexBlocks + selectedBlocks + candBlocks

		L := uint64(0)
		if trialTotalBlocks > 0 {
			L = (trialTotalBlocks + uint64(fec.K) - 1) / uint64(fec.K)
		}
		checksumLen := L * fec.BlockSize
		parityFileLen := (L + 1) * fec.BlockSize
		streamBytesTotal := trialTotalBlocks * fec.BlockSize

		if err := CheckCapacity(streamBytesTotal, checksumLen, uint64(fec.M)*parityFileLen, 2*RunFileLen, trialFileCount, opts.TargetCapacitySectors); err != nil {
			break
		}

		cand.ByteLen = size
		selected = append(selected, cand)
		selectedSet[cand.ID] = true
		for _, p := range newPrereqs {
			prereqSet[p] = true
		}
		selectedBlocks += candBlocks
	}
	return selected, prereqSet, nil
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
		return 0, fmt.Errorf("image: %s: %w", u.ID.TextForm(), err)
	}
	return uint64(fi.Size()), nil
}

// listSnapshots returns every snapshot id in stagingDir/snapshots,
// sorted ascending by id bytes.
func listSnapshots(stagingDir string) ([]object.ID, error) {
	entries, err := os.ReadDir(filepath.Join(stagingDir, "snapshots"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("image: staging snapshots directory: %w", err)
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
func buildPackOrder(stagingDir string, snapshotIDs []object.ID) ([]packUnit, map[object.ID][]byte, error) {
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
		data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
		if err != nil {
			return fmt.Errorf("image: tree %s: %w", id.TextForm(), err)
		}
		var tree format.Tree
		if _, err := tree.Decode(data); err != nil {
			return fmt.Errorf("image: tree %s: %w", id.TextForm(), err)
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
				if err := visitBlob(objectsRoot, object.ID(entry.ContentID), seen, &order); err != nil {
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
			return nil, nil, fmt.Errorf("image: snapshot %s: %w", snapID.TextForm(), err)
		}
		snapshotBytes[snapID] = data
		var snap format.Snapshot
		if _, err := snap.Decode(data); err != nil {
			return nil, nil, fmt.Errorf("image: snapshot %s: %w", snapID.TextForm(), err)
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
// (the chunks) before the blob itself.
func visitBlob(objectsRoot string, id object.ID, seen map[object.ID]bool, order *[]packUnit) error {
	if seen[id] {
		return nil
	}
	data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
	if err != nil {
		return fmt.Errorf("image: blob %s: %w", id.TextForm(), err)
	}
	var blob format.Blob
	if _, err := blob.Decode(data); err != nil {
		return fmt.Errorf("image: blob %s: %w", id.TextForm(), err)
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

// loadDiscsLedger reads the local disc ledger, or returns an empty one
// for a repository with no disc packed yet.
func loadDiscsLedger(stagingDir string, repoUUID [16]byte) (format.DiscsTable, error) {
	data, err := os.ReadFile(filepath.Join(stagingDir, discsLedgerName))
	if err != nil {
		if os.IsNotExist(err) {
			return format.DiscsTable{RepoUUID: repoUUID}, nil
		}
		return format.DiscsTable{}, fmt.Errorf("image: %s: %w", discsLedgerName, err)
	}
	var t format.DiscsTable
	if _, err := t.Decode(data); err != nil {
		return format.DiscsTable{}, fmt.Errorf("image: %s: %w", discsLedgerName, err)
	}
	return t, nil
}

// saveDiscsLedger writes the local disc ledger.
func saveDiscsLedger(stagingDir string, repoUUID [16]byte, rows []format.DiscsRow) error {
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

// blockCount returns the number of fec.BlockSize blocks that hold n
// bytes, zero padded to a block boundary; the same rule
// fec.NewStreamLayout applies per stream file.
func blockCount(n uint64) uint64 {
	return (n + fec.BlockSize - 1) / fec.BlockSize
}
