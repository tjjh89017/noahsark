// Package stage holds the two state logs of a repository: the item log
// state.db, one record for each change of an item, and the disc state
// log discstate.db, one event for each change of a disc. OPERATIONS.md,
// "Local file formats", gives the bytes. docs/states.md gives the state
// machines.
package stage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/tjjh89017/noahsark/internal/object"
)

// State is the stored state of an item.
type State uint8

// The stored states of an item.
const (
	Staged State = 1
	Packed State = 2
	OnDisc State = 3
	Lost   State = 4
)

var stateNames = map[State]string{
	Staged: "Staged",
	Packed: "Packed",
	OnDisc: "OnDisc",
	Lost:   "Lost",
}

// String returns the name of the state, or the code for an unknown
// state.
func (s State) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("state %d", uint8(s))
}

// OnDisc reports whether a record in state s names the disc that took
// the item: Packed, OnDisc or Lost. pack takes no such item again, and
// names that disc as a prerequisite of a later disc.
func (s State) OnDisc() bool {
	return s == Packed || s == OnDisc || s == Lost
}

// Reason is why a record was written.
type Reason uint8

// The reasons of an item record.
const (
	ReasonNormal     Reason = 0
	ReasonPackUndone Reason = 1
	ReasonDiscLost   Reason = 2
	ReasonLostUndone Reason = 3
)

// recordLen is the size of one item record.
const recordLen = 70

// stateFileName is the name of the item log in the state directory of a
// repository.
const stateFileName = "state.db"

// ErrItemTransition is the error for an item record that the current
// record of the item does not permit.
var ErrItemTransition = errors.New("item change not permitted")

// Record is one record of the item log.
type Record struct {
	Sequence  uint64
	ContentID object.ID
	State     State
	RunSeq    uint64
	DiscUUID  [16]byte
	Reason    Reason
}

// encode writes r into buf, which is recordLen bytes, and seals it with
// its CRC.
func (r *Record) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], r.Sequence)
	copy(buf[8:40], r.ContentID[:])
	buf[40] = byte(r.State)
	binary.LittleEndian.PutUint64(buf[41:49], r.RunSeq)
	copy(buf[49:65], r.DiscUUID[:])
	buf[65] = byte(r.Reason)
	sealRecord(buf[:recordLen])
}

// decodeRecord reads one record from buf, which is recordLen bytes. It
// does not check the CRC.
func decodeRecord(buf []byte) Record {
	return Record{
		Sequence:  binary.LittleEndian.Uint64(buf[0:8]),
		ContentID: object.ID(buf[8:40]),
		State:     State(buf[40]),
		RunSeq:    binary.LittleEndian.Uint64(buf[41:49]),
		DiscUUID:  [16]byte(buf[49:65]),
		Reason:    Reason(buf[65]),
	}
}

// check returns an error when the fields of r break the rules of the
// record: a known state and reason, no disc for Staged, and a disc for
// every other state.
func (r *Record) check() error {
	if _, ok := stateNames[r.State]; !ok {
		return fmt.Errorf("unknown state code %d", uint8(r.State))
	}
	if r.Reason > ReasonLostUndone {
		return fmt.Errorf("unknown reason code %d", uint8(r.Reason))
	}
	if r.State == Staged {
		if r.RunSeq != 0 || r.DiscUUID != [16]byte{} {
			return errors.New("a Staged record names a run or a disc")
		}
		return nil
	}
	if r.DiscUUID == [16]byte{} {
		return fmt.Errorf("a %s record names no disc", r.State)
	}
	return nil
}

// Log is the replayed item log of one repository: the newest record of
// each item.
type Log struct {
	file    *recFile
	current map[object.ID]Record
}

// Open reads and replays the item log in stateDir for a command that
// holds the repository lock. It cuts a torn tail; TornBytes reports the
// cut. A missing file is an empty log.
func Open(stateDir string) (*Log, error) {
	return openLog(stateDir, true)
}

// OpenReadOnly reads and replays the item log in stateDir for a command
// that takes no lock. It ignores a torn tail and never changes the file.
// Every append on the returned log fails.
func OpenReadOnly(stateDir string) (*Log, error) {
	return openLog(stateDir, false)
}

func openLog(stateDir string, writable bool) (*Log, error) {
	l := &Log{current: make(map[object.ID]Record)}
	f, err := openRecFile(filepath.Join(stateDir, stateFileName), recordLen, writable, func(buf []byte) error {
		rec := decodeRecord(buf)
		if err := rec.check(); err != nil {
			return err
		}
		l.current[rec.ContentID] = rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	l.file = f
	return l, nil
}

// TornBytes returns the size of the torn tail that the open found: cut
// by Open, ignored by OpenReadOnly. It is 0 for a clean log.
func (l *Log) TornBytes() int64 {
	return l.file.tornBytes
}

// Path returns the path of the item log file.
func (l *Log) Path() string {
	return l.file.path
}

// Get returns the newest record of id, and whether the log knows id.
func (l *Log) Get(id object.ID) (Record, bool) {
	rec, ok := l.current[id]
	return rec, ok
}

// appendRecords writes recs as one batch with one sync, and then makes
// them the newest records. It sets the sequence of each record. It
// checks every record first; when one breaks the rules, it writes
// nothing.
func (l *Log) appendRecords(recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	batch := make([]byte, len(recs)*recordLen)
	seq := l.file.nextSeq()
	for i := range recs {
		recs[i].Sequence = seq + uint64(i)
		if err := recs[i].check(); err != nil {
			return fmt.Errorf("stage: item %s: %w", recs[i].ContentID.TextForm(), err)
		}
		recs[i].encode(batch[i*recordLen : (i+1)*recordLen])
	}
	if err := l.file.appendBatch(batch); err != nil {
		return err
	}
	for _, rec := range recs {
		l.current[rec.ContentID] = rec
	}
	return nil
}

// change builds one record for each id from its current record, and
// appends them as one batch. from lists the states that permit the
// change; an item with no record permits it only when unknownOK is true.
// build gives the new record from the current one. Every id must permit
// the change, or change writes nothing and returns an error that wraps
// ErrItemTransition. An id that occurs more than once gets one record.
func (l *Log) change(ids []object.ID, from []State, unknownOK bool, build func(cur Record) Record) error {
	seen := make(map[object.ID]bool, len(ids))
	recs := make([]Record, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		cur, ok := l.current[id]
		switch {
		case !ok && !unknownOK:
			return fmt.Errorf("stage: item %s has no record: %w", id.TextForm(), ErrItemTransition)
		case ok && !slices.Contains(from, cur.State):
			return fmt.Errorf("stage: item %s is %s: %w", id.TextForm(), cur.State, ErrItemTransition)
		}
		cur.ContentID = id
		recs = append(recs, build(cur))
	}
	return l.appendRecords(recs)
}

// EnsureStaged is the rule of commit. It records each id that the log
// does not know, or that is Lost, as Staged. An id in another state
// keeps its record. The ids go to the log as one batch with one sync.
func (l *Log) EnsureStaged(ids ...object.ID) error {
	var todo []object.ID
	for _, id := range ids {
		if rec, ok := l.current[id]; !ok || rec.State == Lost {
			todo = append(todo, id)
		}
	}
	return l.change(todo, []State{Lost}, true, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: Staged}
	})
}

// MarkStaged records each id as Staged with reason: ReasonPackUndone for
// the items of an undone pack, and ReasonDiscLost for the Packed items of
// a lost disc. Each id must be Packed.
func (l *Log) MarkStaged(reason Reason, ids ...object.ID) error {
	return l.change(ids, []State{Packed}, false, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: Staged, Reason: reason}
	})
}

// MarkPackUndone records each id as Staged with ReasonPackUndone. Each
// id must be Packed.
func (l *Log) MarkPackUndone(ids ...object.ID) error {
	return l.MarkStaged(ReasonPackUndone, ids...)
}

// MarkPacked records each id as Packed on the disc discUUID, with the
// run runSeq. Each id must be Staged.
func (l *Log) MarkPacked(runSeq uint64, discUUID [16]byte, ids ...object.ID) error {
	return l.change(ids, []State{Staged}, false, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: Packed, RunSeq: runSeq, DiscUUID: discUUID}
	})
}

// MarkOnDisc records each id as OnDisc on the disc of its Packed record.
// gc calls it before it unlinks a chunk file. Each id must be Packed.
func (l *Log) MarkOnDisc(ids ...object.ID) error {
	return l.change(ids, []State{Packed}, false, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: OnDisc, RunSeq: cur.RunSeq, DiscUUID: cur.DiscUUID}
	})
}

// EnsureOnDisc is the rule of recover. It records each id that the log
// does not know, or that is Lost, as OnDisc on the disc discUUID, with
// the run runSeq. An id in another state keeps its record.
func (l *Log) EnsureOnDisc(runSeq uint64, discUUID [16]byte, ids ...object.ID) error {
	var todo []object.ID
	for _, id := range ids {
		if rec, ok := l.current[id]; !ok || rec.State == Lost {
			todo = append(todo, id)
		}
	}
	return l.change(todo, []State{Lost}, true, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: OnDisc, RunSeq: runSeq, DiscUUID: discUUID}
	})
}

// MarkLost records each id as Lost, with ReasonDiscLost, on the disc of
// its OnDisc record. Each id must be OnDisc.
func (l *Log) MarkLost(ids ...object.ID) error {
	return l.change(ids, []State{OnDisc}, false, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: Lost, RunSeq: cur.RunSeq, DiscUUID: cur.DiscUUID, Reason: ReasonDiscLost}
	})
}

// MarkLostUndone records each id as OnDisc, with ReasonLostUndone, on
// the disc of its Lost record. Each id must be Lost.
func (l *Log) MarkLostUndone(ids ...object.ID) error {
	return l.change(ids, []State{Lost}, false, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: OnDisc, RunSeq: cur.RunSeq, DiscUUID: cur.DiscUUID, Reason: ReasonLostUndone}
	})
}

// ReturnToDisc records each id as Packed, with ReasonLostUndone, on the
// disc discUUID with the run runSeq. Each id must be Staged.
func (l *Log) ReturnToDisc(runSeq uint64, discUUID [16]byte, ids ...object.ID) error {
	return l.change(ids, []State{Staged}, false, func(cur Record) Record {
		return Record{ContentID: cur.ContentID, State: Packed, RunSeq: runSeq, DiscUUID: discUUID, Reason: ReasonLostUndone}
	})
}

// sortedIDs returns ids sorted by their bytes.
func sortedIDs(ids []object.ID) []object.ID {
	slices.SortFunc(ids, func(a, b object.ID) int { return slices.Compare(a[:], b[:]) })
	return ids
}

// IDsInState returns every id whose newest record is in state, sorted by
// id bytes.
func (l *Log) IDsInState(state State) []object.ID {
	var ids []object.ID
	for id, rec := range l.current {
		if rec.State == state {
			ids = append(ids, id)
		}
	}
	return sortedIDs(ids)
}

// ItemsOfDisc returns every id whose newest record names the disc
// discUUID, in any state, sorted by id bytes.
func (l *Log) ItemsOfDisc(discUUID [16]byte) []object.ID {
	var ids []object.ID
	for id, rec := range l.current {
		if rec.State != Staged && rec.DiscUUID == discUUID {
			ids = append(ids, id)
		}
	}
	return sortedIDs(ids)
}

// ItemsOfDiscInState returns every id whose newest record is in state
// and names the disc discUUID, sorted by id bytes.
func (l *Log) ItemsOfDiscInState(discUUID [16]byte, state State) []object.ID {
	var ids []object.ID
	for id, rec := range l.current {
		if rec.State == state && rec.DiscUUID == discUUID {
			ids = append(ids, id)
		}
	}
	return sortedIDs(ids)
}

// CountState returns the number of items whose newest record is in
// state.
func (l *Log) CountState(state State) int {
	n := 0
	for _, rec := range l.current {
		if rec.State == state {
			n++
		}
	}
	return n
}

// CountOnDisc returns the number of items whose newest record names a
// disc: Packed, OnDisc or Lost.
func (l *Log) CountOnDisc() int {
	n := 0
	for _, rec := range l.current {
		if rec.State.OnDisc() {
			n++
		}
	}
	return n
}

// CountByDisc returns, for each disc, the number of items whose newest
// record is in state and names that disc.
func (l *Log) CountByDisc(state State) map[[16]byte]int {
	counts := make(map[[16]byte]int)
	for _, rec := range l.current {
		if rec.State == state && rec.State != Staged {
			counts[rec.DiscUUID]++
		}
	}
	return counts
}

// DiscsNamed returns every disc that a newest record names, sorted by
// uuid bytes.
func (l *Log) DiscsNamed() [][16]byte {
	set := make(map[[16]byte]bool)
	for _, rec := range l.current {
		if rec.State != Staged {
			set[rec.DiscUUID] = true
		}
	}
	return slices.SortedFunc(maps.Keys(set), func(a, b [16]byte) int { return slices.Compare(a[:], b[:]) })
}

// Totals returns the number of items whose newest record is in state,
// and the sum of their sizes. size gives the size of one item; an error
// from it stops the sum.
func (l *Log) Totals(state State, size func(id object.ID) (uint64, error)) (count int, bytes uint64, err error) {
	for _, id := range l.IDsInState(state) {
		n, err := size(id)
		if err != nil {
			return 0, 0, err
		}
		count++
		bytes += n
	}
	return count, bytes, nil
}

// ItemWord is the derived word of an item, as docs/states.md, "Item
// states", defines it. status never prints it.
type ItemWord string

// The derived item words.
const (
	WordStaged ItemWord = "staged"
	WordPacked ItemWord = "packed"
	WordBurned ItemWord = "burned"
	WordClean  ItemWord = "clean"
	WordOnDisc ItemWord = "on-disc"
	WordLost   ItemWord = "lost"
)

// Word returns the derived word of the item of rec. disc is the replayed
// record of the disc that rec names; Word reads it only for a Packed
// record. A Packed item takes its word from the state of its disc:
// burned for a burned disc, clean for a verified disc, and packed for
// every other state.
func Word(rec Record, disc DiscInfo) ItemWord {
	switch rec.State {
	case Staged:
		return WordStaged
	case OnDisc:
		return WordOnDisc
	case Lost:
		return WordLost
	}
	switch disc.State {
	case DiscBurned:
		return WordBurned
	case DiscVerified:
		return WordClean
	}
	return WordPacked
}
