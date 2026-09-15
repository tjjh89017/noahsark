// Package plan computes a restore plan from the local cache alone,
// OPERATIONS.md's "14. Restore" section "14.1 The planner". It is
// shared by the "plan" command, which only prints a plan, and the
// "restore" command's disc-swap mode, which reads objects in plan
// order as each disc is inserted.
package plan

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// ObjectEntry is one object a plan assigns to a disc.
type ObjectEntry struct {
	ID    object.ID
	Kind  format.ObjectKind
	Bytes uint64
}

// DiscEntry is one disc's share of a plan, in plan order.
type DiscEntry struct {
	Order    int
	DiscSeq  uint64
	DiscUUID [16]byte
	Label    string
	Created  int64
	Runs     []uint64
	Objects  []ObjectEntry
	Bytes    uint64
}

// MissingEntry is one group of objects a plan could not place: either a
// resolved run whose disc no cached DISCS row names, or an object no
// cached run's INDEX names at all (RunSeq 0).
type MissingEntry struct {
	RunSeq  uint64
	Objects int
}

// Result is the outcome of grouping every needed object by the disc
// that holds it, in plan order.
type Result struct {
	Discs            []DiscEntry
	Missing          []MissingEntry
	TotalObjects     int
	TotalBytes       uint64
	PeakStagingBytes uint64
}

// MissingObjectCount sums every MissingEntry's object count.
func (r *Result) MissingObjectCount() int {
	n := 0
	for _, m := range r.Missing {
		n += m.Objects
	}
	return n
}

// Build resolves a restore plan for snap (already read from the cache
// under id snapID), restricted to includes (the whole snapshot when
// includes is empty). It reads only the cache, never a disc.
func Build(c *cache.Cache, snap *format.Snapshot, snapID object.ID, includes []string) (*Result, error) {
	needed, err := collectObjects(c, snap, includes)
	if err != nil {
		return nil, err
	}
	needed[snapID] = format.ObjectKindSnapshot
	return group(c, needed), nil
}

// collectObjects walks the cached trees under includes (the whole
// snapshot when includes is empty), starting from snap's root tree, and
// returns every object id a restore of that scope needs, tagged by
// kind. It never reads a chunk's payload.
func collectObjects(c *cache.Cache, snap *format.Snapshot, includes []string) (map[object.ID]format.ObjectKind, error) {
	w := &walker{c: c, needed: make(map[object.ID]format.ObjectKind)}
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

	for _, inc := range includes {
		target, _, err := resolvePath(c, rootTree.Entries, inc)
		if err != nil {
			return nil, fmt.Errorf("--include=%s: %w", inc, err)
		}
		if err := w.addEntry(*target); err != nil {
			return nil, err
		}
	}
	return w.needed, nil
}

// OverBudgetFile walks the same scope Build would, and reports the
// first regular file whose blob's whole byte size alone exceeds budget:
// no split of that one file's own chunks into passes can bring it under
// the staging budget. A budget of 0 is unlimited, so it always reports
// none. A blob or tree this walk cannot read is left for Build's own
// Missing accounting to report; it is not a budget failure here.
func OverBudgetFile(c *cache.Cache, snap *format.Snapshot, includes []string, budget uint64) (path string, bytes uint64, ok bool, err error) {
	if budget == 0 {
		return "", 0, false, nil
	}
	rootTree, err := c.ReadTree(object.ID(snap.RootTree))
	if err != nil {
		return "", 0, false, err
	}

	entries := rootTree.Entries
	if len(includes) > 0 {
		entries = entries[:0]
		for _, inc := range includes {
			target, _, err := resolvePath(c, rootTree.Entries, inc)
			if err != nil {
				return "", 0, false, fmt.Errorf("--include=%s: %w", inc, err)
			}
			entries = append(entries, *target)
		}
	}

	ow := &overBudgetWalker{c: c, budget: budget}
	for _, e := range entries {
		if path, bytes, ok := ow.walk(e, rootPathOf(e)); ok {
			return path, bytes, true, nil
		}
	}
	return "", 0, false, nil
}

// overBudgetWalker descends a snapshot's cached trees looking for the
// first regular file whose blob exceeds budget.
type overBudgetWalker struct {
	c      *cache.Cache
	budget uint64
}

// walk descends e, named path, reporting the first regular file under
// it (e included) whose blob's total size exceeds w.budget.
func (w *overBudgetWalker) walk(e format.TreeEntry, path string) (string, uint64, bool) {
	switch e.EntryType {
	case format.EntryTypeDirectory:
		t, err := w.c.ReadTree(object.ID(e.ContentID))
		if err != nil {
			return "", 0, false
		}
		for _, ce := range t.Entries {
			childPath := string(ce.Name)
			if path != "" {
				childPath = path + "/" + childPath
			}
			if p, b, ok := w.walk(ce, childPath); ok {
				return p, b, true
			}
		}
	case format.EntryTypeRegular:
		b, err := w.c.ReadBlob(object.ID(e.ContentID))
		if err != nil {
			return "", 0, false
		}
		if b.TotalSize > w.budget {
			return path, b.TotalSize, true
		}
	}
	return "", 0, false
}

// walker collects the object ids one Build call needs, reading trees
// and blobs from the cache alone.
type walker struct {
	c      *cache.Cache
	needed map[object.ID]format.ObjectKind
}

// addEntry adds e's own object (a tree for a directory, a blob and its
// chunks for a regular file) and, for a directory, recurses into it.
// Every other entry type (symlink, device, fifo, socket) stores no
// object of its own and is skipped.
func (w *walker) addEntry(e format.TreeEntry) error {
	switch e.EntryType {
	case format.EntryTypeDirectory:
		return w.addTree(object.ID(e.ContentID))
	case format.EntryTypeRegular:
		w.addBlob(object.ID(e.ContentID))
	}
	return nil
}

// addTree adds treeID and recurses into every child a directory entry
// names. Build's caller already proved every tree the whole snapshot
// reaches is cached, so a read failure here is a hard error, not an
// incompleteness to degrade past.
func (w *walker) addTree(treeID object.ID) error {
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
func (w *walker) addBlob(blobID object.ID) {
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

// resolvePath walks rootEntries for pathArg, the same rule ls and
// --include use: a path either names a root entry directly, or
// descends from one root entry's own ROOT_PATH into its subtree.
func resolvePath(c *cache.Cache, rootEntries []format.TreeEntry, pathArg string) (*format.TreeEntry, string, error) {
	segs := splitPathSegs(pathArg)
	if len(segs) == 0 {
		return nil, "", fmt.Errorf("empty path")
	}

	var cur *format.TreeEntry
	var consumed []string
	for i := range rootEntries {
		e := rootEntries[i]
		rp := splitPathSegs(rootPathOf(e))
		if len(rp) == 0 || len(rp) > len(segs) || !segsEqual(rp, segs[:len(rp)]) {
			continue
		}
		cur = &e
		consumed = rp
		break
	}
	if cur == nil {
		return nil, "", fmt.Errorf("path %q matches no entry", pathArg)
	}

	curEntry := *cur
	for _, name := range segs[len(consumed):] {
		if curEntry.EntryType != format.EntryTypeDirectory {
			return nil, "", fmt.Errorf("path %q matches no entry", pathArg)
		}
		t, err := c.ReadTree(object.ID(curEntry.ContentID))
		if err != nil {
			return nil, "", err
		}
		found := false
		for _, e := range t.Entries {
			if string(e.Name) == name {
				curEntry = e
				found = true
				break
			}
		}
		if !found {
			return nil, "", fmt.Errorf("path %q matches no entry", pathArg)
		}
		consumed = append(consumed, name)
	}
	return &curEntry, strings.Join(consumed, "/"), nil
}

func segsEqual(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitPathSegs splits a forward-slash path into segments, stripping
// one leading slash and dropping any empty segment a doubled or
// trailing slash would otherwise produce.
func splitPathSegs(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// rootPathOf returns e's ROOT_PATH TLV payload, or "" when absent.
func rootPathOf(e format.TreeEntry) string {
	for _, t := range e.TLVs {
		if t.Type == format.TLVTypeRootPath {
			return string(t.Payload)
		}
	}
	return ""
}

// group maps every needed object to a disc through c.LocateObject and
// c.DiscForRun, groups the result by disc, and orders the discs by
// "14.1 The planner"'s tie-breaks: most bytes first, then the newer
// disc, then the lower disc_seq.
func group(c *cache.Cache, needed map[object.ID]format.ObjectKind) *Result {
	byDisc := make(map[[16]byte]*DiscEntry)
	missingByRun := make(map[uint64]int)

	ids := make([]object.ID, 0, len(needed))
	for id := range needed {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].TextForm() < ids[j].TextForm() })

	r := &Result{}
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
			e = &DiscEntry{DiscSeq: row.DiscSeq, DiscUUID: row.DiscUUID, Label: discRowLabel(row), Created: row.CreatedSec}
			byDisc[row.DiscUUID] = e
		}
		if !slices.Contains(e.Runs, loc.RunSeq) {
			e.Runs = append(e.Runs, loc.RunSeq)
		}
		e.Objects = append(e.Objects, ObjectEntry{ID: id, Kind: needed[id], Bytes: loc.PayloadLen})
		e.Bytes += loc.PayloadLen
		r.TotalObjects++
		r.TotalBytes += loc.PayloadLen
		if loc.SizeKnown && loc.PayloadLen > r.PeakStagingBytes {
			r.PeakStagingBytes = loc.PayloadLen
		}
	}

	discs := make([]DiscEntry, 0, len(byDisc))
	for _, e := range byDisc {
		slices.Sort(e.Runs)
		discs = append(discs, *e)
	}
	sort.Slice(discs, func(i, j int) bool {
		a, b := discs[i], discs[j]
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
		if a.Created != b.Created {
			return a.Created > b.Created
		}
		return a.DiscSeq < b.DiscSeq
	})
	for i := range discs {
		discs[i].Order = i
	}
	r.Discs = discs

	runSeqs := make([]uint64, 0, len(missingByRun))
	for seq := range missingByRun {
		runSeqs = append(runSeqs, seq)
	}
	slices.Sort(runSeqs)
	for _, seq := range runSeqs {
		r.Missing = append(r.Missing, MissingEntry{RunSeq: seq, Objects: missingByRun[seq]})
	}

	return r
}

// discRowLabel trims a DISCS row's fixed-width label field.
func discRowLabel(row format.DiscsRow) string {
	n := min(int(row.LabelLen), len(row.Label))
	return string(row.Label[:n])
}

// UUIDText formats a 16-byte uuid as hyphenated lowercase text.
func UUIDText(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
