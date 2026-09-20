// Package stage implements the staging state machine: an object is
// STAGED after commit, PACKED once a run includes it, BURNED once that
// run has been written to a disc, CLEAN once a read-back verify has
// checked it, GC-ELIGIBLE once it has stayed CLEAN for the retention
// period, and DELETED once gc has freed its bytes.
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
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tjjh89017/noahsark/internal/object"
)

// State is one object's position in the staging state machine.
type State uint8

const (
	// Staged is an object's state right after commit.
	Staged State = 1
	// Packed is an object's state once a run includes it.
	Packed State = 2
	// Burned is an object's state once the run that holds it has been
	// written to a disc.
	Burned State = 3
	// Clean is an object's state once a read-back verify of the disc
	// that holds it has checked out.
	Clean State = 4
	// GCEligible is an object's state once it has stayed Clean for at
	// least staging.retain_after_clean.
	GCEligible State = 5
	// Deleted is an object's state once gc has freed its staging bytes.
	Deleted State = 6
)

// OnDisc reports whether an object in this state already has its data
// written to some disc. Packed, Burned, Clean, GCEligible, and Deleted
// all name an object a disc holds; only Staged does not. A caller asking
// "is this object already on a disc" must use this, not a direct
// comparison against Packed, so pack never copies or rebinds an object
// a disc already holds.
func (s State) OnDisc() bool {
	switch s {
	case Packed, Burned, Clean, GCEligible, Deleted:
		return true
	default:
		return false
	}
}

// Reason is why a record's transition happened.
type Reason uint8

const (
	// ReasonNormal is an ordinary transition.
	ReasonNormal Reason = 0
	// ReasonBurnFailed marks a run's objects returned from Packed to
	// Staged after a failed burn.
	ReasonBurnFailed Reason = 1
	// ReasonVerifyFailed marks a run's objects returned from Burned to
	// Packed after a failed verify.
	ReasonVerifyFailed Reason = 2
	// ReasonHealed marks an object a heal reconstructed, entering the
	// machine again at Staged.
	ReasonHealed Reason = 3
	// ReasonDuplicateLocality marks an object staged again to place a
	// second, deliberate copy for locality.
	ReasonDuplicateLocality Reason = 4
)

// recordLen is the fixed size of one state.db record: sequence (8),
// content id (32), state (1), run_seq (8), disc_uuid (16), reason (1),
// crc32c (4).
const recordLen = 8 + 32 + 1 + 8 + 16 + 1 + 4

// stateFileName is the state log's file name inside a staging directory,
// matching OPERATIONS.md's staging store layout.
const stateFileName = "state.db"

// cleanTimeFileName is the clean time companion log's file name inside
// a staging directory. The state.db record has no timestamp field, so
// this package records a Clean transition's wall time in a second,
// append-only file instead: content id (32), unix nanoseconds (8),
// crc32c (4), one record per Clean transition. A reader replays it the
// same way it replays state.db, and the newest record per content id is
// that object's clean time.
const cleanTimeFileName = "clean_times.db"

// cleanTimeRecordLen is the fixed size of one clean_times.db record.
const cleanTimeRecordLen = 32 + 8 + 4

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// Record is one state.db record: an object's state as of Sequence, and,
// for a Packed record, the run and disc that hold it.
type Record struct {
	Sequence  uint64
	ContentID object.ID
	State     State
	RunSeq    uint64
	DiscUUID  [16]byte
	Reason    Reason
}

func (r *Record) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], r.Sequence)
	copy(buf[8:40], r.ContentID[:])
	buf[40] = byte(r.State)
	binary.LittleEndian.PutUint64(buf[41:49], r.RunSeq)
	copy(buf[49:65], r.DiscUUID[:])
	buf[65] = byte(r.Reason)
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
	r.Reason = Reason(buf[65])
	crc := binary.LittleEndian.Uint32(buf[66:70])
	ok := crc == crc32.Checksum(buf[0:66], crc32cTable)
	return r, ok
}

// tailState is one companion file's replay outcome: whether replay
// stopped at a bad CRC before reaching the end of the file, and how far
// short of the end it stopped. A writer that appends to this file must
// first cut off that torn tail (fixTornTail), so a new, good record
// never lands past bytes replay will always stop before and so never
// reach.
type tailState struct {
	validLen     int64
	truncated    bool
	ignoredBytes int64
	fixed        bool
}

// Log is one repository's staging state log: the replayed current state
// of every object it has seen, plus the open file new records append to.
type Log struct {
	path      string
	current   map[object.ID]Record
	nextSeq   uint64
	stateTail tailState

	cleanPath string
	cleanAt   map[object.ID]time.Time
	cleanTail tailState
}

// Truncated reports whether Open's replay of state.db stopped at a bad
// CRC before reaching the file's end, and how many trailing bytes it
// ignored. OPERATIONS.md requires the tool to report a truncated log;
// every command that opens the log checks this and prints that warning.
func (l *Log) Truncated() (truncated bool, ignoredBytes int64) {
	return l.stateTail.truncated, l.stateTail.ignoredBytes
}

// Open reads and replays stagingDir's state.db, if one exists, and
// returns a Log ready to query and append to. A missing file is an empty
// log, matching a fresh repository. A record with a bad CRC ends the
// replay; records after it are ignored, matching a crash during an
// append.
func Open(stagingDir string) (*Log, error) {
	l := &Log{
		path:      filepath.Join(stagingDir, stateFileName),
		current:   make(map[object.ID]Record),
		nextSeq:   1,
		cleanPath: filepath.Join(stagingDir, cleanTimeFileName),
		cleanAt:   make(map[object.ID]time.Time),
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stage: %w", err)
		}
	} else {
		off := 0
		for ; off+recordLen <= len(data); off += recordLen {
			rec, ok := decodeRecord(data[off : off+recordLen])
			if !ok {
				break
			}
			l.current[rec.ContentID] = rec
			if rec.Sequence >= l.nextSeq {
				l.nextSeq = rec.Sequence + 1
			}
		}
		l.stateTail.validLen = int64(off)
		if off < len(data) {
			l.stateTail.truncated = true
			l.stateTail.ignoredBytes = int64(len(data) - off)
		}
	}
	if err := l.loadCleanTimes(); err != nil {
		return nil, err
	}
	return l, nil
}

// fixTornTail cuts path back to tail.validLen when Open found a torn
// tail past the last valid record it replayed, so the append that
// follows lands right after the newest record replay can actually
// reach, instead of after garbage no replay will ever get past. It does
// nothing once it has already fixed this Log's own view of path, and
// nothing at all when Open found no torn tail.
func fixTornTail(path string, tail *tailState) error {
	if tail.fixed {
		return nil
	}
	tail.fixed = true
	if !tail.truncated {
		return nil
	}
	if err := os.Truncate(path, tail.validLen); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	tail.truncated = false
	return nil
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
// and disc, or the object has already moved past Packed. A disc's own
// catalog only ever says an object was packed onto it; replaying that
// catalog through rebuild-cache must never undo progress a verify or a
// gc already recorded, so a record already Burned, Clean, GCEligible or
// Deleted is left as it is. This also makes repeated rebuilding from the
// same discs idempotent: it never grows the log when nothing has
// changed.
func (l *Log) EnsurePacked(id object.ID, runSeq uint64, discUUID [16]byte) error {
	if rec, ok := l.current[id]; ok {
		switch rec.State {
		case Packed:
			if rec.RunSeq == runSeq && rec.DiscUUID == discUUID {
				return nil
			}
		case Burned, Clean, GCEligible, Deleted:
			return nil
		}
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
	return l.CountByDiscInState(Packed)
}

// CleanCountByDisc returns, for every disc uuid the log has a Clean
// record for, the number of distinct objects currently Clean on it.
func (l *Log) CleanCountByDisc() map[[16]byte]int {
	return l.CountByDiscInState(Clean)
}

// CountByDiscInState returns, for every disc uuid the log has a record
// for at state, the number of distinct objects currently at state on
// that disc.
func (l *Log) CountByDiscInState(state State) map[[16]byte]int {
	counts := make(map[[16]byte]int)
	for _, rec := range l.current {
		if rec.State == state {
			counts[rec.DiscUUID]++
		}
	}
	return counts
}

// OnDiscCountByDisc returns, for every disc uuid the log has an on-disc
// record for, the number of distinct objects the log currently places
// on that disc: Packed, Burned, Clean, GCEligible, or Deleted.
func (l *Log) OnDiscCountByDisc() map[[16]byte]int {
	counts := make(map[[16]byte]int)
	for _, rec := range l.current {
		if rec.State.OnDisc() {
			counts[rec.DiscUUID]++
		}
	}
	return counts
}

// MarkBurned appends a Burned record for id, naming the run and disc
// its data now lives on. It is the transition from Packed to Burned,
// once the run has been written to a disc.
func (l *Log) MarkBurned(id object.ID, runSeq uint64, discUUID [16]byte) error {
	return l.append(Record{ContentID: id, State: Burned, RunSeq: runSeq, DiscUUID: discUUID})
}

// MarkClean appends a Clean record for id, carrying forward its current
// run and disc, and records the time of this transition in the clean
// time companion log. verify is the only caller: this is the Burned to
// Clean transition, and OPERATIONS.md gives it no timer and no manual
// override.
func (l *Log) MarkClean(id object.ID) error {
	rec := l.current[id]
	rec.ContentID = id
	rec.State = Clean
	rec.Reason = ReasonNormal
	if err := l.append(rec); err != nil {
		return err
	}
	return l.recordCleanTime(id, time.Now())
}

// MarkVerifyFailed appends a Packed record for id with ReasonVerifyFailed,
// carrying forward its current run and disc. This is the Burned to
// Packed transition a failed verify drives; the run stays named until
// the next pack withdraws it and returns its objects to Staged.
func (l *Log) MarkVerifyFailed(id object.ID) error {
	rec := l.current[id]
	rec.ContentID = id
	rec.State = Packed
	rec.Reason = ReasonVerifyFailed
	return l.append(rec)
}

// MarkBurnUndone appends a Packed record for id with ReasonBurnFailed,
// carrying forward its current run and disc. This is the Burned to
// Packed transition "disc burned --undo" drives, for a burn that
// turned out bad after it was marked burned.
func (l *Log) MarkBurnUndone(id object.ID) error {
	rec := l.current[id]
	rec.ContentID = id
	rec.State = Packed
	rec.Reason = ReasonBurnFailed
	return l.append(rec)
}

// MarkGCEligible appends a GCEligible record for id, carrying forward
// its current run and disc. gc calls this once an object has stayed
// Clean for at least staging.retain_after_clean, and then deletes the
// object's staging file. The record is therefore written durably: it
// must be on the disc before the bytes it accounts for go away.
func (l *Log) MarkGCEligible(id object.ID) error {
	rec := l.current[id]
	rec.ContentID = id
	rec.State = GCEligible
	rec.Reason = ReasonNormal
	return l.appendRecord(rec, true)
}

// MarkDeleted appends a Deleted record for id, carrying forward its
// current run and disc. gc calls this once it has freed the object's
// staging bytes.
func (l *Log) MarkDeleted(id object.ID) error {
	rec := l.current[id]
	rec.ContentID = id
	rec.State = Deleted
	rec.Reason = ReasonNormal
	return l.append(rec)
}

// CleanTime returns the time id last transitioned to Clean, and whether
// the clean time companion log has a record for it.
func (l *Log) CleanTime(id object.ID) (time.Time, bool) {
	t, ok := l.cleanAt[id]
	return t, ok
}

// FedDiscs reports whether the state log holds a current on-disc record
// (Packed, Burned, Clean, GCEligible or Deleted) naming discUUID as the
// disc that holds it. rebuild-cache calls EnsurePacked for every object
// of a disc's own catalog before asking this, and Pack always refuses to
// create a run with no objects ("nothing to pack: no staged object
// remains"), so every disc that was ever packed leaves at least one such
// record; there is no disc that is fed and yet leaves the state log
// empty for it.
func (l *Log) FedDiscs(discUUID [16]byte) bool {
	for _, rec := range l.current {
		if rec.State.OnDisc() && rec.DiscUUID == discUUID {
			return true
		}
	}
	return false
}

// appendCloser is the file-like value openAppend returns: enough to
// write one record, flush it to stable storage and close it. *os.File
// satisfies it.
type appendCloser interface {
	io.Writer
	Sync() error
	Close() error
}

// openAppend opens path for appending, creating it and its parent
// directory as needed. Tests replace it to inject a Close failure a
// record's writer must still surface.
var openAppend = func(path string) (appendCloser, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// writeRecord appends buf to path and reports whether it reached the
// operating system. A record is not written until Close succeeds: a
// buffered write can still be sitting in memory when Write returns, so
// both of state.db's appenders (append, recordCleanTime) go through this
// one helper, and neither may treat a record as written, or update its
// own in-memory state, until writeRecord itself returns nil.
//
// It does not flush the record to the disc. Commit writes one record
// per object, and a sync per object would dominate its cost; a lost
// tail there only replays as an object still STAGED, which the next
// pack heals. A caller that is about to destroy what the record
// describes must use writeRecordDurable instead.
func writeRecord(path string, buf []byte) error {
	return writeRecordTo(path, buf, false)
}

// writeRecordDurable is writeRecord, plus a flush to stable storage
// before the close. gc deletes a staged object's bytes only after its
// GC-ELIGIBLE record is durable: without the flush, a crash could take
// the record away and leave the bytes gone, an object the log still
// calls CLEAN with nothing behind it.
func writeRecordDurable(path string, buf []byte) error {
	return writeRecordTo(path, buf, true)
}

func writeRecordTo(path string, buf []byte, durable bool) error {
	f, err := openAppend(path)
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	_, writeErr := f.Write(buf)
	var syncErr error
	if durable && writeErr == nil {
		syncErr = f.Sync()
	}
	closeErr := f.Close()
	switch {
	case writeErr != nil:
		return fmt.Errorf("stage: %w", writeErr)
	case syncErr != nil:
		return fmt.Errorf("stage: %w", syncErr)
	case closeErr != nil:
		return fmt.Errorf("stage: %w", closeErr)
	}
	return nil
}

// append writes one record to state.db and updates the replayed state.
func (l *Log) append(rec Record) error {
	return l.appendRecord(rec, false)
}

// appendRecord writes one record to state.db and updates the replayed
// state. A durable record is flushed to the disc before it counts as
// written.
func (l *Log) appendRecord(rec Record, durable bool) error {
	if err := fixTornTail(l.path, &l.stateTail); err != nil {
		return err
	}

	rec.Sequence = l.nextSeq
	buf := make([]byte, recordLen)
	rec.encode(buf)

	write := writeRecord
	if durable {
		write = writeRecordDurable
	}
	if err := write(l.path, buf); err != nil {
		return err
	}

	l.current[rec.ContentID] = rec
	l.nextSeq++
	return nil
}

// loadCleanTimes reads and replays the clean time companion log, if one
// exists. A record with a bad CRC ends the replay, matching state.db's
// own truncated-tail rule.
func (l *Log) loadCleanTimes() error {
	data, err := os.ReadFile(l.cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stage: %w", err)
	}
	off := 0
	for ; off+cleanTimeRecordLen <= len(data); off += cleanTimeRecordLen {
		rec := data[off : off+cleanTimeRecordLen]
		crc := binary.LittleEndian.Uint32(rec[40:44])
		if crc != crc32.Checksum(rec[0:40], crc32cTable) {
			break
		}
		var id object.ID
		copy(id[:], rec[0:32])
		nanos := int64(binary.LittleEndian.Uint64(rec[32:40]))
		l.cleanAt[id] = time.Unix(0, nanos)
	}
	l.cleanTail.validLen = int64(off)
	if off < len(data) {
		l.cleanTail.truncated = true
		l.cleanTail.ignoredBytes = int64(len(data) - off)
	}
	return nil
}

// recordCleanTime appends one record to the clean time companion log
// and updates the in-memory record MarkClean and CleanTime share.
func (l *Log) recordCleanTime(id object.ID, when time.Time) error {
	if err := fixTornTail(l.cleanPath, &l.cleanTail); err != nil {
		return err
	}

	buf := make([]byte, cleanTimeRecordLen)
	copy(buf[0:32], id[:])
	binary.LittleEndian.PutUint64(buf[32:40], uint64(when.UnixNano()))
	crc := crc32.Checksum(buf[0:40], crc32cTable)
	binary.LittleEndian.PutUint32(buf[40:44], crc)

	if err := writeRecord(l.cleanPath, buf); err != nil {
		return err
	}

	l.cleanAt[id] = when
	return nil
}
