package stage

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/tjjh89017/noahsark/internal/object"
)

// A command that changes a disc and its items writes the disc event
// first, as one synced append, and then the item records that the event
// asks for, as one synced batch. The event is the intent. A command that
// stops between the two leaves a disc whose items do not follow its
// state yet. Repairs finds each such disc, and Complete writes the
// missing item records.

// Repair is one disc whose item records do not follow its state.
type Repair struct {
	Disc DiscInfo
	// Command is the command that stopped: "disc lost", "disc lost
	// --undo", "pack --undo" or "gc".
	Command string
	// Items is the number of item records that the disc still needs.
	Items int
}

// IndexItems returns the items that the catalog INDEX of a disc lists,
// and the run number of that INDEX. It returns no item and no error when
// the catalog holds no INDEX of the disc.
type IndexItems func(disc [16]byte) ([]object.ID, uint64, error)

// CatalogHolds reports whether the catalog holds a good snapshot, tree or
// blob object with the id. pack reads such an object from the catalog,
// thus it needs no copy in the staging store and no disc.
type CatalogHolds func(id object.ID) bool

// Repairs returns each disc whose item records do not follow its state,
// sorted by disc number and then by uuid. It writes nothing. index reads
// the catalog INDEX of a disc that disc lost --undo gave back. holds
// tells which items the catalog holds; nil holds none. A nil holds
// changes the records of a repair, and can change the count of a repair
// of disc lost --undo, but never which discs need a repair.
func (s *Logs) Repairs(index IndexItems, holds CatalogHolds) ([]Repair, error) {
	plans, err := s.plan(nil, index, holds)
	if err != nil {
		return nil, err
	}
	out := make([]Repair, 0, len(plans))
	for _, p := range plans {
		out = append(out, p.Repair)
	}
	return out, nil
}

// Complete writes the missing item records of each disc that Repairs
// returns, one batch for each disc, and returns what it wrote.
func (s *Logs) Complete(index IndexItems, holds CatalogHolds) ([]Repair, error) {
	plans, err := s.plan(nil, index, holds)
	if err != nil {
		return nil, err
	}
	done := make([]Repair, 0, len(plans))
	for _, p := range plans {
		if err := s.Items.appendRecords(p.recs); err != nil {
			return done, err
		}
		done = append(done, p.Repair)
	}
	return done, nil
}

// CompleteDisc writes the item records that the state of the disc id asks
// for, as one batch, and returns their number. A command calls it right
// after it appends the event of the disc.
func (s *Logs) CompleteDisc(id [16]byte, index IndexItems, holds CatalogHolds) (int, error) {
	plans, err := s.plan(&id, index, holds)
	if err != nil || len(plans) == 0 {
		return 0, err
	}
	if err := s.Items.appendRecords(plans[0].recs); err != nil {
		return 0, err
	}
	return len(plans[0].recs), nil
}

// repairPlan is a Repair and the records that it writes.
type repairPlan struct {
	Repair
	recs []Record
}

// plan returns the repair of each disc, or of the disc only when only is
// not nil. It reads the item log in one pass.
//
// The item records that each disc state asks for:
//   - lost: each Packed item and each OnDisc item that the catalog holds
//     return to Staged, and each other OnDisc item is Lost, all with the
//     reason disc lost.
//   - undone: each Packed item returns to Staged, with the reason pack
//     undone.
//   - on disc only: each Packed item is OnDisc, as gc records it, and each
//     Lost item is OnDisc with the reason lost undone. While a Lost item
//     remains and the newest event is LostUndone, each item that the INDEX
//     of the disc lists, that the catalog holds, and that is Staged with
//     the reason disc lost, is OnDisc too, with the reason lost undone.
//   - burned, when the newest event is LostUndone and no item is Packed on
//     the disc: each Staged item that the INDEX of the disc lists is
//     Packed on the disc, with the reason lost undone.
func (s *Logs) plan(only *[16]byte, index IndexItems, holds CatalogHolds) ([]repairPlan, error) {
	plans := make(map[[16]byte]*repairPlan)
	packed := make(map[[16]byte]int)
	for id, rec := range s.Items.current {
		if rec.State == Staged || (only != nil && rec.DiscUUID != *only) {
			continue
		}
		if rec.State == Packed {
			packed[rec.DiscUUID]++
		}
		disc, ok := s.Discs.Disc(rec.DiscUUID)
		if !ok {
			continue
		}
		// holds reads a catalog file, thus only an OnDisc item of a lost
		// disc asks it.
		inCatalog := disc.State == DiscLost && rec.State == OnDisc && holds != nil && holds(id)
		next, cmd, ok := itemRepair(disc.State, rec, inCatalog)
		if !ok {
			continue
		}
		next.ContentID = id
		p := plans[rec.DiscUUID]
		if p == nil {
			p = &repairPlan{Disc: disc, Command: cmd}
			plans[rec.DiscUUID] = p
		}
		p.recs = append(p.recs, next)
	}

	for _, disc := range s.Discs.Discs() {
		if only != nil && disc.UUID != *only {
			continue
		}
		if p := plans[disc.UUID]; p != nil && p.Command == "disc lost --undo" && disc.State == DiscOnDiscOnly {
			recs, err := s.catalogObjectsBack(disc, index, holds)
			if err != nil {
				return nil, err
			}
			p.recs = append(p.recs, recs...)
			continue
		}
		if disc.State != DiscBurned || disc.LastEvent != EventLostUndone || packed[disc.UUID] > 0 || index == nil {
			continue
		}
		ids, runSeq, err := index(disc.UUID)
		if err != nil {
			return nil, fmt.Errorf("stage: disc lost --undo of disc %d: %w", disc.DiscSeq, err)
		}
		var recs []Record
		for _, id := range ids {
			if rec, ok := s.Items.current[id]; ok && rec.State == Staged {
				recs = append(recs, Record{ContentID: id, State: Packed, RunSeq: runSeq, DiscUUID: disc.UUID, Reason: ReasonLostUndone})
			}
		}
		if len(recs) > 0 {
			plans[disc.UUID] = &repairPlan{Disc: disc, Command: "disc lost --undo", recs: recs}
		}
	}

	out := make([]repairPlan, 0, len(plans))
	for _, p := range plans {
		slices.SortFunc(p.recs, func(a, b Record) int { return slices.Compare(a.ContentID[:], b.ContentID[:]) })
		p.recs = slices.CompactFunc(p.recs, func(a, b Record) bool { return a.ContentID == b.ContentID })
		p.Items = len(p.recs)
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b repairPlan) int {
		return cmp.Or(cmp.Compare(a.Disc.DiscSeq, b.Disc.DiscSeq), slices.Compare(a.Disc.UUID[:], b.Disc.UUID[:]))
	})
	return out, nil
}

// catalogObjectsBack returns the OnDisc records that disc lost --undo of
// an on disc only disc writes for the catalog objects that disc lost
// staged: each item that the catalog INDEX of the disc lists, that holds
// reports, and that is Staged with the reason disc lost.
func (s *Logs) catalogObjectsBack(disc DiscInfo, index IndexItems, holds CatalogHolds) ([]Record, error) {
	if index == nil || holds == nil {
		return nil, nil
	}
	ids, runSeq, err := index(disc.UUID)
	if err != nil {
		return nil, fmt.Errorf("stage: disc lost --undo of disc %d: %w", disc.DiscSeq, err)
	}
	var recs []Record
	for _, id := range ids {
		if rec, ok := s.Items.current[id]; ok && rec.State == Staged && rec.Reason == ReasonDiscLost && holds(id) {
			recs = append(recs, Record{ContentID: id, State: OnDisc, RunSeq: runSeq, DiscUUID: disc.UUID, Reason: ReasonLostUndone})
		}
	}
	return recs, nil
}

// itemRepair returns the record that an item whose newest record is rec
// needs on a disc in state, and the command that writes it. inCatalog
// tells that the catalog holds the item. ok is false when the record
// already follows the state.
func itemRepair(state DiscState, rec Record, inCatalog bool) (next Record, cmd string, ok bool) {
	switch {
	case state == DiscLost && rec.State == Packed, state == DiscLost && inCatalog:
		return Record{State: Staged, Reason: ReasonDiscLost}, "disc lost", true
	case state == DiscLost && rec.State == OnDisc:
		return Record{State: Lost, RunSeq: rec.RunSeq, DiscUUID: rec.DiscUUID, Reason: ReasonDiscLost}, "disc lost", true
	case state == DiscUndone && rec.State == Packed:
		return Record{State: Staged, Reason: ReasonPackUndone}, "pack --undo", true
	case state == DiscOnDiscOnly && rec.State == Packed:
		return Record{State: OnDisc, RunSeq: rec.RunSeq, DiscUUID: rec.DiscUUID}, "gc", true
	case state == DiscOnDiscOnly && rec.State == Lost:
		return Record{State: OnDisc, RunSeq: rec.RunSeq, DiscUUID: rec.DiscUUID, Reason: ReasonLostUndone}, "disc lost --undo", true
	}
	return Record{}, "", false
}
