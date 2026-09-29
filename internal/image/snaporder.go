package image

import (
	"fmt"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// StagedSnapshot is one snapshot whose snapshot object no disc holds.
type StagedSnapshot struct {
	ID   object.ID
	Time time.Time
	// StagedItems counts the objects that the snapshot reaches and that
	// no disc holds, the snapshot object included.
	StagedItems int
	// PackedInParts is true when a disc holds one or more objects that
	// the snapshot reaches.
	PackedInParts bool
}

// StagedSnapshots returns each snapshot of snapshotIDs whose snapshot
// object onDisc does not report, in the order in which pack takes them:
// the snapshots packed in parts first, then the others; inside each
// group the oldest snapshot time first, then the id bytes.
//
// It walks one snapshot at a time and keeps only the ids of that one
// walk. It stops at an object that onDisc reports, as buildPackOrder
// does, so it never needs the staged file of such an object.
func StagedSnapshots(objectPath ObjectPathFunc, snapshotIDs []object.ID, onDisc func(object.ID) bool) ([]StagedSnapshot, error) {
	var out []StagedSnapshot
	for _, id := range snapshotIDs {
		if onDisc(id) {
			continue
		}
		data, err := readObjectFile(objectPath(format.ObjectKindSnapshot, id))
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", id.TextForm(), err)
		}
		var snap format.Snapshot
		if _, err := snap.Decode(data); err != nil {
			return nil, stagedDamaged(id, format.ObjectKindSnapshot)
		}
		w := partWalk{objectPath: objectPath, onDisc: onDisc, seen: make(map[object.ID]bool)}
		if err := w.tree(object.ID(snap.RootTree)); err != nil {
			return nil, err
		}
		out = append(out, StagedSnapshot{
			ID:            id,
			Time:          time.Unix(snap.TimeSec, int64(snap.TimeNsec)),
			StagedItems:   w.staged + 1,
			PackedInParts: w.reachedDisc,
		})
	}
	sort.Slice(out, func(i, j int) bool { return packsBefore(out[i], out[j]) })
	return out, nil
}

// packsBefore reports whether pack takes snapshot a before snapshot b.
func packsBefore(a, b StagedSnapshot) bool {
	if a.PackedInParts != b.PackedInParts {
		return a.PackedInParts
	}
	if !a.Time.Equal(b.Time) {
		return a.Time.Before(b.Time)
	}
	return lessBytes(a.ID[:], b.ID[:])
}

// partWalk counts the objects of one snapshot that no disc holds, and
// notes whether the snapshot reaches an object that a disc holds.
type partWalk struct {
	objectPath  ObjectPathFunc
	onDisc      func(object.ID) bool
	seen        map[object.ID]bool
	staged      int
	reachedDisc bool
}

// visit counts id once. It reports true when the walk must read the
// object of id: the first visit of an object that no disc holds.
func (w *partWalk) visit(id object.ID) bool {
	if w.seen[id] {
		return false
	}
	w.seen[id] = true
	if w.onDisc(id) {
		w.reachedDisc = true
		return false
	}
	w.staged++
	return true
}

func (w *partWalk) tree(id object.ID) error {
	if !w.visit(id) {
		return nil
	}
	data, err := readObjectFile(w.objectPath(format.ObjectKindTree, id))
	if err != nil {
		return fmt.Errorf("tree %s: %w", id.TextForm(), err)
	}
	var tree format.Tree
	if _, err := tree.Decode(data); err != nil {
		return stagedDamaged(id, format.ObjectKindTree)
	}
	for _, entry := range tree.Entries {
		var err error
		switch entry.EntryType {
		case format.EntryTypeDirectory:
			err = w.tree(object.ID(entry.ContentID))
		case format.EntryTypeRegular:
			err = w.blob(object.ID(entry.ContentID))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *partWalk) blob(id object.ID) error {
	if !w.visit(id) {
		return nil
	}
	data, err := readObjectFile(w.objectPath(format.ObjectKindBlob, id))
	if err != nil {
		return fmt.Errorf("blob %s: %w", id.TextForm(), err)
	}
	var blob format.Blob
	if _, err := blob.Decode(data); err != nil {
		return stagedDamaged(id, format.ObjectKindBlob)
	}
	for _, e := range blob.Entries {
		w.visit(object.ID(e.ContentID))
	}
	return nil
}

// packWalkOrder returns snapshotIDs in the order buildPackOrder walks
// them: the staged snapshots in the order of StagedSnapshots, then the
// snapshots that a disc holds.
func packWalkOrder(objectPath ObjectPathFunc, snapshotIDs []object.ID, onDisc func(object.ID) bool) ([]object.ID, error) {
	staged, err := StagedSnapshots(objectPath, snapshotIDs, onDisc)
	if err != nil {
		return nil, err
	}
	order := make([]object.ID, 0, len(snapshotIDs))
	for _, s := range staged {
		order = append(order, s.ID)
	}
	for _, id := range snapshotIDs {
		if onDisc(id) {
			order = append(order, id)
		}
	}
	return order, nil
}
