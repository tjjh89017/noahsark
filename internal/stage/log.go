// Package stage implements the Phase 1 part of the staging state
// machine: an object is STAGED after commit, and PACKED once a run
// includes it. It records, for a packed object, which disc and which run
// hold it, so a later pack can list it as a Prereqs row instead of
// copying it again.
//
// Reading: OPERATIONS.md's staging state machine names the states and
// the transitions but gives no byte layout for the state log. This
// package picks the simplest deterministic layout: a fixed-width,
// append-only record, CRC-32C checked, so a reader can replay it and
// stop cleanly at a truncated tail. See docs/decisions.md, "4. Staging
// state machine".
package stage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/object"
)

// State is one object's position in the Phase 1 staging state machine.
type State uint8

const (
	// Staged is an object's state right after commit.
	Staged State = 1
	// Packed is an object's state once a run includes it.
	Packed State = 2
)

// recordLen is the fixed size of one state.db record: sequence (8),
// content id (32), state (1), run_seq (8), disc_uuid (16), reason (1),
// crc32c (4).
const recordLen = 8 + 32 + 1 + 8 + 16 + 1 + 4

// stateFileName is the state log's file name inside a staging directory,
// matching OPERATIONS.md's staging store layout.
const stateFileName = "state.db"

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// Record is one state.db record: an object's state as of Sequence, and,
// for a Packed record, the run and disc that hold it.
type Record struct {
	Sequence  uint64
	ContentID object.ID
	State     State
	RunSeq    uint64
	DiscUUID  [16]byte
	Reason    uint8
}

func (r *Record) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], r.Sequence)
	copy(buf[8:40], r.ContentID[:])
	buf[40] = byte(r.State)
	binary.LittleEndian.PutUint64(buf[41:49], r.RunSeq)
	copy(buf[49:65], r.DiscUUID[:])
	buf[65] = r.Reason
	crc := crc32.Checksum(buf[0:66], crc32cTable)
	binary.LittleEndian.PutUint32(buf[66:70], crc)
}

// decode reads one record from buf, which must be exactly recordLen
// bytes, and reports whether its CRC checks out.
func decodeRecord(buf []byte) (Record, bool) {
	var r Record
	r.Sequence = binary.LittleEndian.Uint64(buf[0:8])
	copy(r.ContentID[:], buf[8:40])
	r.State = State(buf[40])
	r.RunSeq = binary.LittleEndian.Uint64(buf[41:49])
	copy(r.DiscUUID[:], buf[49:65])
	r.Reason = buf[65]
	crc := binary.LittleEndian.Uint32(buf[66:70])
	ok := crc == crc32.Checksum(buf[0:66], crc32cTable)
	return r, ok
}

// Log is one repository's staging state log: the replayed current state
// of every object it has seen, plus the open file new records append to.
type Log struct {
	path    string
	current map[object.ID]Record
	nextSeq uint64
}

// Open reads and replays stagingDir's state.db, if one exists, and
// returns a Log ready to query and append to. A missing file is an empty
// log, matching a fresh repository. A record with a bad CRC ends the
// replay; records after it are ignored, matching a crash during an
// append.
func Open(stagingDir string) (*Log, error) {
	l := &Log{
		path:    filepath.Join(stagingDir, stateFileName),
		current: make(map[object.ID]Record),
		nextSeq: 1,
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, fmt.Errorf("stage: %w", err)
	}
	for off := 0; off+recordLen <= len(data); off += recordLen {
		rec, ok := decodeRecord(data[off : off+recordLen])
		if !ok {
			break
		}
		l.current[rec.ContentID] = rec
		if rec.Sequence >= l.nextSeq {
			l.nextSeq = rec.Sequence + 1
		}
	}
	return l, nil
}

// Get returns the current record for id and whether one exists. An id
// with no record has never been staged.
func (l *Log) Get(id object.ID) (Record, bool) {
	rec, ok := l.current[id]
	return rec, ok
}

// EnsureStaged appends a Staged record for id if it has no record yet.
// It does nothing when id is already known, so a re-commit of the same
// content never resets a Packed object back to Staged.
func (l *Log) EnsureStaged(id object.ID) error {
	if _, ok := l.current[id]; ok {
		return nil
	}
	return l.append(Record{ContentID: id, State: Staged})
}

// MarkPacked appends a Packed record for id, naming the run and disc
// that now hold it.
func (l *Log) MarkPacked(id object.ID, runSeq uint64, discUUID [16]byte) error {
	return l.append(Record{ContentID: id, State: Packed, RunSeq: runSeq, DiscUUID: discUUID})
}

// EnsurePacked appends a Packed record for id, naming runSeq and
// discUUID, unless id's current record already carries that exact run
// and disc. This makes repeated rebuilding from the same discs
// idempotent: it never grows the log when nothing has changed.
func (l *Log) EnsurePacked(id object.ID, runSeq uint64, discUUID [16]byte) error {
	if rec, ok := l.current[id]; ok && rec.State == Packed && rec.RunSeq == runSeq && rec.DiscUUID == discUUID {
		return nil
	}
	return l.MarkPacked(id, runSeq, discUUID)
}

// CountState returns the number of distinct objects whose current state
// is state.
func (l *Log) CountState(state State) int {
	n := 0
	for _, rec := range l.current {
		if rec.State == state {
			n++
		}
	}
	return n
}

// IDsInState returns every object id whose current state is state, in
// no particular order.
func (l *Log) IDsInState(state State) []object.ID {
	var ids []object.ID
	for id, rec := range l.current {
		if rec.State == state {
			ids = append(ids, id)
		}
	}
	return ids
}

// PackedCountByDisc returns, for every disc uuid the log has a Packed
// record for, the number of distinct objects currently Packed onto it.
func (l *Log) PackedCountByDisc() map[[16]byte]int {
	counts := make(map[[16]byte]int)
	for _, rec := range l.current {
		if rec.State == Packed {
			counts[rec.DiscUUID]++
		}
	}
	return counts
}

// append writes one record to state.db and updates the replayed state.
func (l *Log) append(rec Record) error {
	rec.Sequence = l.nextSeq
	buf := make([]byte, recordLen)
	rec.encode(buf)

	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("stage: %w", err)
	}

	l.current[rec.ContentID] = rec
	l.nextSeq++
	return nil
}
