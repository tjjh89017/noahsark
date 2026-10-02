package image

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// ErrNothingToPack marks a pack that has nothing left to write: no
// commit yet, or every staged object already on a disc. It is not a
// failure, so pack prints the reason and exits with success.
var ErrNothingToPack = errors.New("nothing to pack")

// Store names the repository files that Pack and DryRun read and
// write. The caller gives every path: the image package has no layout
// of its own.
type Store struct {
	// ObjectPath gives the file path of an object from its kind and id.
	ObjectPath ObjectPathFunc
	// SnapshotIDs lists every snapshot of the repository. Every run
	// carries each snapshot that an earlier disc holds, and packs each
	// snapshot that is still staged.
	SnapshotIDs SnapshotIDsFunc
	// DiscsLedger is the path of the disc ledger.
	DiscsLedger string
	// RefsLedger is the path of the ref ledger.
	RefsLedger string
}

// check reports the first field of s that is not set.
func (s Store) check() error {
	switch {
	case s.ObjectPath == nil:
		return fmt.Errorf("an object path function is required")
	case s.SnapshotIDs == nil:
		return fmt.Errorf("a snapshot list function is required")
	case s.DiscsLedger == "" || s.RefsLedger == "":
		return fmt.Errorf("the disc ledger and the ref ledger paths are required")
	}
	return nil
}

// PackOptions holds everything Pack needs to select the next run's
// objects from the staging store and lay it out.
type PackOptions struct {
	Store
	// Snapshots names the refs this run's REFS table carries.
	Snapshots []SnapshotRef
	// TargetCapacitySectors is the pack limit. Pack refuses to run
	// without it.
	TargetCapacitySectors uint64
	// OutputDir receives the NOAHSARK tree.
	OutputDir string
	RepoUUID  [16]byte
	DiscUUID  [16]byte
	Label     string
	// MinRunSeq and MinDiscSeq are the lowest run_seq and disc_seq that
	// this pack can use. The disc ledger alone does not know a number
	// that an undone pack used. The caller gives the numbers after the
	// highest that its other records hold, so that no number is used
	// again. Zero values add no limit.
	MinRunSeq  uint64
	MinDiscSeq uint64
	// Now returns the pack time. Defaults to time.Now.
	Now func() time.Time
	// StageLog is the repository's staging state log. Pack reads it to
	// select STAGED objects and appends a Packed record for every
	// object this run stores.
	StageLog *stage.Log
	// WriteCatalog writes the catalog tables of the disc from OutputDir.
	// Pack calls it after the disc root is synced, and before it writes
	// the disc ledger row. A nil WriteCatalog writes nothing.
	WriteCatalog func() error
	// Progress reports bytes of object content placed into the run's
	// tree. A nil Progress reports nothing.
	Progress *progress.Reporter
	// Unreadable gets each item that pack cannot take, once for each
	// snapshot that reaches it. pack takes the other items. A nil
	// Unreadable reports nothing.
	Unreadable func(UnreadableItem)
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
// FORMAT, REFS, DISCS and every snapshot object) already use
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
// chunk or snapshot, with the direct child ids of a metadata object
// (tree, blob or snapshot) that a disc holds: the Prereqs rows the unit
// can need. A child that the walk takes comes earlier in the order. The
// walk keeps no bytes of a tree or a blob, so that its memory does not
// grow with their size; pack reads them again for the objects that it
// places.
type packUnit struct {
	ID       object.ID
	Kind     format.ObjectKind
	Children []object.ID
	Bytes    []byte // set for a snapshot object only.
	ByteLen  uint64 // the size of the object file.
}

// snapshotIDs lists the snapshots through opts.SnapshotIDs, sorted by
// id bytes.
func (opts PackOptions) snapshotIDs() ([]object.ID, error) {
	ids, err := opts.SnapshotIDs()
	if err != nil {
		return nil, err
	}
	sort.Slice(ids, func(i, j int) bool { return lessBytes(ids[i][:], ids[j][:]) })
	return ids, nil
}

// Pack selects the STAGED objects for exactly one run within
// opts.TargetCapacitySectors, in the pack order of buildPackPlan (a
// snapshot's tree and blob objects with their chunks, where the budget
// allows), writes the run's NOAHSARK tree the same way Build does, and
// appends every object it packed as Packed to opts.StageLog. Every
// snapshot object in the repository is always written to this run's
// catalog, whatever its own state.
func Pack(opts PackOptions) (*PackResult, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("an output directory is required")
	}
	if opts.TargetCapacitySectors == 0 {
		return nil, fmt.Errorf("target capacity is required and must not be zero")
	}
	if opts.StageLog == nil {
		return nil, fmt.Errorf("a staging state log is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	packTime := now()

	allSnapshotIDs, err := opts.snapshotIDs()
	if err != nil {
		return nil, err
	}
	if len(allSnapshotIDs) == 0 && len(opts.Snapshots) == 0 {
		// No snapshot and no ref: this repository has never had a
		// commit.
		return nil, fmt.Errorf("%w: no snapshot has been committed", ErrNothingToPack)
	}

	// A chunk that fails its check while the run copies it goes into
	// damaged, and the run is built again without it and without the
	// items above it.
	damaged := make(map[object.ID]bool)
	reported := make(map[[2]object.ID]bool)
	for {
		plan, candidates, err := opts.plan(allSnapshotIDs, damaged, reported)
		if err != nil {
			return nil, err
		}
		for _, u := range candidates {
			if _, ok := opts.StageLog.Get(u.ID); !ok {
				// Defensive: every committed object should already carry
				// a Staged record. Treat an unrecorded object as staged
				// rather than silently dropping it from selection.
				if err := opts.StageLog.EnsureStaged(u.ID); err != nil {
					return nil, err
				}
			}
		}
		result, err := opts.packRun(plan, candidates, allSnapshotIDs, packTime)
		if dmg, ok := errors.AsType[*ErrStagedDamaged](err); ok && dmg.Kind == format.ObjectKindChunk && !damaged[dmg.ID] {
			damaged[dmg.ID] = true
			continue
		}
		return result, err
	}
}

// plan builds the pack order of the snapshots ids and returns it with
// its candidates, each sized. A chunk whose file is missing goes into
// damaged, and the order is built again without it. plan reports each
// unreadable item through opts.Unreadable once for each key of
// reported. A snapshot object that a disc holds and that the catalog
// cannot give stops the pack: each run must carry it.
func (opts PackOptions) plan(ids []object.ID, damaged map[object.ID]bool, reported map[[2]object.ID]bool) (*packPlan, []packUnit, error) {
	for {
		plan := buildPackPlan(opts.ObjectPath, ids, opts.StageLog, damaged, true, false)
		for _, g := range plan.groups {
			if g.OnDisc && g.Unreadable != nil && g.Unreadable.ID == g.ID {
				return nil, nil, fmt.Errorf("snapshot %s: %s; each disc carries it: run recover with a disc that holds it", g.ID.TextForm(), g.Unreadable.Problem())
			}
		}
		missing := false
		for i := range plan.units {
			u := &plan.units[i]
			if u.Kind != format.ObjectKindChunk {
				continue
			}
			fi, err := os.Stat(opts.ObjectPath(u.Kind, u.ID))
			if errors.Is(err, fs.ErrNotExist) {
				damaged[u.ID] = true
				missing = true
				continue
			}
			if err != nil {
				return nil, nil, fmt.Errorf("chunk %s: %w", u.ID.TextForm(), err)
			}
			u.ByteLen = uint64(fi.Size())
		}
		if missing {
			continue
		}
		report := func(item UnreadableItem) {
			key := [2]object.ID{item.Snapshot, item.ID}
			if opts.Unreadable != nil && !reported[key] {
				reported[key] = true
				opts.Unreadable(item)
			}
		}
		for _, g := range plan.groups {
			if g.Unreadable != nil {
				report(*g.Unreadable)
			}
		}
		for _, item := range plan.orphans {
			report(item)
		}
		return plan, plan.units, nil
	}
}

// packRun writes the run of candidates, the pack order of order, and
// records it. It returns an *ErrStagedDamaged of a chunk when the copy
// check of that chunk fails; nothing is recorded then.
func (opts PackOptions) packRun(order *packPlan, candidates []packUnit, allSnapshotIDs []object.ID, packTime time.Time) (*PackResult, error) {
	if len(candidates) == 0 {
		if order.hasUnreadable() {
			return nil, fmt.Errorf("%w: pack can read no staged object", ErrNothingToPack)
		}
		if len(opts.Snapshots) == 0 {
			return nil, fmt.Errorf("%w: no staged object remains", ErrNothingToPack)
		}
		return nil, fmt.Errorf("%w: every object of %s is already on a disc", ErrNothingToPack, refNamesText(opts.Snapshots))
	}
	snapshotBytes := order.snapshotBytes

	ledger, err := LoadDiscsLedger(opts.DiscsLedger, opts.RepoUUID)
	if err != nil {
		return nil, err
	}
	runSeq, discSeq := opts.nextSeqNumbers(ledger.Rows)

	discBuf, discHash, err := buildDisc(opts.asBuildOptions(), packTime, discSeq)
	if err != nil {
		return nil, err
	}
	refsLedger, err := LoadRefsLedger(opts.RefsLedger, opts.RepoUUID)
	if err != nil {
		return nil, err
	}
	newRefRecords := refRecordsFromSnapshots(opts.Snapshots)
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

	// Every snapshot object goes onto every disc. A snapshot an earlier
	// disc already carries is placed whatever the budget allows, so it
	// is reserved here instead of competing for selection; a snapshot
	// this run is the first to store is an ordinary candidate, and its
	// root tree then gets a prerequisite row like any other reference.
	carried := carriedSnapshots(allSnapshotIDs, opts.StageLog)
	var carriedSectors uint64
	for _, id := range carried {
		carriedSectors += sectorCount(uint64(len(snapshotBytes[id])))
	}

	fixedSectorsExclIndex := sectorCount(uint64(len(discBuf))) +
		sectorCount(uint64(len(readmeBuf))) +
		sectorCount(uint64(len(FormatTxt))) +
		sectorCount(uint64(len(refsBuf))) +
		sectorCount(uint64(len(discsBuf))) +
		carriedSectors
	fixedFileCount := 7 + len(carried) // INDEX,RUN,DISC,README,FORMAT,REFS,DISCS

	selected, prereqIDs, err := selectRun(opts, candidates, fixedSectorsExclIndex, fixedFileCount)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		needed, needErr := minimumSectorsToPlaceOne(opts, candidates, fixedSectorsExclIndex, fixedFileCount)
		if needErr != nil {
			return nil, needErr
		}
		smallest, err := smallestCandidate(opts.ObjectPath, candidates)
		if err != nil {
			return nil, err
		}
		return nil, &ErrCapacityTooSmall{
			TargetSectors: opts.TargetCapacitySectors, NeededSectors: needed,
			SmallestID: smallest.ID, SmallestKind: smallest.Kind, SmallestBytes: smallest.ByteLen,
		}
	}

	// The role 13 rows and the Objects rows pair by position, and both
	// are in ascending content id order.
	placed := make([]packUnit, 0, len(selected)+len(carried))
	placed = append(placed, selected...)
	for _, id := range carried {
		data := snapshotBytes[id]
		placed = append(placed, packUnit{
			ID: id, Kind: format.ObjectKindSnapshot, Bytes: data, ByteLen: uint64(len(data)),
		})
	}
	sort.Slice(placed, func(i, j int) bool { return lessBytes(placed[i].ID[:], placed[j].ID[:]) })

	prereqs := make([]format.IndexPrereqRecord, 0, len(prereqIDs))
	for id := range prereqIDs {
		rec, ok := opts.StageLog.Get(id)
		if !ok || !rec.State.OnDisc() {
			return nil, fmt.Errorf("internal error: prerequisite %s is not packed", id.TextForm())
		}
		prereqs = append(prereqs, format.IndexPrereqRecord{ContentID: id, DiscUUID: rec.DiscUUID})
	}
	sort.Slice(prereqs, func(i, j int) bool { return lessBytes(prereqs[i].ContentID[:], prereqs[j].ContentID[:]) })

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

	for _, h := range placed {
		row := fileRow{role: format.FileRoleObject, byteLen: h.ByteLen, path: objectDiscPath(h.ID, h.Kind)}
		switch {
		case h.Bytes != nil:
			row.data = h.Bytes
		case h.Kind != format.ObjectKindChunk:
			data, err := readPlacedObject(opts.ObjectPath, h)
			if err != nil {
				return nil, err
			}
			row.data = data
		default:
			row.srcPath = opts.ObjectPath(h.Kind, h.ID)
			row.srcID = h.ID
		}
		rows = append(rows, row)
	}

	objectCount := len(placed)
	fileCount := len(rows) + run2RowCount
	indexLen := format.IndexHeaderLen + fileCount*format.IndexFileRecordLen +
		objectCount*format.IndexObjectRecordLen + len(prereqs)*format.IndexPrereqRecordLen
	rows[indexRowIdx].byteLen = uint64(indexLen)

	rows, run2RowIdx := appendRun2Row(rows)

	if err := CheckCapacity(fileBytes(rows), 2*RunFileLen, len(rows), opts.TargetCapacitySectors); err != nil {
		return nil, fmt.Errorf("internal error: selected run does not fit after all: %w", err)
	}

	objRows := make([]format.IndexObjectRecord, objectCount)
	for i, h := range placed {
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
		RunSeq: runSeq, FileCount: uint32(len(rows)), ObjectCount: uint32(objectCount),
		PrereqCount: uint32(len(prereqs)),
		Files:       idxFiles, Objects: objRows, Prereqs: prereqs,
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

	runBuf, err := buildRun(opts.asBuildOptions(), packTime, indexBuf, indexHash, runSeq, discSeq)
	if err != nil {
		return nil, err
	}
	rows[runRowIdx].data = runBuf
	rows[run2RowIdx].data = runBuf

	// A staged object that does not match its own content is caught while
	// its bytes are copied, so the output tree is already part written
	// when this fails. The same holds when the catalog tables cannot be
	// written. Take the part-written tree away again: nothing below has
	// run yet, so no object is recorded Packed, no ledger is saved, and
	// this run and disc sequence number stay free for the next pack.
	removeTree := func(err error) error {
		if rmErr := os.RemoveAll(filepath.Join(opts.OutputDir, "NOAHSARK")); rmErr != nil {
			return fmt.Errorf("%w; the part-written run could not be removed either: %v", err, rmErr)
		}
		return err
	}
	if err := writeRunTree(opts.OutputDir, runSeq, rows, opts.Progress); err != nil {
		return nil, removeTree(err)
	}

	// The durable writes follow the synced disc root in this order: the
	// catalog tables, the disc ledger row, then the item records. The
	// caller appends the Packed event last. Only the objects this run is
	// the first to store change state. A carried snapshot stays bound to
	// the disc that first stored it.
	if opts.WriteCatalog != nil {
		if err := opts.WriteCatalog(); err != nil {
			return nil, removeTree(fmt.Errorf("catalog tables: %w", err))
		}
	}
	newRow := newDiscsRow(opts.asBuildOptions(), packTime, runSeq, discSeq)
	newRow.RunHash = sha256.Sum256(runBuf[:format.RunLen])
	// The on-disc DISCS row this run carries for itself still reads
	// zero, matching run_hash: the run's own final size is not known
	// until the run is written. The local ledger row, and so every
	// later run's copy of DISCS, carries the real value from here on.
	ledger.Rows = append(ledger.Rows, newRow)
	if err := SaveDiscsLedger(opts.DiscsLedger, opts.RepoUUID, ledger.Rows); err != nil {
		return nil, err
	}
	if err := SaveRefsLedger(opts.RefsLedger, opts.RepoUUID, mergedRefRecords); err != nil {
		return nil, err
	}
	selectedIDs := make([]object.ID, len(selected))
	for i, h := range selected {
		selectedIDs[i] = h.ID
	}
	if err := opts.StageLog.MarkPacked(runSeq, opts.DiscUUID, selectedIDs...); err != nil {
		return nil, err
	}

	remainingObjects, remainingBytes := 0, uint64(0)
	for _, u := range candidates[len(selected):] {
		remainingObjects++
		n, err := objectByteLen(opts.ObjectPath, u)
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
		// ObjectCount here is len(selected), not the INDEX table's own
		// objectCount: a carried snapshot is already counted on the disc
		// that first stored it, so this disc's own object count, the one
		// the operator sees, must match what disc burned and verify
		// report for the same disc, both of which count by that first
		// disc's ownership. objectBytes above already follows the same
		// rule.
		RunSeq: runSeq, DiscSeq: discSeq, ObjectCount: len(selected),
		FileCount:        len(rows),
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
// Each predicted disc gets the number that Pack will give it and the
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
	if err := opts.check(); err != nil {
		return nil, err
	}
	if opts.TargetCapacitySectors == 0 {
		return nil, fmt.Errorf("target capacity is required and must not be zero")
	}
	if opts.StageLog == nil {
		return nil, fmt.Errorf("a staging state log is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	packTime := now()

	allSnapshotIDs, err := opts.snapshotIDs()
	if err != nil {
		return nil, err
	}
	if len(allSnapshotIDs) == 0 && len(opts.Snapshots) == 0 {
		return nil, fmt.Errorf("%w: no snapshot has been committed", ErrNothingToPack)
	}

	// An object with no state log record at all is a candidate here
	// too, matching Pack's own defensive rule, but DryRun never writes
	// the record: a read-only prediction must not change repository
	// state.
	plan, candidates, err := opts.plan(allSnapshotIDs, make(map[object.ID]bool), make(map[[2]object.ID]bool))
	if err != nil {
		return nil, err
	}
	snapshotBytes := plan.snapshotBytes
	if len(candidates) == 0 {
		return nil, nil
	}

	ledger, err := LoadDiscsLedger(opts.DiscsLedger, opts.RepoUUID)
	if err != nil {
		return nil, err
	}
	refsLedger, err := LoadRefsLedger(opts.RefsLedger, opts.RepoUUID)
	if err != nil {
		return nil, err
	}

	carried := carriedSnapshots(allSnapshotIDs, opts.StageLog)
	var carriedSectors uint64
	for _, id := range carried {
		carriedSectors += sectorCount(uint64(len(snapshotBytes[id])))
	}
	fixedFileCount := 7 + len(carried)

	rows := append([]format.DiscsRow(nil), ledger.Rows...)
	var discs []DryRunDisc
	for len(candidates) > 0 {
		runSeq, discSeq := opts.nextSeqNumbers(rows)
		discOpts := opts
		if labelFor != nil {
			discOpts.Label = labelFor(discSeq)
		}

		discBuf, _, err := buildDisc(discOpts.asBuildOptions(), packTime, discSeq)
		if err != nil {
			return discs, err
		}
		newRefRecords := refRecordsFromSnapshots(discOpts.Snapshots)
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

		fixedSectorsExclIndex := sectorCount(uint64(len(discBuf))) +
			sectorCount(uint64(len(readmeBuf))) +
			sectorCount(uint64(len(FormatTxt))) +
			sectorCount(uint64(len(refsBuf))) +
			sectorCount(uint64(len(discsBuf))) +
			carriedSectors

		selected, _, err := selectRun(discOpts, candidates, fixedSectorsExclIndex, fixedFileCount)
		if err != nil {
			return discs, err
		}
		if len(selected) == 0 {
			needed, needErr := minimumSectorsToPlaceOne(discOpts, candidates, fixedSectorsExclIndex, fixedFileCount)
			if needErr != nil {
				return discs, needErr
			}
			smallest, err := smallestCandidate(discOpts.ObjectPath, candidates)
			if err != nil {
				return discs, err
			}
			return discs, &ErrCapacityTooSmall{
				TargetSectors: discOpts.TargetCapacitySectors, NeededSectors: needed,
				SmallestID: smallest.ID, SmallestKind: smallest.Kind, SmallestBytes: smallest.ByteLen,
			}
		}

		var objectBytes uint64
		for _, u := range selected {
			objectBytes += u.ByteLen
		}
		discs = append(discs, DryRunDisc{
			DiscSeq: discSeq, Label: discOpts.Label,
			ObjectCount: len(selected), ObjectBytes: objectBytes,
		})

		candidates = candidates[len(selected):]
		rows = append(rows, newDiscsRow(discOpts.asBuildOptions(), packTime, runSeq, discSeq))
	}
	return discs, nil
}

// asBuildOptions adapts PackOptions to the fields buildDisc, buildRun,
// buildRefs, buildDiscs and buildReadme read from BuildOptions.
func (opts PackOptions) asBuildOptions() BuildOptions {
	return BuildOptions{
		ObjectPath:            opts.ObjectPath,
		Snapshots:             opts.Snapshots,
		TargetCapacitySectors: opts.TargetCapacitySectors,
		OutputDir:             opts.OutputDir, RepoUUID: opts.RepoUUID, DiscUUID: opts.DiscUUID,
		Label: opts.Label,
	}
}

// selectRun walks candidates in dependency order and greedily takes the
// longest prefix whose sectors (the run's fixed files, the INDEX, and
// every selected object, each rounded up to a whole sector) stay within
// the run's data budget: the sectors of opts.TargetCapacitySectors left
// once the two run header copies and the filesystem overhead of the
// run's own file count are set aside, matching OPERATIONS.md's budget
// formula. Each trial computes the budget for the file count of the
// trial prefix itself. The budget only shrinks as the prefix grows, thus
// the first candidate that does not fit ends the prefix. It returns the
// selected prefix of candidates and the set of external ids it
// references: the children that a disc holds.
func selectRun(opts PackOptions, candidates []packUnit, fixedSectorsExclIndex uint64, fixedFileCount int) ([]packUnit, map[object.ID]bool, error) {
	n := 0
	prereqs := make(map[object.ID]bool)
	var selectedSectors uint64
	for _, cand := range candidates {
		candSectors := sectorCount(cand.ByteLen)

		var newPrereqs []object.ID
		for _, c := range cand.Children {
			if !prereqs[c] {
				newPrereqs = append(newPrereqs, c)
			}
		}

		trialObjectCount := n + 1
		trialPrereqCount := len(prereqs) + len(newPrereqs)
		trialFileCount := fixedFileCount + trialObjectCount + run2RowCount
		trialIndexLen := format.IndexHeaderLen + trialFileCount*format.IndexFileRecordLen +
			trialObjectCount*format.IndexObjectRecordLen + trialPrereqCount*format.IndexPrereqRecordLen
		trialIndexSectors := sectorCount(uint64(trialIndexLen))
		trialTotalSectors := fixedSectorsExclIndex + trialIndexSectors + selectedSectors + candSectors

		if trialTotalSectors > DataBudgetSectors(opts.TargetCapacitySectors, trialFileCount) {
			break
		}

		n++
		for _, p := range newPrereqs {
			prereqs[p] = true
		}
		selectedSectors += candSectors
	}
	return candidates[:n:n], prereqs, nil
}

// minimumSectorsToPlaceOne finds the smallest target capacity at which
// selectRun, run over these same candidates and fixed sizes, places at
// least one object. It calls selectRun itself at each trial capacity,
// so that selectRun's own answer decides. A binary search holds: a
// larger capacity never places fewer objects.
func minimumSectorsToPlaceOne(opts PackOptions, candidates []packUnit, fixedSectorsExclIndex uint64, fixedFileCount int) (uint64, error) {
	placesOne := func(targetSectors uint64) (bool, error) {
		trial := opts
		trial.TargetCapacitySectors = targetSectors
		selected, _, err := selectRun(trial, candidates, fixedSectorsExclIndex, fixedFileCount)
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
func smallestCandidate(objectPath ObjectPathFunc, candidates []packUnit) (packUnit, error) {
	var best packUnit
	for i, u := range candidates {
		n, err := objectByteLen(objectPath, u)
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

// readPlacedObject reads the tree or blob of u again for the disc root,
// into a buffer of its exact size, and checks it against its id.
func readPlacedObject(objectPath ObjectPathFunc, u packUnit) ([]byte, error) {
	path := objectPath(u.Kind, u.ID)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", kindName(u.Kind), u.ID.TextForm(), err)
	}
	defer func() { _ = f.Close() }()
	data := make([]byte, u.ByteLen)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, fmt.Errorf("%s %s changed during the pack: %w", kindName(u.Kind), u.ID.TextForm(), err)
	}
	if err := verifyObjectID(u.ID, u.Kind, data); err != nil {
		return nil, err
	}
	return data, nil
}

// objectByteLen returns the encoded byte length of unit's own staged
// file: the size that the plan gave it, or a stat of the file otherwise.
func objectByteLen(objectPath ObjectPathFunc, u packUnit) (uint64, error) {
	if u.ByteLen != 0 {
		return u.ByteLen, nil
	}
	fi, err := os.Stat(objectPath(u.Kind, u.ID))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", u.ID.TextForm(), err)
	}
	return uint64(fi.Size()), nil
}

// stagedKinds is the order in which StagedTotals looks for the file of
// a staged item. The state log does not hold the kind of an item.
var stagedKinds = []format.ObjectKind{
	format.ObjectKindChunk, format.ObjectKindBlob, format.ObjectKindTree, format.ObjectKindSnapshot,
}

// StagedTotals sums the repository-wide STAGED objects stageLog knows
// whose file exists: how many, and the total size of their files.
// objectPath gives the file of each kind; the first kind whose file
// exists gives the size. missing counts the STAGED objects that have no
// file of any kind, for example after the operator emptied the staging
// directory. The sum is the count the next pack would still have left
// to place.
func StagedTotals(objectPath ObjectPathFunc, stageLog *stage.Log) (objects int, bytes uint64, missing int, err error) {
	for _, id := range stageLog.IDsInState(stage.Staged) {
		size, found := int64(0), false
		for _, kind := range stagedKinds {
			fi, statErr := os.Stat(objectPath(kind, id))
			if statErr == nil {
				size, found = fi.Size(), true
				break
			}
			if !os.IsNotExist(statErr) {
				return 0, 0, 0, fmt.Errorf("%s: %w", id.TextForm(), statErr)
			}
		}
		if !found {
			missing++
			continue
		}
		objects++
		bytes += uint64(size)
	}
	return objects, bytes, missing, nil
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

// nextSeqNumbers returns the run_seq and disc_seq of the next run: the
// numbers after the ledger rows, and not below MinRunSeq and MinDiscSeq.
func (opts PackOptions) nextSeqNumbers(rows []format.DiscsRow) (runSeq, discSeq uint64) {
	runSeq, discSeq = NextSeqNumbers(rows)
	return max(runSeq, opts.MinRunSeq), max(discSeq, opts.MinDiscSeq)
}

// carriedSnapshots returns the snapshot ids an earlier disc already
// carries, in ascending content id order. Every snapshot object goes
// onto every disc, so this run places these again whatever its budget
// allows; a snapshot no disc carries yet is an ordinary candidate.
func carriedSnapshots(all []object.ID, log *stage.Log) []object.ID {
	out := make([]object.ID, 0, len(all))
	for _, id := range all {
		if rec, ok := log.Get(id); ok && rec.State.OnDisc() {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessBytes(out[i][:], out[j][:]) })
	return out
}

// mergeRefRecords unions carried and fresh REFS records. The table only
// grows from one run to the next: a record is dropped only when another
// record holds the same name, time and snapshot id. A reader takes the
// newest record of a name by time, then by snapshot id bytes.
// The result is unsorted; encodeRefsTable orders it before writing.
func mergeRefRecords(carried, fresh []format.RefRecord) []format.RefRecord {
	byKey := make(map[format.RefRecord]bool, len(carried)+len(fresh))
	merged := make([]format.RefRecord, 0, len(carried)+len(fresh))
	for _, r := range append(append([]format.RefRecord(nil), carried...), fresh...) {
		if byKey[r] {
			continue
		}
		byKey[r] = true
		merged = append(merged, r)
	}
	return merged
}

// sectorCount returns the number of whole sectors that hold n bytes.
func sectorCount(n uint64) uint64 {
	return (n + SectorSize - 1) / SectorSize
}
