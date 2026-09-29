package image

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// SnapshotGroup is one snapshot of the pack order and the Staged items
// that pack takes with it.
type SnapshotGroup struct {
	ID   object.ID
	Time time.Time
	// OnDisc is true when the state log names a disc for the snapshot
	// object: Packed, OnDisc or Lost.
	OnDisc bool
	// StagedItems counts the Staged items that pack takes with the
	// snapshot: each Staged item that the snapshot reaches and that no
	// snapshot before it in the pack order reaches, the snapshot object
	// included when it is Staged. It also counts the items that pack
	// cannot read.
	StagedItems int
	// Unreadable names the first item of the snapshot that pack cannot
	// read, or nil. pack does not take that item, the trees above it and
	// the snapshot object.
	Unreadable *UnreadableItem
}

// UnreadableItem is one item that pack cannot take: a Staged tree, blob
// or snapshot object whose file is missing or does not verify, a Staged
// chunk that failed its check, or a snapshot object of the catalog that
// does not verify.
type UnreadableItem struct {
	// Snapshot is the snapshot whose walk found the item. It is the zero
	// id for an item that no snapshot reaches.
	Snapshot object.ID
	ID       object.ID
	// Kind is 0 when no file of the item exists.
	Kind format.ObjectKind
	// Missing is true when the file of the item does not exist.
	Missing bool
	// Path is the file of the item, when its kind is known.
	Path string
	// Err is the read error, when the file exists and cannot be read.
	Err error
}

// Problem describes the item for an operator message.
func (u *UnreadableItem) Problem() string {
	switch {
	case u.Kind == 0:
		return fmt.Sprintf("staged item %s has no file", u.ID.TextForm())
	case u.Missing:
		return fmt.Sprintf("the file of %s %s is missing", kindName(u.Kind), u.ID.TextForm())
	case u.Err != nil:
		return fmt.Sprintf("%s %s cannot be read: %v", kindName(u.Kind), u.ID.TextForm(), u.Err)
	}
	return fmt.Sprintf("%s %s is damaged", kindName(u.Kind), u.ID.TextForm())
}

// Repair names the action that makes the item readable again.
func (u *UnreadableItem) Repair() string {
	if u.Kind == format.ObjectKindChunk && !u.Missing {
		return fmt.Sprintf("delete %s, commit the same source again, then pack", u.Path)
	}
	return "commit the same source again, then pack; when the source is gone, the item cannot be packed"
}

// packPlan is the pack order of a repository: the candidate units in the
// order in which pack takes them, and the snapshot groups of that order.
type packPlan struct {
	units  []packUnit
	groups []SnapshotGroup
	// orphans are the unreadable items that no snapshot reaches.
	orphans []UnreadableItem
	// snapshotBytes holds the bytes of each snapshot object that a disc
	// holds: each run carries them again.
	snapshotBytes map[object.ID][]byte
}

// hasUnreadable reports whether the plan holds an item that pack cannot
// take.
func (p *packPlan) hasUnreadable() bool {
	if len(p.orphans) > 0 {
		return true
	}
	for _, g := range p.groups {
		if g.Unreadable != nil {
			return true
		}
	}
	return false
}

// PackGroups returns the snapshot groups of the pack order: the
// snapshots in the order in which pack takes them, each with the count
// of the Staged items that pack takes with it. It also returns the
// unreadable items that no snapshot reaches. It reads each snapshot
// object and each Staged tree and blob, and needs the file of an item
// that a disc holds only while a Staged item hides below such items.
func PackGroups(objectPath ObjectPathFunc, snapshotIDs []object.ID, log *stage.Log) ([]SnapshotGroup, []UnreadableItem) {
	plan := buildPackPlan(objectPath, snapshotIDs, log, nil, true, true)
	return plan.groups, plan.orphans
}

// buildPackPlan builds the pack order of the snapshots snapshotIDs.
//
// The snapshots go in the order of their snapshot time, the oldest
// first, and snapshots with equal times in the order of their id bytes.
// Each snapshot is walked in post-order: the chunks of a file, then its
// blob, then the tree of its directory, then the snapshot object. An
// item goes with the first snapshot that reaches it.
//
// The first walk stops at a tree or a blob that a disc holds. When that
// walk misses a Staged item of the state log, for example an item that
// `disc lost` returned to Staged below a tree on another disc, the walk
// runs again and goes down into the trees and blobs that a disc holds
// until it finds each such item. A Staged item that no snapshot reaches
// goes last, in the order of the id bytes, when withOrphans is set.
//
// A Staged item that cannot be read, and a blob that lists a chunk of
// damaged, is not taken. Neither are the trees above it and its
// snapshot object; each other item is.
func buildPackPlan(objectPath ObjectPathFunc, snapshotIDs []object.ID, log *stage.Log, damaged map[object.ID]bool, withOrphans, countOnly bool) *packPlan {
	snaps := readSnapshots(objectPath, snapshotIDs, log)
	w := newPlanWalk(objectPath, log, damaged, countOnly)
	w.walk(snaps)
	if missed := log.CountState(stage.Staged) - w.stagedReached; missed > 0 {
		want := make(map[object.ID]bool, missed)
		for _, id := range log.IDsInState(stage.Staged) {
			if !w.seen[id] && w.bad[id] == nil {
				want[id] = true
			}
		}
		w = newPlanWalk(objectPath, log, damaged, countOnly)
		w.want = want
		w.walk(snaps)
	}
	plan := &packPlan{units: w.units, groups: w.groups, snapshotBytes: make(map[object.ID][]byte)}
	for _, s := range snaps {
		if s.onDisc && s.bytes != nil {
			plan.snapshotBytes[s.id] = s.bytes
		}
	}
	if withOrphans && len(w.want) > 0 {
		w.orphans()
		plan.units = w.units
		plan.orphans = w.orphanItems
	}
	return plan
}

// snapInfo is one snapshot object as the walk needs it.
type snapInfo struct {
	id     object.ID
	time   time.Time
	root   object.ID
	onDisc bool
	bytes  []byte
	// unreadable is set when the snapshot object cannot be read.
	unreadable *UnreadableItem
}

// readSnapshots reads each snapshot object and returns them in pack
// order. A snapshot object that cannot be read has no time, and goes
// first.
func readSnapshots(objectPath ObjectPathFunc, ids []object.ID, log *stage.Log) []snapInfo {
	out := make([]snapInfo, 0, len(ids))
	for _, id := range ids {
		s := snapInfo{id: id, onDisc: onDiscIn(log, id)}
		data, snap, bad := readSnapshotObject(objectPath, id)
		if bad != nil {
			s.unreadable = bad
		} else {
			s.bytes = data
			s.time = time.Unix(snap.TimeSec, int64(snap.TimeNsec))
			s.root = object.ID(snap.RootTree)
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].time.Equal(out[j].time) {
			return out[i].time.Before(out[j].time)
		}
		return lessBytes(out[i].id[:], out[j].id[:])
	})
	return out
}

// readSnapshotObject reads, decodes and checks the snapshot object id.
func readSnapshotObject(objectPath ObjectPathFunc, id object.ID) ([]byte, format.Snapshot, *UnreadableItem) {
	var snap format.Snapshot
	data, bad := readChecked(objectPath, id, format.ObjectKindSnapshot)
	if bad != nil {
		return nil, snap, bad
	}
	if _, err := snap.Decode(data); err != nil {
		return nil, snap, &UnreadableItem{ID: id, Kind: format.ObjectKindSnapshot, Path: objectPath(format.ObjectKindSnapshot, id)}
	}
	return data, snap, nil
}

// readChecked reads the tree, blob or snapshot object id of kind and
// checks it against its content id.
func readChecked(objectPath ObjectPathFunc, id object.ID, kind format.ObjectKind) ([]byte, *UnreadableItem) {
	path := objectPath(kind, id)
	data, err := readObjectFile(path)
	if err != nil {
		return nil, &UnreadableItem{ID: id, Kind: kind, Path: path, Missing: errors.Is(err, fs.ErrNotExist), Err: missingOr(err)}
	}
	if verifyObjectID(id, kind, data) != nil {
		return nil, &UnreadableItem{ID: id, Kind: kind, Path: path}
	}
	return data, nil
}

// missingOr returns err, or nil when err says that the file does not
// exist: Missing reports that case.
func missingOr(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// onDiscIn reports whether the state log names a disc for id.
func onDiscIn(log *stage.Log, id object.ID) bool {
	rec, ok := log.Get(id)
	return ok && rec.State.OnDisc()
}

// planWalk is one walk of the snapshots in pack order.
type planWalk struct {
	objectPath ObjectPathFunc
	log        *stage.Log
	damaged    map[object.ID]bool
	// seen holds each item that the walk took, and each item of a disc
	// that it passed.
	seen map[object.ID]bool
	// bad holds each Staged item that the walk cannot take, with the
	// unreadable item that is the cause.
	bad   map[object.ID]*UnreadableItem
	units []packUnit
	// want holds the Staged items that the walk still looks for below
	// the trees and blobs of a disc. With none, the walk stops at them.
	want map[object.ID]bool
	// underDisc is true while the walk is in a snapshot whose snapshot
	// object a disc holds.
	underDisc bool
	// countOnly makes the walk count the units and keep none.
	countOnly     bool
	groups        []SnapshotGroup
	count         int
	stagedReached int
	orphanItems   []UnreadableItem
}

func newPlanWalk(objectPath ObjectPathFunc, log *stage.Log, damaged map[object.ID]bool, countOnly bool) *planWalk {
	return &planWalk{
		objectPath: objectPath, log: log, damaged: damaged, countOnly: countOnly,
		seen: make(map[object.ID]bool), bad: make(map[object.ID]*UnreadableItem),
	}
}

// itemRole tells the walk what to do with an item.
type itemRole int

const (
	// roleTake is a Staged item. An item with no record counts as Staged
	// in a snapshot whose snapshot object is Staged: a commit that
	// stopped before its records wrote it.
	roleTake itemRole = iota
	// roleDisc is an item that the state log names a disc for.
	roleDisc
	// roleUnknown is an item with no record in a snapshot whose snapshot
	// object a disc holds, for example below a missing disc. The walk
	// leaves it alone.
	roleUnknown
)

// role returns the role of id in the current snapshot.
func (w *planWalk) role(id object.ID) itemRole {
	rec, ok := w.log.Get(id)
	switch {
	case ok && rec.State.OnDisc():
		return roleDisc
	case ok || !w.underDisc:
		return roleTake
	}
	return roleUnknown
}

// descending reports whether the walk goes down into the trees and
// blobs of a disc.
func (w *planWalk) descending() bool { return len(w.want) > 0 }

// passes reports whether the walk leaves the item of role alone, and
// marks an item of a disc as seen then.
func (w *planWalk) passes(id object.ID, role itemRole) bool {
	switch {
	case role == roleUnknown:
		return true
	case role == roleDisc && !w.descending():
		w.seen[id] = true
		return true
	}
	return false
}

// emit appends u to the pack order.
func (w *planWalk) emit(u packUnit) {
	if !w.countOnly {
		w.units = append(w.units, u)
	}
}

// reach counts id as a Staged item that the current snapshot takes.
func (w *planWalk) reach(id object.ID) {
	w.count++
	if rec, ok := w.log.Get(id); ok && rec.State == stage.Staged {
		w.stagedReached++
	}
	delete(w.want, id)
}

// markBad records that the Staged item id cannot be taken, for cause.
func (w *planWalk) markBad(id object.ID, cause *UnreadableItem) *UnreadableItem {
	w.bad[id] = cause
	w.reach(id)
	return cause
}

// walk walks each snapshot of snaps, in their order.
func (w *planWalk) walk(snaps []snapInfo) {
	for i := range snaps {
		w.snapshot(&snaps[i])
	}
}

// snapshot walks one snapshot and appends its group. A snapshot whose
// snapshot object a disc holds is walked only while the walk looks for
// Staged items below the trees and blobs of a disc.
func (w *planWalk) snapshot(s *snapInfo) {
	w.count = 0
	w.underDisc = s.onDisc
	g := SnapshotGroup{ID: s.id, Time: s.time, OnDisc: s.onDisc}
	var cause *UnreadableItem
	switch {
	case s.unreadable != nil:
		cause = s.unreadable
		if !s.onDisc {
			w.markBad(s.id, cause)
		}
	case s.onDisc:
		if w.descending() {
			cause = w.tree(s.root)
		}
	default:
		if cause = w.tree(s.root); cause != nil {
			w.markBad(s.id, cause)
			break
		}
		w.seen[s.id] = true
		w.reach(s.id)
		var children []object.ID
		if w.role(s.root) == roleDisc {
			children = []object.ID{s.root}
		}
		w.emit(packUnit{
			ID: s.id, Kind: format.ObjectKindSnapshot, Children: children,
			Bytes: s.bytes, ByteLen: uint64(len(s.bytes)),
		})
	}
	if cause != nil {
		named := *cause
		named.Snapshot = s.id
		g.Unreadable = &named
	}
	g.StagedItems = w.count
	w.groups = append(w.groups, g)
}

// tree walks the tree id. It returns the unreadable item that keeps the
// tree from being taken, or nil.
func (w *planWalk) tree(id object.ID) *UnreadableItem {
	if cause := w.bad[id]; cause != nil {
		return cause
	}
	role := w.role(id)
	if w.seen[id] || w.passes(id, role) {
		return nil
	}
	data, bad := readChecked(w.objectPath, id, format.ObjectKindTree)
	var tree format.Tree
	if bad == nil {
		if _, err := tree.Decode(data); err != nil {
			bad = &UnreadableItem{ID: id, Kind: format.ObjectKindTree, Path: w.objectPath(format.ObjectKindTree, id)}
		}
	}
	if bad != nil {
		if role == roleDisc {
			// The walk cannot go down into this tree. The orphan pass
			// takes what it would find there.
			w.seen[id] = true
			return nil
		}
		return w.markBad(id, bad)
	}
	var children []object.ID
	var first *UnreadableItem
	for _, entry := range tree.Entries {
		child := object.ID(entry.ContentID)
		var cause *UnreadableItem
		switch entry.EntryType {
		case format.EntryTypeDirectory:
			cause = w.tree(child)
		case format.EntryTypeRegular:
			cause = w.blob(child)
		default:
			continue
		}
		if first == nil {
			first = cause
		}
		if w.role(child) == roleDisc {
			children = append(children, child)
		}
	}
	if role == roleDisc {
		w.seen[id] = true
		return first
	}
	if first != nil {
		return w.markBad(id, first)
	}
	w.seen[id] = true
	w.reach(id)
	w.emit(packUnit{ID: id, Kind: format.ObjectKindTree, Children: children, ByteLen: uint64(len(data))})
	return nil
}

// blob walks the blob id and its chunks, the chunks first. It returns
// the unreadable item that keeps the blob from being taken, or nil. A
// chunk file is never read here: the copy into the disc root checks it.
func (w *planWalk) blob(id object.ID) *UnreadableItem {
	if cause := w.bad[id]; cause != nil {
		return cause
	}
	role := w.role(id)
	if w.seen[id] || w.passes(id, role) {
		return nil
	}
	data, bad := readChecked(w.objectPath, id, format.ObjectKindBlob)
	var blob format.Blob
	if bad == nil {
		if _, err := blob.Decode(data); err != nil {
			bad = &UnreadableItem{ID: id, Kind: format.ObjectKindBlob, Path: w.objectPath(format.ObjectKindBlob, id)}
		}
	}
	if bad != nil {
		if role == roleDisc {
			w.seen[id] = true
			return nil
		}
		return w.markBad(id, bad)
	}
	var children []object.ID
	var first *UnreadableItem
	for _, e := range blob.Entries {
		chunkID := object.ID(e.ContentID)
		if cause := w.chunk(chunkID); first == nil {
			first = cause
		}
		if w.role(chunkID) == roleDisc {
			children = append(children, chunkID)
		}
	}
	if role == roleDisc {
		w.seen[id] = true
		return first
	}
	if first != nil {
		return w.markBad(id, first)
	}
	w.seen[id] = true
	w.reach(id)
	w.emit(packUnit{ID: id, Kind: format.ObjectKindBlob, Children: children, ByteLen: uint64(len(data))})
	return nil
}

// chunk takes the chunk id, unless a disc holds it or it is damaged.
func (w *planWalk) chunk(id object.ID) *UnreadableItem {
	if cause := w.bad[id]; cause != nil {
		return cause
	}
	if w.seen[id] {
		return nil
	}
	switch w.role(id) {
	case roleDisc:
		w.seen[id] = true
		return nil
	case roleUnknown:
		return nil
	}
	if w.damaged[id] {
		path := w.objectPath(format.ObjectKindChunk, id)
		_, err := os.Lstat(path)
		return w.markBad(id, &UnreadableItem{ID: id, Kind: format.ObjectKindChunk, Path: path, Missing: errors.Is(err, fs.ErrNotExist)})
	}
	w.seen[id] = true
	w.reach(id)
	w.emit(packUnit{ID: id, Kind: format.ObjectKindChunk})
	return nil
}

// orphanKinds is the order in which the orphan pass looks for the file
// of an item. The state log does not hold the kind of an item.
var orphanKinds = []format.ObjectKind{format.ObjectKindChunk, format.ObjectKindBlob, format.ObjectKindTree}

// orphans takes each item of want that no snapshot reaches, in the order
// of the id bytes. It finds the kind of an item from the file that
// exists. A tree or a blob takes its Staged children first.
func (w *planWalk) orphans() {
	ids := make([]object.ID, 0, len(w.want))
	for id := range w.want {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return lessBytes(ids[i][:], ids[j][:]) })
	w.want = nil
	w.underDisc = false
	reported := make(map[object.ID]bool)
	for _, id := range ids {
		if w.seen[id] || w.bad[id] != nil {
			continue
		}
		var cause *UnreadableItem
		switch w.orphanKind(id) {
		case format.ObjectKindChunk:
			cause = w.chunk(id)
		case format.ObjectKindBlob:
			cause = w.blob(id)
		case format.ObjectKindTree:
			cause = w.tree(id)
		default:
			cause = w.markBad(id, &UnreadableItem{ID: id, Missing: true})
		}
		if cause != nil && !reported[cause.ID] {
			reported[cause.ID] = true
			w.orphanItems = append(w.orphanItems, *cause)
		}
	}
}

// orphanKind returns the kind of the first file of id that exists, or 0.
func (w *planWalk) orphanKind(id object.ID) format.ObjectKind {
	for _, kind := range orphanKinds {
		if _, err := os.Lstat(w.objectPath(kind, id)); err == nil {
			return kind
		}
	}
	return 0
}
