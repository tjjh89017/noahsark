package cache

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// loadState reads the completeness record at path. A missing file
// means no snapshot has been recorded yet, not an error.
func loadState(path string) (map[string]bool, error) {
	state := make(map[string]bool)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		id, mark, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("state.txt: malformed line %q", line)
		}
		switch mark {
		case "complete":
			state[id] = true
		case "incomplete":
			state[id] = false
		default:
			return nil, fmt.Errorf("state.txt: unknown mark %q", mark)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return state, nil
}

// saveState writes the whole completeness record back, one line per
// snapshot id, sorted for a deterministic file.
func saveState(path string, state map[string]bool) error {
	ids := make([]string, 0, len(state))
	for id := range state {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	for _, id := range ids {
		mark := "incomplete"
		if state[id] {
			mark = "complete"
		}
		_, _ = fmt.Fprintf(&b, "%s %s\n", id, mark)
	}
	return atomicWriteFile(path, []byte(b.String()))
}

// setComplete records id's completeness and persists it at once.
func (c *Cache) setComplete(id object.ID, complete bool) error {
	c.state[id.TextForm()] = complete
	return saveState(c.statePath(), c.state)
}

// Complete reports whether id's whole tree set is present in the cache,
// from the last time it was written or verified. An id this cache has
// never seen reports false.
func (c *Cache) Complete(id object.ID) bool {
	return c.state[id.TextForm()]
}

// IncompleteError reports that a snapshot's tree set is not fully
// present in the cache, naming, when it can be resolved, the disc that
// holds a missing tree.
type IncompleteError struct {
	// Snapshot is the snapshot whose tree set is incomplete.
	Snapshot object.ID
	// MissingTree is the first tree id the walk could not find, or
	// Snapshot itself when the cache never received the snapshot
	// object at all.
	MissingTree object.ID
	// DiscUUID is the disc known to store MissingTree, resolved through
	// a cached disc's INDEX Objects or Prereqs table. HasDiscUUID is
	// false when unknown.
	DiscUUID    [16]byte
	HasDiscUUID bool
	// Label is that disc's on-disc label, empty when no cached DISCS
	// row names the disc.
	Label string
}

func (e *IncompleteError) Error() string {
	if e.HasDiscUUID {
		return fmt.Sprintf("snapshot %s: object %s is missing from the cache; disc %s%s holds it",
			e.Snapshot.TextForm(), e.MissingTree.TextForm(), uuidText(e.DiscUUID), LabelSuffix(e.Label))
	}
	return fmt.Sprintf("snapshot %s: object %s is missing from the cache; no cached INDEX names the disc that holds it",
		e.Snapshot.TextForm(), e.MissingTree.TextForm())
}

// LabelSuffix renders a disc label for a message that already names the
// disc uuid, and renders nothing when the label is unknown.
func LabelSuffix(label string) string {
	if label == "" {
		return ""
	}
	return " (" + label + ")"
}

// CheckComplete reports whether id's tree set is present in the cache.
// It trusts the persisted record when Complete already says true, and
// otherwise resolves an IncompleteError naming the missing tree and,
// where possible, the run or disc that holds it.
func (c *Cache) CheckComplete(id object.ID) error {
	if c.Complete(id) {
		return nil
	}
	missing, ok, err := c.walkTrees(id)
	if err != nil {
		return err
	}
	if ok {
		return c.setComplete(id, true)
	}
	return c.incompleteError(id, missing)
}

// walkTrees walks every tree reachable from snapshot id's root tree,
// using only trees already present in the cache. It returns the first
// tree id it could not find and false, or a zero id and true when every
// reachable tree is present. A snapshot id the cache has never seen at
// all is reported as missing, id itself, rather than a hard error: a
// pack or a recover that never saw this snapshot's disc leaves
// exactly that gap, and CheckComplete resolves it the same way it
// resolves a missing tree.
func (c *Cache) walkTrees(id object.ID) (missing object.ID, complete bool, err error) {
	snap, err := c.ReadSnapshot(id)
	if os.IsNotExist(err) {
		return id, false, nil
	}
	if err != nil {
		return object.ID{}, false, err
	}
	seen := make(map[object.ID]bool)
	ok := true
	var missID object.ID
	var walk func(treeID object.ID)
	walk = func(treeID object.ID) {
		if !ok || seen[treeID] {
			return
		}
		seen[treeID] = true
		tree, err := c.ReadTree(treeID)
		if err != nil {
			ok = false
			missID = treeID
			return
		}
		for _, e := range tree.Entries {
			if e.EntryType == format.EntryTypeDirectory {
				walk(object.ID(e.ContentID))
			}
		}
	}
	walk(object.ID(snap.RootTree))
	return missID, ok, nil
}

// refreshComplete recomputes and persists id's completeness. It returns
// only I/O errors, never an IncompleteError; call CheckComplete for a
// resolved report of what is missing.
func (c *Cache) refreshComplete(id object.ID) error {
	_, ok, err := c.walkTrees(id)
	if err != nil {
		return err
	}
	return c.setComplete(id, ok)
}

// incompleteError builds an IncompleteError for snapshot id and its
// first missing tree, resolving the disc that holds the tree through
// LocateObject and its label through DiscRow.
func (c *Cache) incompleteError(id, missingTree object.ID) *IncompleteError {
	e := &IncompleteError{Snapshot: id, MissingTree: missingTree}

	loc, found := c.LocateObject(missingTree)
	if !found {
		return e
	}
	e.DiscUUID = loc.DiscUUID
	e.HasDiscUUID = true
	if row, found := c.DiscRow(loc.DiscUUID); found {
		e.Label = discLabelText(row)
	}
	return e
}

// ObjectLocation reports where a cached disc's INDEX says one object
// lives.
type ObjectLocation struct {
	// DiscUUID is the disc that stores the object.
	DiscUUID [16]byte
	// PayloadLen is the object's uncompressed size. SizeKnown is true
	// only when the disc named by DiscUUID is itself cached, so its own
	// Objects row, which carries the size, was read directly; a disc
	// known only through another cached disc's Prereqs table names the
	// disc but not the size.
	PayloadLen uint64
	SizeKnown  bool
}

// LocateObject looks across every cached disc's INDEX for id, first in
// each disc's own Objects table, then, failing that, in each disc's
// Prereqs table, and reports the disc that stores it. An Objects table
// match is preferred and returned at once, since it also carries the
// object's size; a Prereqs table match is kept only as a fallback, in
// case some other cached disc's Objects table still resolves the same
// id with its size.
//
// A Prereqs row names a run_seq, a number the host assigns. Each disc
// resolves its own Prereqs rows through its own cached DISCS table, so
// a run_seq that repeats on another disc never sends the lookup to the
// wrong disc.
func (c *Cache) LocateObject(id object.ID) (ObjectLocation, bool) {
	uuids, err := c.cachedDiscs()
	if err != nil {
		return ObjectLocation{}, false
	}
	var fallback ObjectLocation
	haveFallback := false
	for _, uuid := range uuids {
		idx, err := c.IndexForDisc(uuid)
		if err != nil {
			continue
		}
		for _, row := range idx.Objects {
			if object.ID(row.ContentID) == id {
				return ObjectLocation{DiscUUID: uuid, PayloadLen: row.PayloadLen, SizeKnown: true}, true
			}
		}
		if haveFallback {
			continue
		}
		for _, row := range idx.Prereqs {
			if object.ID(row.ContentID) != id {
				continue
			}
			if holder, ok := c.runHolderOf(uuid, row.RunSeq); ok {
				fallback = ObjectLocation{DiscUUID: holder}
				haveFallback = true
			}
			break
		}
	}
	if haveFallback {
		return fallback, true
	}
	return ObjectLocation{}, false
}

// runHolderOf returns the disc that uuid's own cached DISCS table names
// for runSeq.
func (c *Cache) runHolderOf(uuid [16]byte, runSeq uint64) ([16]byte, bool) {
	discs, err := c.discsTableOf(uuid)
	if err != nil {
		return [16]byte{}, false
	}
	for _, row := range discs.Rows {
		if row.RunSeq == runSeq {
			return row.DiscUUID, true
		}
	}
	return [16]byte{}, false
}

// DiscRow returns the DISCS row for uuid: the disc's own cached table
// first, then the table of any other cached disc that names it.
func (c *Cache) DiscRow(uuid [16]byte) (format.DiscsRow, bool) {
	if row, found := c.ownDiscRow(uuid); found {
		return row, true
	}
	uuids, err := c.cachedDiscs()
	if err != nil {
		return format.DiscsRow{}, false
	}
	for _, other := range uuids {
		discs, err := c.discsTableOf(other)
		if err != nil {
			continue
		}
		for _, row := range discs.Rows {
			if row.DiscUUID == uuid {
				return row, true
			}
		}
	}
	return format.DiscsRow{}, false
}

// discLabelText trims a DISCS row's fixed-width label field to its
// stored length.
func discLabelText(row format.DiscsRow) string {
	n := min(int(row.LabelLen), len(row.Label))
	return string(row.Label[:n])
}
