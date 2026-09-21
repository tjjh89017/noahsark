// Package plan computes a restore plan from the local cache alone,
// OPERATIONS.md's "14. Restore" section "14.1 The planner". The
// "restore" command's disc-swap mode reads objects in plan order as
// each disc is inserted, and "restore --dry-run" prints the same plan
// with no disc read.
package plan

import (
	"bytes"
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
	Objects  []ObjectEntry
	Bytes    uint64
}

// MissingEntry is one group of objects a plan could not place: either a
// resolved disc that no cached DISCS row describes, or an object no
// cached INDEX names at all (HasDisc false).
type MissingEntry struct {
	DiscUUID [16]byte
	HasDisc  bool
	Objects  int
}

// Result is the outcome of grouping every needed object by the disc
// that holds it, in plan order.
type Result struct {
	Discs        []DiscEntry
	Missing      []MissingEntry
	TotalObjects int
	TotalBytes   uint64
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
	w, err := collectObjects(c, snap, includes)
	if err != nil {
		return nil, err
	}
	w.add(snapID, format.ObjectKindSnapshot)
	return group(c, w.needed, w.order), nil
}

// collectObjects walks the cached trees under includes (the whole
// snapshot when includes is empty), starting from snap's root tree, and
// returns the walker holding every object id a restore of that scope
// needs, tagged by kind and in discovery order. It never reads a
// chunk's payload.
func collectObjects(c *cache.Cache, snap *format.Snapshot, includes []string) (*walker, error) {
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
		return w, nil
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
	return w, nil
}

// walker collects the object ids one Build call needs, reading trees
// and blobs from the cache alone. order records the ids in the order
// they were first found, a depth-first, file-by-file tree walk: a
// blob's own chunk ids always sit right after it. group() reads objects
// off a disc in this order, so a blob's own chunks stay adjacent in the
// disc's own object list, letting a restore free a file's spool bytes
// as soon as its last chunk arrives.
type walker struct {
	c      *cache.Cache
	needed map[object.ID]format.ObjectKind
	order  []object.ID
}

// add records id, tagged kind, the first time it is seen, preserving
// w.order's discovery order. It reports whether id was new.
func (w *walker) add(id object.ID, kind format.ObjectKind) bool {
	if _, ok := w.needed[id]; ok {
		return false
	}
	w.needed[id] = kind
	w.order = append(w.order, id)
	return true
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
	if !w.add(treeID, format.ObjectKindTree) {
		return nil
	}
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
// snapshots pack or recover have processed since blob caching was
// added, and its absence is not, by itself, an incomplete cache.
func (w *walker) addBlob(blobID object.ID) {
	if !w.add(blobID, format.ObjectKindBlob) {
		return
	}
	b, err := w.c.ReadBlob(blobID)
	if err != nil {
		return
	}
	for _, e := range b.Entries {
		w.add(object.ID(e.ContentID), format.ObjectKindChunk)
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
// c.DiscRow, groups the result by disc, and orders the discs by
// "14.1 The planner"'s tie-breaks: most bytes first, then the newer
// disc, then the lower disc_seq.
//
// Only chunk objects count toward a disc's Objects and Bytes: a
// disc-swap restore resolves every tree, blob and the snapshot itself
// from the local cache alone, the same cache group reads from, and
// never opens a disc for them. A disc that holds none of the needed
// chunks is dropped from the result entirely, even when it happens to
// hold a needed tree or blob, so the discs printed here are exactly
// the discs a restore of this plan reads, in the order it reads them.
//
// Within one disc, objects keep order's relative order: the walk's
// depth-first, file-by-file discovery order, so a blob's own chunks
// stay adjacent in each DiscEntry.Objects, letting a restore free a
// file's spool bytes as soon as its last chunk is read.
func group(c *cache.Cache, needed map[object.ID]format.ObjectKind, order []object.ID) *Result {
	byDisc := make(map[[16]byte]*DiscEntry)
	missingByDisc := make(map[[16]byte]int)
	missingDiscUnknown := 0

	r := &Result{}
	for _, id := range order {
		loc, found := c.LocateObject(id)
		if !found {
			missingDiscUnknown++
			continue
		}
		row, found := c.DiscRow(loc.DiscUUID)
		if !found {
			missingByDisc[loc.DiscUUID]++
			continue
		}
		if needed[id] != format.ObjectKindChunk {
			// Located, so not missing, but never read from row's disc
			// by a restore: nothing to add to the plan.
			continue
		}
		e, ok := byDisc[row.DiscUUID]
		if !ok {
			e = &DiscEntry{DiscSeq: row.DiscSeq, DiscUUID: row.DiscUUID, Label: discRowLabel(row), Created: row.CreatedSec}
			byDisc[row.DiscUUID] = e
		}
		e.Objects = append(e.Objects, ObjectEntry{ID: id, Kind: needed[id], Bytes: loc.PayloadLen})
		e.Bytes += loc.PayloadLen
		r.TotalObjects++
		r.TotalBytes += loc.PayloadLen
	}

	discs := make([]DiscEntry, 0, len(byDisc))
	for _, e := range byDisc {
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

	uuids := make([][16]byte, 0, len(missingByDisc))
	for uuid := range missingByDisc {
		uuids = append(uuids, uuid)
	}
	slices.SortFunc(uuids, func(a, b [16]byte) int { return bytes.Compare(a[:], b[:]) })
	for _, uuid := range uuids {
		r.Missing = append(r.Missing, MissingEntry{DiscUUID: uuid, HasDisc: true, Objects: missingByDisc[uuid]})
	}
	if missingDiscUnknown > 0 {
		r.Missing = append(r.Missing, MissingEntry{Objects: missingDiscUnknown})
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
