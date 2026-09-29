package catalog

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// The two marks of a line of catalog-state.txt.
const (
	markComplete = "complete"
	markPartial  = "partial"
)

// loadState reads the completeness file at path. A missing file means
// that no snapshot is recorded yet, not an error.
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
			return nil, fmt.Errorf("%s: malformed line %q", stateFileName, line)
		}
		switch mark {
		case markComplete:
			state[id] = true
		case markPartial:
			state[id] = false
		default:
			return nil, fmt.Errorf("%s: unknown mark %q", stateFileName, mark)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return state, nil
}

// saveState writes the whole completeness file with an atomic replace.
// It writes one line for each snapshot id, sorted by id, so that a diff
// of two versions stays small.
func saveState(path string, state map[string]bool) error {
	var b strings.Builder
	for _, id := range slices.Sorted(maps.Keys(state)) {
		mark := markPartial
		if state[id] {
			mark = markComplete
		}
		_, _ = fmt.Fprintf(&b, "%s %s\n", id, mark)
	}
	return atomicWriteFile(path, []byte(b.String()))
}

// setComplete records the completeness of id and writes the file at once.
func (c *Catalog) setComplete(id object.ID, complete bool) error {
	c.state[id.TextForm()] = complete
	if err := saveState(c.statePath, c.state); err != nil {
		return fmt.Errorf("catalog: %s: %w", stateFileName, err)
	}
	return nil
}

// MarkComplete records that the catalog holds every object that snapshot
// id reaches.
func (c *Catalog) MarkComplete(id object.ID) error {
	return c.setComplete(id, true)
}

// MarkPartial records that the catalog does not hold every object that
// snapshot id reaches.
func (c *Catalog) MarkPartial(id object.ID) error {
	return c.setComplete(id, false)
}

// Complete reports whether the completeness file marks id complete. An
// id that the file does not list reports false.
func (c *Catalog) Complete(id object.ID) bool {
	return c.state[id.TextForm()]
}

// Partial reports whether the completeness file marks id partial.
func (c *Catalog) Partial(id object.ID) bool {
	complete, listed := c.state[id.TextForm()]
	return listed && !complete
}

// PartialError reports that the catalog does not hold every tree and
// blob object that a snapshot reaches. When it can, it names the disc
// that holds a missing object.
type PartialError struct {
	// Snapshot is the partial snapshot.
	Snapshot object.ID
	// MissingObject is the first tree or blob id that the walk could not
	// find, or Snapshot itself when the catalog does not hold the
	// snapshot object.
	MissingObject object.ID
	// DiscUUID is the disc that stores MissingObject, found through the
	// Objects or Prereqs table of an INDEX in the catalog. HasDiscUUID
	// is false when no INDEX names the disc.
	DiscUUID    [16]byte
	HasDiscUUID bool
	// Label is the label of that disc, empty when no DISCS row in the
	// catalog names the disc.
	Label string
}

func (e *PartialError) Error() string {
	if e.HasDiscUUID {
		return fmt.Sprintf("snapshot %s: object %s is missing from the catalog; disc %s%s holds it",
			e.Snapshot.TextForm(), e.MissingObject.TextForm(), uuidText(e.DiscUUID), LabelSuffix(e.Label))
	}
	return fmt.Sprintf("snapshot %s: object %s is missing from the catalog; no INDEX in the catalog names the disc that holds it",
		e.Snapshot.TextForm(), e.MissingObject.TextForm())
}

// LabelSuffix renders a disc label for a message that already names the
// disc uuid, and renders nothing when the label is unknown.
func LabelSuffix(label string) string {
	if label == "" {
		return ""
	}
	return " (" + label + ")"
}

// CheckComplete reports whether the catalog holds every tree and blob
// object that snapshot id reaches. It trusts the completeness file when
// Complete already says true. Otherwise it returns a PartialError that
// names the missing object and, when it can, the disc that holds it.
func (c *Catalog) CheckComplete(id object.ID) error {
	if c.Complete(id) {
		return nil
	}
	missing, ok, err := c.walkObjects(id)
	if err != nil {
		return err
	}
	if ok {
		return c.setComplete(id, true)
	}
	return c.partialError(id, missing)
}

// walkObjects walks every tree and blob reachable from snapshot id's
// root tree, using only objects already present in the catalog. It
// returns the first object id it could not find and false, or a zero id
// and true when every reachable object is present. A tree that does not
// decode counts as missing. A snapshot id the catalog has never seen at
// all is reported as missing, id itself, rather than a hard error: a
// pack or a recover that never saw this snapshot's disc leaves exactly
// that gap, and CheckComplete resolves it the same way it resolves a
// missing tree.
func (c *Catalog) walkObjects(id object.ID) (missing object.ID, complete bool, err error) {
	snap, err := c.ReadSnapshot(id)
	if os.IsNotExist(err) {
		return id, false, nil
	}
	if err != nil {
		return object.ID{}, false, err
	}
	seen := make(map[object.ID]bool)
	pending := []object.ID{object.ID(snap.RootTree)}
	for len(pending) > 0 {
		treeID := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[treeID] {
			continue
		}
		seen[treeID] = true
		tree, err := c.ReadTree(treeID)
		if err != nil {
			return treeID, false, nil
		}
		for _, e := range tree.Entries {
			switch e.EntryType {
			case format.EntryTypeDirectory:
				pending = append(pending, object.ID(e.ContentID))
			case format.EntryTypeRegular:
				blobID := object.ID(e.ContentID)
				_, err := os.Stat(c.MetaPath(format.ObjectKindBlob, blobID))
				if os.IsNotExist(err) {
					return blobID, false, nil
				}
				if err != nil {
					return object.ID{}, false, err
				}
			}
		}
	}
	return object.ID{}, true, nil
}

// RefreshComplete computes again and saves the completeness of snapshot
// id. It does not trust the completeness file. It returns only I/O
// errors, never a PartialError; call CheckComplete for a resolved report
// of what is missing.
func (c *Catalog) RefreshComplete(id object.ID) error {
	_, ok, err := c.walkObjects(id)
	if err != nil {
		return err
	}
	return c.setComplete(id, ok)
}

// partialError builds a PartialError for snapshot id and its first
// missing object, resolving the disc that holds the object through
// LocateObject and its label through DiscRow.
func (c *Catalog) partialError(id, missingObject object.ID) *PartialError {
	e := &PartialError{Snapshot: id, MissingObject: missingObject}

	loc, found := c.LocateObject(missingObject)
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

// ObjectLocation reports where a catalog disc's INDEX says one object
// lives.
type ObjectLocation struct {
	// DiscUUID is the disc that stores the object.
	DiscUUID [16]byte
	// ByteLen is the object file's length on the disc. SizeKnown is
	// true only when the disc named by DiscUUID is itself in the catalog, so
	// its own Files row, which carries the length, was read directly;
	// a disc known only through another catalog disc's Prereqs table
	// names the disc but not the length.
	ByteLen   uint64
	SizeKnown bool
}

// LocateObject looks across every catalog disc's INDEX for id, first in
// each disc's own Objects table, then, failing that, in each disc's
// Prereqs table, and reports the disc that stores it. An Objects table
// match is preferred and returned at once, since it also carries the
// object's size; a Prereqs table match is kept only as a fallback, in
// case some other catalog disc's Objects table still resolves the same
// id with its size.
//
// A Prereqs row names the disc by uuid, so the lookup never depends on
// a run number, which can repeat after a repository is rebuilt.
func (c *Catalog) LocateObject(id object.ID) (ObjectLocation, bool) {
	uuids, err := c.catalogDiscs()
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
		objectRows := objectFileRows(idx)
		for i, row := range idx.Objects {
			if object.ID(row.ContentID) == id {
				return ObjectLocation{DiscUUID: uuid, ByteLen: objectRows[i].ByteLen, SizeKnown: true}, true
			}
		}
		if haveFallback {
			continue
		}
		for _, row := range idx.Prereqs {
			if object.ID(row.ContentID) != id {
				continue
			}
			fallback = ObjectLocation{DiscUUID: row.DiscUUID}
			haveFallback = true
			break
		}
	}
	if haveFallback {
		return fallback, true
	}
	return ObjectLocation{}, false
}

// objectFileRows returns a run's role 13 Files rows, in row order. The
// j-th of them describes the file of Objects row j.
func objectFileRows(idx *format.Index) []format.IndexFileRecord {
	rows := make([]format.IndexFileRecord, 0, idx.ObjectCount)
	for _, row := range idx.Files {
		if row.Role == format.FileRoleObject {
			rows = append(rows, row)
		}
	}
	return rows
}

// DiscRow returns the DISCS row for uuid: the disc's own catalog table
// first, then the table of any other catalog disc that names it.
func (c *Catalog) DiscRow(uuid [16]byte) (format.DiscsRow, bool) {
	if row, found := c.ownDiscRow(uuid); found {
		return row, true
	}
	uuids, err := c.catalogDiscs()
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
