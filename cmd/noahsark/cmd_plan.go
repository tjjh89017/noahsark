package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// cmdPlan implements "noahsark plan". OPERATIONS.md's "14. Restore"
// resolves a restore plan from the repository's catalog and cache;
// this build reads the local cache alone, never a disc, matching
// "Computes the restore plan ... reads nothing from a disc beyond the
// catalog." SNAPSHOT accepts a snapshot id or a ref name, resolved the
// same way ls and log resolve it.
//
// The plan groups every object the restore needs by the disc that
// holds it, ordered by the tie-breaks of "14.1 The planner": most
// bytes first, then the newer disc, then the lower disc_seq. An
// object's size counts only when the run that actually stores it is
// itself cached (cache.ObjectLocation.SizeKnown); an object known only
// through another cached run's Prereqs table still counts toward that
// disc's object count, at zero bytes.
func cmdPlan(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark plan [--include=PATH]... [--out=FILE] SNAPSHOT",
		"Compute a restore plan from the local cache: which discs a restore of SNAPSHOT would need, and what each holds.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	var includeFlags stringList
	fs.Var(&includeFlags, "include", "plan only this snapshot-relative path and, if it names a directory, everything under it; repeatable")
	outFile := fs.String("out", "", "write the plan as JSON to this file")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("plan", fs, stderr) {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark plan [--include=PATH]... [--out=FILE] SNAPSHOT")
		return 2
	}

	src, c, err := openCacheSource(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}

	snapID, err := src.ParseSnapshotArg(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 2
	}

	if err := c.CheckComplete(snapID); err != nil {
		if ie, ok := err.(*cache.IncompleteError); ok {
			_, _ = fmt.Fprintln(stderr, formatIncompleteError("plan", ie))
			return 3
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}

	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		return reportSourceError("plan", stderr, err, c, snapID)
	}

	needed, err := collectPlanObjects(c, snap, includeFlags)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}
	needed[snapID] = format.ObjectKindSnapshot

	result := buildPlan(c, needed)

	printPlanText(stdout, result)

	if *outFile != "" {
		doc := buildPlanDocument(snapID, includeFlags, result)
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
			return 1
		}
		if err := os.WriteFile(*outFile, append(b, '\n'), 0o644); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
			return 1
		}
	}

	if len(result.missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: plan: %d object(s) have no run known to the cache; rebuild-cache from more discs\n", result.missingObjectCount())
		return 3
	}
	return 0
}

// collectPlanObjects walks the cached trees under includes (the whole
// snapshot when includes is empty), starting from snap's root tree, and
// returns every object id a restore of that scope needs: every tree
// and blob object, the chunk ids a cached blob names, and the caller
// adds the snapshot object itself. It never reads a chunk's payload.
func collectPlanObjects(c *cache.Cache, snap *format.Snapshot, includes []string) (map[object.ID]format.ObjectKind, error) {
	w := &planWalker{c: c, needed: make(map[object.ID]format.ObjectKind)}
	rootTree, err := c.ReadTree(object.ID(snap.RootTree))
	if err != nil {
		return nil, err
	}

	if len(includes) == 0 {
		for _, e := range rootTree.Entries {
			if err := w.addEntry(e); err != nil {
				return nil, err
			}
		}
		return w.needed, nil
	}

	src := &cacheSource{c: c}
	for _, inc := range includes {
		target, _, err := resolveLsPath(src, rootTree.Entries, inc)
		if err != nil {
			return nil, fmt.Errorf("--include=%s: %w", inc, err)
		}
		if err := w.addEntry(*target); err != nil {
			return nil, err
		}
	}
	return w.needed, nil
}

// planWalker collects the object ids one plan call needs, reading
// trees and blobs from the cache alone.
type planWalker struct {
	c      *cache.Cache
	needed map[object.ID]format.ObjectKind
}

// addEntry adds e's own object (a tree for a directory, a blob and its
// chunks for a regular file) and, for a directory, recurses into it.
// Every other entry type (symlink, device, fifo, socket) stores no
// object of its own and is skipped.
func (w *planWalker) addEntry(e format.TreeEntry) error {
	switch e.EntryType {
	case format.EntryTypeDirectory:
		return w.addTree(object.ID(e.ContentID))
	case format.EntryTypeRegular:
		w.addBlob(object.ID(e.ContentID))
	}
	return nil
}

// addTree adds treeID and recurses into every child a directory entry
// names. CheckComplete already proved every tree the whole snapshot
// reaches is cached, so a read failure here is a hard error, not an
// incompleteness to degrade past.
func (w *planWalker) addTree(treeID object.ID) error {
	if _, ok := w.needed[treeID]; ok {
		return nil
	}
	w.needed[treeID] = format.ObjectKindTree
	t, err := w.c.ReadTree(treeID)
	if err != nil {
		return err
	}
	for _, e := range t.Entries {
		if err := w.addEntry(e); err != nil {
			return err
		}
	}
	return nil
}

// addBlob adds blobID and, when the cache also holds that blob object,
// every chunk id it names. A blob the cache does not hold is still
// added, at the object level only: the cache holds blobs only for the
// snapshots pack or rebuild-cache have processed since blob caching was
// added, and its absence is not, by itself, an incomplete cache.
func (w *planWalker) addBlob(blobID object.ID) {
	if _, ok := w.needed[blobID]; ok {
		return
	}
	w.needed[blobID] = format.ObjectKindBlob
	b, err := w.c.ReadBlob(blobID)
	if err != nil {
		return
	}
	for _, e := range b.Entries {
		id := object.ID(e.ContentID)
		if _, ok := w.needed[id]; !ok {
			w.needed[id] = format.ObjectKindChunk
		}
	}
}

// discPlanEntry is one disc's share of a plan.
type discPlanEntry struct {
	order    int
	discSeq  uint64
	discUUID [16]byte
	label    string
	created  int64
	runs     []uint64
	objects  int
	bytes    uint64
}

// planResult is the outcome of grouping every needed object by the
// disc that holds it.
type planResult struct {
	discs            []discPlanEntry
	missing          []missingEntry
	totalObjects     int
	totalBytes       uint64
	peakStagingBytes uint64
}

// missingEntry is one group of objects a plan could not place: either a
// resolved run whose disc no cached DISCS row names, or an object no
// cached run's INDEX names at all (runSeq 0).
type missingEntry struct {
	runSeq  uint64
	objects int
}

func (r *planResult) missingObjectCount() int {
	n := 0
	for _, m := range r.missing {
		n += m.objects
	}
	return n
}

// buildPlan maps every needed object to a disc through c.LocateObject
// and c.DiscForRun, groups the result by disc, and orders the discs by
// "14.1 The planner"'s tie-breaks: most bytes first, then the newer
// disc, then the lower disc_seq.
func buildPlan(c *cache.Cache, needed map[object.ID]format.ObjectKind) *planResult {
	byDisc := make(map[[16]byte]*discPlanEntry)
	missingByRun := make(map[uint64]int)

	ids := make([]object.ID, 0, len(needed))
	for id := range needed {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].TextForm() < ids[j].TextForm() })

	r := &planResult{}
	for _, id := range ids {
		loc, found := c.LocateObject(id)
		if !found {
			missingByRun[0]++
			continue
		}
		row, found := c.DiscForRun(loc.RunSeq)
		if !found {
			missingByRun[loc.RunSeq]++
			continue
		}
		e, ok := byDisc[row.DiscUUID]
		if !ok {
			e = &discPlanEntry{discSeq: row.DiscSeq, discUUID: row.DiscUUID, label: discRowLabel(row), created: row.CreatedSec}
			byDisc[row.DiscUUID] = e
		}
		if !slices.Contains(e.runs, loc.RunSeq) {
			e.runs = append(e.runs, loc.RunSeq)
		}
		e.objects++
		e.bytes += loc.PayloadLen
		r.totalObjects++
		r.totalBytes += loc.PayloadLen
		if loc.SizeKnown && loc.PayloadLen > r.peakStagingBytes {
			r.peakStagingBytes = loc.PayloadLen
		}
	}

	discs := make([]discPlanEntry, 0, len(byDisc))
	for _, e := range byDisc {
		slices.Sort(e.runs)
		discs = append(discs, *e)
	}
	sort.Slice(discs, func(i, j int) bool {
		a, b := discs[i], discs[j]
		if a.bytes != b.bytes {
			return a.bytes > b.bytes
		}
		if a.created != b.created {
			return a.created > b.created
		}
		return a.discSeq < b.discSeq
	})
	for i := range discs {
		discs[i].order = i
	}
	r.discs = discs

	runSeqs := make([]uint64, 0, len(missingByRun))
	for seq := range missingByRun {
		runSeqs = append(runSeqs, seq)
	}
	slices.Sort(runSeqs)
	for _, seq := range runSeqs {
		r.missing = append(r.missing, missingEntry{runSeq: seq, objects: missingByRun[seq]})
	}

	return r
}

// discRowLabel trims a DISCS row's fixed-width label field.
func discRowLabel(row format.DiscsRow) string {
	n := min(int(row.LabelLen), len(row.Label))
	return string(row.Label[:n])
}

// printPlanText prints one line per disc, in plan order, then the
// plan's totals.
func printPlanText(stdout io.Writer, r *planResult) {
	for _, d := range r.discs {
		_, _ = fmt.Fprintf(stdout, "disc_seq=%d uuid=%s label=%q objects=%d bytes=%d\n",
			d.discSeq, uuidText(d.discUUID), d.label, d.objects, d.bytes)
	}
	for _, m := range r.missing {
		if m.runSeq == 0 {
			_, _ = fmt.Fprintf(stdout, "missing: %d object(s), run unknown\n", m.objects)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "missing: %d object(s) on run %d, disc unknown\n", m.objects, m.runSeq)
	}
	_, _ = fmt.Fprintf(stdout, "totals: discs=%d objects=%d bytes=%d\n", len(r.discs), r.totalObjects, r.totalBytes)
}

// planDiscJSON is one disc of the JSON plan's discs array, the subset
// of OPERATIONS.md "14.4 The plan file"'s discs[] fields this build
// knows: order, disc_uuid, disc_seq, label, runs, objects_to_read and
// bytes_to_read.
type planDiscJSON struct {
	Order         int      `json:"order"`
	DiscUUID      string   `json:"disc_uuid"`
	DiscSeq       uint64   `json:"disc_seq"`
	Label         string   `json:"label"`
	Runs          []uint64 `json:"runs"`
	ObjectsToRead int      `json:"objects_to_read"`
	BytesToRead   uint64   `json:"bytes_to_read"`
}

// planMissingJSON is one missing_discs entry: run_seq when the object's
// run is known but no cached DISCS row names its disc, 0 when no
// cached run's INDEX names the object's run at all.
type planMissingJSON struct {
	RunSeq  uint64 `json:"run_seq"`
	Objects int    `json:"objects"`
}

// planDocument is the JSON plan --out writes: OPERATIONS.md "14.4 The
// plan file"'s fields, as far as a cache-only, filter-less build knows
// them.
type planDocument struct {
	Format           string            `json:"format"`
	Version          int               `json:"version"`
	Snapshot         string            `json:"snapshot"`
	Include          []string          `json:"include,omitempty"`
	Objects          int               `json:"objects"`
	Bytes            uint64            `json:"bytes"`
	PeakStagingBytes uint64            `json:"peak_staging_bytes"`
	Switches         int               `json:"switches"`
	Passes           int               `json:"passes"`
	Discs            []planDiscJSON    `json:"discs"`
	MissingDiscs     []planMissingJSON `json:"missing_discs"`
}

func buildPlanDocument(snapID object.ID, includes []string, r *planResult) planDocument {
	discs := make([]planDiscJSON, len(r.discs))
	for i, d := range r.discs {
		discs[i] = planDiscJSON{
			Order:         d.order,
			DiscUUID:      uuidText(d.discUUID),
			DiscSeq:       d.discSeq,
			Label:         d.label,
			Runs:          d.runs,
			ObjectsToRead: d.objects,
			BytesToRead:   d.bytes,
		}
	}
	missing := make([]planMissingJSON, len(r.missing))
	for i, m := range r.missing {
		missing[i] = planMissingJSON{RunSeq: m.runSeq, Objects: m.objects}
	}
	return planDocument{
		Format:           "noahsark-restore-plan",
		Version:          1,
		Snapshot:         snapID.TextForm(),
		Include:          includes,
		Objects:          r.totalObjects,
		Bytes:            r.totalBytes,
		PeakStagingBytes: r.peakStagingBytes,
		Switches:         len(r.discs),
		Passes:           1,
		Discs:            discs,
		MissingDiscs:     missing,
	}
}
