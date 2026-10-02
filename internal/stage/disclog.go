package stage

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"
	"uuid"
)

// discRecordLen is the size of one disc state log record.
const discRecordLen = 54

// discStateFileName is the name of the disc state log in the state
// directory of a repository.
const discStateFileName = "discstate.db"

// DiscEvent is the event code of a disc state log record.
type DiscEvent uint8

// The event codes of the disc state log.
const (
	EventPacked         DiscEvent = 1
	EventPackUndone     DiscEvent = 2
	EventBurnRecorded   DiscEvent = 3
	EventBurnRemoved    DiscEvent = 4
	EventCheckOK        DiscEvent = 5
	EventCheckFailed    DiscEvent = 6
	EventMarkedVerified DiscEvent = 7
	EventVerifyUndone   DiscEvent = 8
	EventFreed          DiscEvent = 9
	EventLost           DiscEvent = 10
	EventLostUndone     DiscEvent = 11
	EventRecovered      DiscEvent = 12
	EventNamedMissing   DiscEvent = 13
)

var discEventNames = map[DiscEvent]string{
	EventPacked:         "Packed",
	EventPackUndone:     "PackUndone",
	EventBurnRecorded:   "BurnRecorded",
	EventBurnRemoved:    "BurnRemoved",
	EventCheckOK:        "CheckOK",
	EventCheckFailed:    "CheckFailed",
	EventMarkedVerified: "MarkedVerified",
	EventVerifyUndone:   "VerifyUndone",
	EventFreed:          "Freed",
	EventLost:           "Lost",
	EventLostUndone:     "LostUndone",
	EventRecovered:      "Recovered",
	EventNamedMissing:   "NamedMissing",
}

// String returns the event name, or the code for an unknown event.
func (e DiscEvent) String() string {
	if name, ok := discEventNames[e]; ok {
		return name
	}
	return fmt.Sprintf("event %d", uint8(e))
}

// DiscFlags is the flags field of a disc state log record.
type DiscFlags uint8

const (
	// FlagClose marks a disc that pack closed.
	FlagClose DiscFlags = 1 << 0

	discFlagsKnown = FlagClose
)

// DiscState is the state of a disc, as the replay of its events gives
// it.
type DiscState uint8

// The disc states. DiscUnknown is the state before the first event.
// DiscUndone is the state after PackUndone: the disc is out of the
// repository.
const (
	DiscUnknown DiscState = iota
	DiscPacked
	DiscUndone
	DiscBurned
	DiscVerified
	DiscOnDiscOnly
	DiscLost
	DiscMissing
)

var discStateNames = map[DiscState]string{
	DiscUnknown:    "unknown",
	DiscPacked:     "packed",
	DiscUndone:     "undone",
	DiscBurned:     "burned",
	DiscVerified:   "verified",
	DiscOnDiscOnly: "on disc only",
	DiscLost:       "lost",
	DiscMissing:    "missing",
}

// String returns the word that status shows for the state.
func (s DiscState) String() string {
	if name, ok := discStateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("state %d", uint8(s))
}

// ErrDiscEventRefused is the error for an event that the replay table
// does not permit in the state of the disc.
var ErrDiscEventRefused = errors.New("event not permitted")

// DiscRecord is one record of the disc state log.
type DiscRecord struct {
	Sequence uint64
	TimeSec  int64
	DiscUUID [16]byte
	Event    DiscEvent
	Flags    DiscFlags
	DiscSeq  uint64
	RunSeq   uint64
}

// encode writes r into buf, which is discRecordLen bytes, and seals it
// with its CRC.
func (r *DiscRecord) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], r.Sequence)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(r.TimeSec))
	copy(buf[16:32], r.DiscUUID[:])
	buf[32] = byte(r.Event)
	buf[33] = byte(r.Flags)
	binary.LittleEndian.PutUint64(buf[34:42], r.DiscSeq)
	binary.LittleEndian.PutUint64(buf[42:50], r.RunSeq)
	sealRecord(buf[:discRecordLen])
}

// decodeDiscRecord reads one record from buf, which is discRecordLen
// bytes. It does not check the CRC.
func decodeDiscRecord(buf []byte) DiscRecord {
	return DiscRecord{
		Sequence: binary.LittleEndian.Uint64(buf[0:8]),
		TimeSec:  int64(binary.LittleEndian.Uint64(buf[8:16])),
		DiscUUID: [16]byte(buf[16:32]),
		Event:    DiscEvent(buf[32]),
		Flags:    DiscFlags(buf[33]),
		DiscSeq:  binary.LittleEndian.Uint64(buf[34:42]),
		RunSeq:   binary.LittleEndian.Uint64(buf[42:50]),
	}
}

// eventFlags returns the flag bits that event e can carry: close in
// Packed, and none in every other event.
func eventFlags(e DiscEvent) DiscFlags {
	if e == EventPacked {
		return FlagClose
	}
	return 0
}

// check returns an error when the fields of r break the rules of the
// record: a known event, a disc uuid that is not zero, only the flag bits
// of eventFlags, and numbers only in Packed.
func (r *DiscRecord) check() error {
	if _, ok := discEventNames[r.Event]; !ok {
		return fmt.Errorf("unknown event code %d", uint8(r.Event))
	}
	if r.DiscUUID == [16]byte{} {
		return errors.New("the disc uuid is zero")
	}
	if r.Flags&^discFlagsKnown != 0 {
		return fmt.Errorf("unknown flag bits 0x%02x", uint8(r.Flags&^discFlagsKnown))
	}
	if extra := r.Flags &^ eventFlags(r.Event); extra != 0 {
		return fmt.Errorf("%s carries flags 0x%02x", r.Event, uint8(extra))
	}
	if (r.DiscSeq != 0 || r.RunSeq != 0) && r.Event != EventPacked {
		return fmt.Errorf("%s carries a disc or run number", r.Event)
	}
	return nil
}

// DiscTransition returns the state that event e gives a disc in state
// from. beforeLost is the state of the disc before its Lost event; only
// LostUndone reads it. ok is false when the replay table does not permit
// e in from.
func DiscTransition(from, beforeLost DiscState, e DiscEvent) (to DiscState, ok bool) {
	switch e {
	case EventPacked:
		if from == DiscUnknown {
			return DiscPacked, true
		}
	case EventPackUndone:
		if from == DiscPacked {
			return DiscUndone, true
		}
	case EventBurnRecorded:
		if from == DiscPacked {
			return DiscBurned, true
		}
	case EventBurnRemoved:
		if from == DiscBurned {
			return DiscPacked, true
		}
	case EventCheckOK:
		switch from {
		case DiscBurned:
			return DiscVerified, true
		case DiscVerified, DiscOnDiscOnly:
			return from, true
		}
	case EventCheckFailed:
		switch from {
		case DiscVerified:
			return DiscBurned, true
		case DiscBurned:
			return DiscPacked, true
		case DiscPacked, DiscOnDiscOnly:
			return from, true
		}
	case EventMarkedVerified:
		if from == DiscBurned {
			return DiscVerified, true
		}
	case EventVerifyUndone:
		if from == DiscVerified {
			return DiscBurned, true
		}
	case EventFreed:
		if from == DiscVerified {
			return DiscOnDiscOnly, true
		}
	case EventLost:
		switch from {
		case DiscPacked, DiscBurned, DiscVerified, DiscOnDiscOnly, DiscMissing:
			return DiscLost, true
		}
	case EventLostUndone:
		if from == DiscLost {
			switch beforeLost {
			case DiscVerified:
				return DiscBurned, true
			case DiscOnDiscOnly, DiscMissing:
				return beforeLost, true
			}
		}
	case EventRecovered:
		if from == DiscUnknown || from == DiscMissing {
			return DiscOnDiscOnly, true
		}
	case EventNamedMissing:
		if from == DiscUnknown {
			return DiscMissing, true
		}
	}
	return from, false
}

// CheckResult is the result of the last check of a disc.
type CheckResult uint8

const (
	// CheckResultNone means that no check of the disc is in the log.
	CheckResultNone CheckResult = iota
	// CheckResultOK is a good counted verify.
	CheckResultOK
	// CheckResultFailed is a failed counted verify, or a recover of a
	// damaged disc.
	CheckResultFailed
	// CheckResultNotChecked is a disc verified on the word of the
	// operator.
	CheckResultNotChecked
)

// DiscInfo is the replayed record of one disc.
type DiscInfo struct {
	UUID [16]byte
	// DiscSeq and RunSeq come from the Packed event. They are 0 for a
	// disc that recover wrote, because Recovered carries no numbers.
	DiscSeq uint64
	RunSeq  uint64
	Close   bool
	State   DiscState
	// BeforeLost is the state before the Lost event while the disc is
	// lost. It is DiscUnknown in every other state.
	BeforeLost DiscState
	// VerifiedTime is the time of the event that moved the disc to
	// verified. The states on disc only and lost keep it. Every other
	// state clears it.
	VerifiedTime time.Time
	// LastCheck is the result of the newest CheckOK, CheckFailed or
	// MarkedVerified event, and LastCheckTime its time.
	LastCheck     CheckResult
	LastCheckTime time.Time
	// LastEvent is the newest event of the disc, and LastEventTime its
	// time.
	LastEvent     DiscEvent
	LastEventTime time.Time
}

// apply returns d after the event of rec, or an error when the replay
// table does not permit the event.
func (d DiscInfo) apply(rec DiscRecord) (DiscInfo, error) {
	next, ok := DiscTransition(d.State, d.BeforeLost, rec.Event)
	if !ok {
		return d, fmt.Errorf("disc %s: %s in state %s: %w", uuid.UUID(rec.DiscUUID), rec.Event, d.State, ErrDiscEventRefused)
	}
	at := time.Unix(rec.TimeSec, 0)
	switch rec.Event {
	case EventPacked:
		d.DiscSeq = rec.DiscSeq
		d.RunSeq = rec.RunSeq
		d.Close = rec.Flags&FlagClose != 0
	case EventCheckOK:
		d.LastCheck, d.LastCheckTime = CheckResultOK, at
	case EventCheckFailed:
		d.LastCheck, d.LastCheckTime = CheckResultFailed, at
	case EventMarkedVerified:
		d.LastCheck, d.LastCheckTime = CheckResultNotChecked, at
	case EventLost:
		d.BeforeLost = d.State
	case EventLostUndone:
		d.BeforeLost = DiscUnknown
	}
	switch {
	case next == DiscVerified && d.State != DiscVerified:
		d.VerifiedTime = at
	case next != DiscVerified && next != DiscOnDiscOnly && next != DiscLost:
		d.VerifiedTime = time.Time{}
	}
	d.UUID = rec.DiscUUID
	d.State = next
	d.LastEvent, d.LastEventTime = rec.Event, at
	return d, nil
}

// DiscLog is the replayed disc state log of one repository.
type DiscLog struct {
	file       *recFile
	discs      map[[16]byte]DiscInfo
	maxDiscSeq uint64
	maxRunSeq  uint64
}

// OpenDiscLog reads and replays stateDir's disc state log for a command
// that holds the repository lock. It cuts a torn tail; TornBytes reports
// the cut. A missing file is an empty log.
func OpenDiscLog(stateDir string) (*DiscLog, error) {
	return openDiscLog(stateDir, true)
}

// OpenDiscLogReadOnly reads and replays stateDir's disc state log for a
// command that takes no lock. It ignores a torn tail and never changes
// the file. Append on the returned log fails.
func OpenDiscLogReadOnly(stateDir string) (*DiscLog, error) {
	return openDiscLog(stateDir, false)
}

func openDiscLog(stateDir string, writable bool) (*DiscLog, error) {
	l := &DiscLog{discs: make(map[[16]byte]DiscInfo)}
	f, err := openRecFile(filepath.Join(stateDir, discStateFileName), discRecordLen, writable, func(buf []byte) error {
		rec := decodeDiscRecord(buf)
		if err := rec.check(); err != nil {
			return err
		}
		return l.apply(rec)
	})
	if err != nil {
		return nil, err
	}
	l.file = f
	return l, nil
}

// apply replays one checked record into l.
func (l *DiscLog) apply(rec DiscRecord) error {
	d, err := l.discs[rec.DiscUUID].apply(rec)
	if err != nil {
		return err
	}
	l.discs[rec.DiscUUID] = d
	if rec.Event == EventPacked {
		l.maxDiscSeq = max(l.maxDiscSeq, rec.DiscSeq)
		l.maxRunSeq = max(l.maxRunSeq, rec.RunSeq)
	}
	return nil
}

// TornBytes returns the size of the torn tail that the open found: cut
// by OpenDiscLog, ignored by OpenDiscLogReadOnly. It is 0 for a clean
// log.
func (l *DiscLog) TornBytes() int64 {
	return l.file.tornBytes
}

// Path returns the path of the disc state log file.
func (l *DiscLog) Path() string {
	return l.file.path
}

// Disc returns the record of the disc with uuid id, and whether the log
// knows that disc.
func (l *DiscLog) Disc(id [16]byte) (DiscInfo, bool) {
	d, ok := l.discs[id]
	return d, ok
}

// Discs returns the record of every disc in the log, undone discs
// included, sorted by disc number and then by uuid.
func (l *DiscLog) Discs() []DiscInfo {
	return slices.SortedFunc(maps.Values(l.discs), func(a, b DiscInfo) int {
		return cmp.Or(cmp.Compare(a.DiscSeq, b.DiscSeq), slices.Compare(a.UUID[:], b.UUID[:]))
	})
}

// HighestPacked returns the highest disc number and the highest run
// number over all Packed events, the events of undone packs included.
// Both are 0 when the log has no Packed event.
func (l *DiscLog) HighestPacked() (discSeq, runSeq uint64) {
	return l.maxDiscSeq, l.maxRunSeq
}

// InState returns the record of every disc in one of states, in the
// order of Discs.
func (l *DiscLog) InState(states ...DiscState) []DiscInfo {
	var out []DiscInfo
	for _, d := range l.Discs() {
		if slices.Contains(states, d.State) {
			out = append(out, d)
		}
	}
	return out
}

// Next returns the state that event e gives the disc id now, and
// whether the replay table permits e. It writes nothing. A command calls
// it to refuse a change before it asks a confirmation.
func (l *DiscLog) Next(id [16]byte, e DiscEvent) (DiscState, bool) {
	d := l.discs[id]
	return DiscTransition(d.State, d.BeforeLost, e)
}

// Check reports whether Append would accept recs, and writes nothing.
// It returns the error that Append would return for a record that
// breaks the rules or for a refused transition.
func (l *DiscLog) Check(recs ...DiscRecord) error {
	_, err := l.prepare(recs)
	return err
}

// prepare sets the sequence of each record of a copy of recs, checks
// each record and each transition in order, and returns the copy.
func (l *DiscLog) prepare(recs []DiscRecord) ([]DiscRecord, error) {
	staged := make(map[[16]byte]DiscInfo)
	written := slices.Clone(recs)
	seq := l.file.nextSeq()
	for i := range written {
		rec := &written[i]
		rec.Sequence = seq + uint64(i)
		if err := rec.check(); err != nil {
			return nil, fmt.Errorf("stage: disc %s: %w", uuid.UUID(rec.DiscUUID), err)
		}
		d, ok := staged[rec.DiscUUID]
		if !ok {
			d = l.discs[rec.DiscUUID]
		}
		d, err := d.apply(*rec)
		if err != nil {
			return nil, fmt.Errorf("stage: %w", err)
		}
		staged[rec.DiscUUID] = d
	}
	return written, nil
}

// Append writes recs as one batch with one sync, and then applies them.
// It sets the sequence of each record. It first checks each record and
// each transition in order; when one is refused, it writes nothing and
// returns an error that wraps ErrDiscEventRefused for a refused
// transition. A batch can hold two events of one disc, for example
// BurnRecorded and then CheckOK.
func (l *DiscLog) Append(recs ...DiscRecord) error {
	written, err := l.prepare(recs)
	if err != nil {
		return err
	}
	batch := make([]byte, len(written)*discRecordLen)
	for i := range written {
		written[i].encode(batch[i*discRecordLen : (i+1)*discRecordLen])
	}
	if err := l.file.appendBatch(batch); err != nil {
		return err
	}
	for _, rec := range written {
		if err := l.apply(rec); err != nil {
			return fmt.Errorf("stage: %w", err)
		}
	}
	return nil
}
