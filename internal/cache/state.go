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
// present in the cache, naming, when it can be resolved, the run or
// the disc that holds a missing tree.
type IncompleteError struct {
	// Snapshot is the snapshot whose tree set is incomplete.
	Snapshot object.ID
	// MissingTree is the first tree id the walk could not find.
	MissingTree object.ID
	// RunSeq is the run known to store MissingTree, resolved through a
	// cached run's INDEX Objects or Prereqs table. Zero when unknown.
	RunSeq uint64
	// DiscUUID is the disc that run was burned onto, resolved through a
	// cached DISCS table. HasDiscUUID is false when unknown.
	DiscUUID    [16]byte
	HasDiscUUID bool
}

func (e *IncompleteError) Error() string {
	switch {
	case e.HasDiscUUID:
		return fmt.Sprintf("snapshot %s: tree %s is missing from the cache; run %d, disc %s holds it",
			e.Snapshot.TextForm(), e.MissingTree.TextForm(), e.RunSeq, uuidText(e.DiscUUID))
	case e.RunSeq != 0:
		return fmt.Sprintf("snapshot %s: tree %s is missing from the cache; run %d holds it, but no cached DISCS row names its disc",
			e.Snapshot.TextForm(), e.MissingTree.TextForm(), e.RunSeq)
	default:
		return fmt.Sprintf("snapshot %s: tree %s is missing from the cache; no cached run's INDEX names the run that holds it",
			e.Snapshot.TextForm(), e.MissingTree.TextForm())
	}
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
// reachable tree is present.
func (c *Cache) walkTrees(id object.ID) (missing object.ID, complete bool, err error) {
	snap, err := c.ReadSnapshot(id)
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
// first missing tree, resolving the tree's owning run through every
// cached run's INDEX, and that run's disc through the newest cached
// DISCS table.
func (c *Cache) incompleteError(id, missingTree object.ID) *IncompleteError {
	e := &IncompleteError{Snapshot: id, MissingTree: missingTree}

	seqs, err := c.cachedRunSeqs()
	if err != nil {
		return e
	}
	for _, seq := range seqs {
		idx, err := c.IndexForRun(seq)
		if err != nil {
			continue
		}
		if runSeq, found := findOwningRun(idx, missingTree); found {
			e.RunSeq = runSeq
			break
		}
	}
	if e.RunSeq == 0 {
		return e
	}

	discs, err := c.Discs()
	if err != nil {
		return e
	}
	for _, row := range discs.Rows {
		if row.RunSeq == e.RunSeq {
			e.DiscUUID = row.DiscUUID
			e.HasDiscUUID = true
			break
		}
	}
	return e
}

// findOwningRun looks for id in idx's Objects table, then its Prereqs
// table, and reports the run_seq that stores it directly.
func findOwningRun(idx *format.Index, id object.ID) (runSeq uint64, found bool) {
	for _, row := range idx.Objects {
		if object.ID(row.ContentID) == id {
			return idx.RunSeq, true
		}
	}
	for _, row := range idx.Prereqs {
		if object.ID(row.ContentID) == id {
			return row.RunSeq, true
		}
	}
	return 0, false
}
