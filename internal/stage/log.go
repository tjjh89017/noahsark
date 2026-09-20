// Package stage implements the staging state machine: an object is
// STAGED after commit, PACKED once a run includes it, BURNED once that
// run has been written to a disc, CLEAN once a read-back verify has
// checked it, and ON-DISC once a disc alone holds it and staging holds
// no file for it.
//
// The state log is a fixed-width, append-only file. Each record carries
// a CRC-32C, so a reader replays it and stops cleanly at a torn tail.
// See docs/decisions.md, "4. Staging state machine".
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
	// OnDiscOnly is an object's state once a disc alone holds it. gc
	// records it before it unlinks the staged file, and rebuild-cache
	// records it for every object it reads from a disc's own catalog.
	// It is terminal: the object needs no staging file any more.
	OnDiscOnly State = 5
)

// OnDisc reports whether an object in this state already has its data
// written to some disc. Packed, Burned, Clean and OnDiscOnly all name an
// object a disc holds; only Staged does not. A caller asking "is this
// object already on a disc" must use this, not a direct comparison
// against Packed, so pack never copies or rebinds an object a disc
// already holds.
func (s State) OnDisc() bool {
	switch s {
	case Packed, Burned, Clean, OnDiscOnly:
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
// verify_count (1), clean_sec (8), crc32c (4).
const recordLen = 8 + 32 + 1 + 8 + 16 + 1 + 1 + 8 + 4

// maxVerifyCount is the largest value VerifyCount holds. A further
// verify keeps the count there instead of wrapping to zero.
const maxVerifyCount = 255

// stateFileName is the state log's file name inside a staging directory,
// matching OPERATIONS.md's staging store layout.
const stateFileName = "state.db"

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// Record is one state.db record: an object's state as of Sequence, and,
// for a Packed record or a later one, the run and disc that hold it.
type Record struct {
	Sequence  uint64
	ContentID object.ID
	State     State
	RunSeq    uint64
	DiscUUID  [16]byte
	Reason    Reason
	// VerifyCount is how many successful verifies the object has had.
	// gc deletes an object only at gc.min_verified_copies verifies, so
	// the staged bytes stay until the second identical disc passes
	// verify.
	VerifyCount uint8
	// CleanSec is the unix time of the first successful verify, and 0
	// before that verify. Every later record carries it forward, so the
	// retention period counts from the first verify and a second verify
	// never restarts it.
	CleanSec int64
}

func (r *Record) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], r.Sequence)
	copy(buf[8:40], r.ContentID[:])
	buf[40] = byte(r.State)
	binary.LittleEndian.PutUint64(buf[41:49], r.RunSeq)
	copy(buf[49:65], r.DiscUUID[:])
	buf[65] = byte(r.Reason)
	buf[66] = r.VerifyCount
	binary.LittleEndian.PutUint64(buf[67:75], uint64(r.CleanSec))
	crc := crc32.Checksum(buf[0:75], crc32cTable)
	binary.LittleEndian.PutUint32(buf[75:79], crc)
}

// decodeRecord reads one record from buf, which must be exactly
// recordLen bytes, and reports whether its CRC checks out.
func decodeRecord(buf []byte) (Record, bool) {
	var r Record
	r.Sequence = binary.LittleEndian.Uint64(buf[0:8])
	copy(r.ContentID[:], buf[8:40])
	r.State = State(buf[40])
	r.RunSeq = binary.LittleEndian.Uint64(buf[41:49])
	copy(r.DiscUUID[:], buf[49:65])
	r.Reason = Reason(buf[65])
	r.VerifyCount = buf[66]
	r.CleanSec = int64(binary.LittleEndian.Uint64(buf[67:75]))
	crc := binary.LittleEndian.Uint32(buf[75:79])
	ok := crc == crc32.Checksum(buf[0:75], crc32cTable)
	return r, ok
}

// Log is one repository's staging state log: the replayed current state
// of every object it has seen, plus the file new records append to.
type Log struct {
	path         string
	current      map[object.ID]Record
	nextSeq      uint64
	truncated    bool
	ignoredBytes int64
}

// Truncated reports whether Open cut a torn tail off the state log, and
// how many bytes it cut. Every command that opens the log checks this
// and prints one warning.
func (l *Log) Truncated() (truncated bool, ignoredBytes int64) {
	return l.truncated, l.ignoredBytes
}

// Open reads and replays stagingDir's state.db, if one exists, and
// returns a Log ready to query and append to. A missing file is an empty
// log, matching a fresh repository.
//
// Open applies one torn-tail rule. A partial record at the end, or a
// last record with a bad CRC, is what a crash during an append leaves:
// Open cuts the file back to the last good record and reports the cut
// through Truncated. A bad record anywhere else is damage, not a torn
// tail, because good records follow it. Open returns an error there and
// changes nothing, so no command silently drops the good records behind
// the damage.
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

	full := len(data) / recordLen
	recs := make([]Record, 0, full)
	for i := range full {
		off := i * recordLen
		rec, ok := decodeRecord(data[off : off+recordLen])
		if !ok {
			if i != full-1 {
				return nil, fmt.Errorf("stage: %s: record %d of %d has a bad CRC; the log is damaged", l.path, i+1, full)
			}
			break
		}
		recs = append(recs, rec)
	}

	for _, rec := range recs {
		l.current[rec.ContentID] = rec
		if rec.Sequence >= l.nextSeq {
			l.nextSeq = rec.Sequence + 1
		}
	}

	validLen := int64(len(recs) * recordLen)
	if validLen != int64(len(data)) {
		l.truncated = true
		l.ignoredBytes = int64(len(data)) - validLen
		if err := os.Truncate(l.path, validLen); err != nil {
			return nil, fmt.Errorf("stage: %w", err)
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

// EnsureOnDisc appends an OnDiscOnly record for id, naming the run and
// disc that hold it, unless the log already has a record for id.
// rebuild-cache is the only caller: it reads a disc's own catalog into a
// repository whose staging is empty, so the objects of that disc need no
// staging file and no further burn or verify. An object the log already
// knows keeps its own state, because that state says more than a disc
// catalog can. A repeat rebuild from the same discs therefore appends
// nothing.
func (l *Log) EnsureOnDisc(id object.ID, runSeq uint64, discUUID [16]byte) error {
	if _, ok := l.current[id]; ok {
		return nil
	}
	return l.appendRecord(Record{ContentID: id, State: OnDiscOnly, RunSeq: runSeq, DiscUUID: discUUID}, false)
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

// CountOnDisc returns the number of distinct objects the log places on
// some disc, whatever their state.
func (l *Log) CountOnDisc() int {
	n := 0
	for _, rec := range l.current {
		if rec.State.OnDisc() {
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

// MinCleanVerifyCountByDisc returns, for every disc uuid the log has a
// Clean record for, the lowest verify count of the objects currently
// Clean on it. That lowest count is what gc acts on, so it is what "disc
// list" reports for the disc.
func (l *Log) MinCleanVerifyCountByDisc() map[[16]byte]uint8 {
	counts := make(map[[16]byte]uint8)
	for _, rec := range l.current {
		if rec.State != Clean {
			continue
		}
		if lowest, ok := counts[rec.DiscUUID]; ok && lowest <= rec.VerifyCount {
			continue
		}
		counts[rec.DiscUUID] = rec.VerifyCount
	}
	return counts
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
// on that disc.
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

// MarkVerified appends a Clean record for id, carrying forward its
// current run and disc, and adds 1 to its verify count. verify is the
// only caller. It marks the Burned to Clean transition, and it also
// marks a verify of an object that is already Clean: the operator
// verifies the second identical disc that way, and the count is what
// tells gc that both copies are readable.
//
// The clean time is set once, at the first verify, so the retention
// period counts from the first verify and a later verify never restarts
// it.
func (l *Log) MarkVerified(id object.ID) error {
	rec := l.current[id]
	rec.ContentID = id
	rec.State = Clean
	rec.Reason = ReasonNormal
	if rec.VerifyCount < maxVerifyCount {
		rec.VerifyCount++
	}
	if rec.CleanSec == 0 {
		rec.CleanSec = time.Now().Unix()
	}
	return l.append(rec)
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

// MarkOnDisc appends an OnDiscOnly record for id, carrying forward its
// current run, disc, verify count and clean time. gc calls it for a
// CLEAN object it is about to free, and then unlinks the staged file.
// The record is flushed to stable storage before it counts as written:
// it must be on the disk before the bytes it accounts for go away.
func (l *Log) MarkOnDisc(id object.ID) error {
	rec := l.current[id]
	rec.ContentID = id
	rec.State = OnDiscOnly
	rec.Reason = ReasonNormal
	return l.appendRecord(rec, true)
}

// CleanTime returns the time id first passed verify, and whether it has
// passed one at all.
func (l *Log) CleanTime(id object.ID) (time.Time, bool) {
	rec, ok := l.current[id]
	if !ok || rec.CleanSec == 0 {
		return time.Time{}, false
	}
	return time.Unix(rec.CleanSec, 0), true
}

// FedDiscs reports whether the state log holds a current on-disc record
// naming discUUID as the disc that holds it. rebuild-cache records every
// object of a disc's own catalog before asking this, and pack always
// refuses to create a run with no objects, so every disc that was ever
// packed leaves at least one such record.
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
// directory as needed. Tests replace it to check that a durable append
// flushes before it closes, and that a Close failure reaches the caller.
var openAppend = func(path string) (appendCloser, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// writeRecord appends buf to path and reports whether it reached the
// operating system. A record is not written until Close succeeds: a
// buffered write can still be sitting in memory when Write returns, so
// no caller may treat a record as written, or update its own in-memory
// state, until writeRecord returns nil.
//
// A durable write also flushes the record to the disk. gc unlinks a
// staged object's bytes only after its ON-DISC record is durable:
// without the flush, a crash could take the record away and leave the
// bytes gone, an object the log still calls CLEAN with nothing behind
// it. No other append flushes, because commit writes one record per
// object and a flush per object would set its pace; a lost tail there
// only replays as an object still STAGED, which the next pack heals.
func writeRecord(path string, buf []byte, durable bool) error {
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
// state. A durable record is flushed to the disk before it counts as
// written.
func (l *Log) appendRecord(rec Record, durable bool) error {
	rec.Sequence = l.nextSeq
	buf := make([]byte, recordLen)
	rec.encode(buf)

	if err := writeRecord(l.path, buf, durable); err != nil {
		return err
	}

	l.current[rec.ContentID] = rec
	l.nextSeq++
	return nil
}
