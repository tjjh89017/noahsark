package stage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// discStart is one start state of the transition tests. A lost start
// names the state before Lost.
type discStart struct {
	state, beforeLost DiscState
}

func (s discStart) String() string {
	if s.state == DiscLost {
		return fmt.Sprintf("lost (was %s)", s.beforeLost)
	}
	return s.state.String()
}

var discStarts = []discStart{
	{DiscUnknown, DiscUnknown},
	{DiscPacked, DiscUnknown},
	{DiscUndone, DiscUnknown},
	{DiscBurned, DiscUnknown},
	{DiscVerified, DiscUnknown},
	{DiscOnDiscOnly, DiscUnknown},
	{DiscMissing, DiscUnknown},
	{DiscLost, DiscPacked},
	{DiscLost, DiscBurned},
	{DiscLost, DiscVerified},
	{DiscLost, DiscOnDiscOnly},
	{DiscLost, DiscMissing},
}

var allDiscEvents = []DiscEvent{
	EventPacked, EventPackUndone, EventBurnRecorded, EventBurnRemoved,
	EventCheckOK, EventCheckFailed, EventMarkedVerified, EventVerifyUndone,
	EventFreed, EventLost, EventLostUndone, EventRecovered, EventNamedMissing,
}

// replayTable is the table "Replay of the disc state log" of
// docs/states.md: for each event, the permitted start states and the
// result. A start that the table does not list is refused.
var replayTable = map[DiscEvent]map[discStart]DiscState{
	EventPacked:       {{DiscUnknown, DiscUnknown}: DiscPacked},
	EventPackUndone:   {{DiscPacked, DiscUnknown}: DiscUndone},
	EventBurnRecorded: {{DiscPacked, DiscUnknown}: DiscBurned},
	EventBurnRemoved:  {{DiscBurned, DiscUnknown}: DiscPacked},
	EventCheckOK: {
		{DiscBurned, DiscUnknown}:     DiscVerified,
		{DiscVerified, DiscUnknown}:   DiscVerified,
		{DiscOnDiscOnly, DiscUnknown}: DiscOnDiscOnly,
	},
	EventCheckFailed: {
		{DiscVerified, DiscUnknown}:   DiscBurned,
		{DiscBurned, DiscUnknown}:     DiscPacked,
		{DiscPacked, DiscUnknown}:     DiscPacked,
		{DiscOnDiscOnly, DiscUnknown}: DiscOnDiscOnly,
	},
	EventMarkedVerified: {{DiscBurned, DiscUnknown}: DiscVerified},
	EventVerifyUndone:   {{DiscVerified, DiscUnknown}: DiscBurned},
	EventFreed:          {{DiscVerified, DiscUnknown}: DiscOnDiscOnly},
	EventLost: {
		{DiscPacked, DiscUnknown}:     DiscLost,
		{DiscBurned, DiscUnknown}:     DiscLost,
		{DiscVerified, DiscUnknown}:   DiscLost,
		{DiscOnDiscOnly, DiscUnknown}: DiscLost,
		{DiscMissing, DiscUnknown}:    DiscLost,
	},
	EventLostUndone: {
		{DiscLost, DiscVerified}:   DiscBurned,
		{DiscLost, DiscOnDiscOnly}: DiscOnDiscOnly,
		{DiscLost, DiscMissing}:    DiscMissing,
	},
	EventRecovered: {
		{DiscUnknown, DiscUnknown}: DiscOnDiscOnly,
		{DiscMissing, DiscUnknown}: DiscOnDiscOnly,
	},
	EventNamedMissing: {{DiscUnknown, DiscUnknown}: DiscMissing},
}

func TestDiscTransitionTable(t *testing.T) {
	for _, e := range allDiscEvents {
		for _, s := range discStarts {
			want, wantOK := replayTable[e][s]
			got, ok := DiscTransition(s.state, s.beforeLost, e)
			if ok != wantOK || (ok && got != want) {
				t.Errorf("%s in %s: got %s, %v; want %s, %v", e, s, got, ok, want, wantOK)
			}
		}
	}
}

// discPath returns the events that bring a disc from unknown to s.
func discPath(s discStart) []DiscEvent {
	switch s.state {
	case DiscPacked:
		return []DiscEvent{EventPacked}
	case DiscUndone:
		return []DiscEvent{EventPacked, EventPackUndone}
	case DiscBurned:
		return []DiscEvent{EventPacked, EventBurnRecorded}
	case DiscVerified:
		return []DiscEvent{EventPacked, EventBurnRecorded, EventCheckOK}
	case DiscOnDiscOnly:
		return []DiscEvent{EventPacked, EventBurnRecorded, EventCheckOK, EventFreed}
	case DiscMissing:
		return []DiscEvent{EventNamedMissing}
	case DiscLost:
		return append(discPath(discStart{s.beforeLost, DiscUnknown}), EventLost)
	}
	return nil
}

var testDiscA = [16]byte{0xA0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// discEvent returns a record of event e for disc id at time sec, with
// the fields that the event carries.
func discEvent(id [16]byte, e DiscEvent, sec int64) DiscRecord {
	rec := DiscRecord{TimeSec: sec, DiscUUID: id, Event: e}
	switch e {
	case EventPacked:
		rec.DiscSeq = 3
		rec.RunSeq = 5
	}
	return rec
}

func appendEvents(t *testing.T, l *DiscLog, id [16]byte, sec int64, events ...DiscEvent) {
	t.Helper()
	for _, e := range events {
		if err := l.Append(discEvent(id, e, sec)); err != nil {
			t.Fatalf("append %s: %v", e, err)
		}
		sec++
	}
}

// TestDiscLogEachEventInEachState appends each event to a disc in each
// state. A permitted event gives the state of the table, also after a
// new open. A refused event returns ErrDiscEventRefused and writes
// nothing.
func TestDiscLogEachEventInEachState(t *testing.T) {
	for _, s := range discStarts {
		for _, e := range allDiscEvents {
			t.Run(fmt.Sprintf("%s/%s", s, e), func(t *testing.T) {
				dir := t.TempDir()
				l, err := OpenDiscLog(dir)
				if err != nil {
					t.Fatal(err)
				}
				appendEvents(t, l, testDiscA, 1000, discPath(s)...)
				before := readDiscFile(t, dir)

				want, permitted := replayTable[e][s]
				err = l.Append(discEvent(testDiscA, e, 2000))
				if !permitted {
					if !errors.Is(err, ErrDiscEventRefused) {
						t.Fatalf("error %v, want ErrDiscEventRefused", err)
					}
					if !bytes.Equal(readDiscFile(t, dir), before) {
						t.Fatal("a refused append changed the file")
					}
					if d, _ := l.Disc(testDiscA); d.State != s.state {
						t.Fatalf("state %s after a refused append, want %s", d.State, s.state)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if d, _ := l.Disc(testDiscA); d.State != want {
					t.Fatalf("state %s, want %s", d.State, want)
				}
				again, err := OpenDiscLogReadOnly(dir)
				if err != nil {
					t.Fatal(err)
				}
				if d, _ := again.Disc(testDiscA); d.State != want {
					t.Fatalf("state %s after replay, want %s", d.State, want)
				}
			})
		}
	}
}

func readDiscFile(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, discStateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// writeDiscFile writes recs with sequences 1 to n as the disc state log
// of a new state directory.
func writeDiscFile(t *testing.T, recs ...DiscRecord) string {
	t.Helper()
	dir := t.TempDir()
	var data []byte
	buf := make([]byte, discRecordLen)
	for i, rec := range recs {
		rec.Sequence = uint64(i + 1)
		rec.encode(buf)
		data = append(data, buf...)
	}
	if err := os.WriteFile(filepath.Join(dir, discStateFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiscLogReplayDamage(t *testing.T) {
	cases := []struct {
		name string
		recs []DiscRecord
		want string
	}{
		{"refused event", []DiscRecord{
			discEvent(testDiscA, EventPacked, 1),
			discEvent(testDiscA, EventCheckOK, 2),
			discEvent(testDiscA, EventBurnRecorded, 3),
		}, "record 2 (sequence 2)"},
		{"unknown event code", []DiscRecord{
			{DiscUUID: testDiscA, Event: 14},
			discEvent(testDiscA, EventPacked, 1),
		}, "unknown event code 14"},
		{"flags on a later event", []DiscRecord{
			discEvent(testDiscA, EventPacked, 1),
			{DiscUUID: testDiscA, Event: EventBurnRecorded, Flags: FlagClose},
			discEvent(testDiscA, EventBurnRemoved, 3),
		}, "BurnRecorded carries flags"},
		{"close bit in Recovered", []DiscRecord{
			{DiscUUID: testDiscA, Event: EventRecovered, Flags: FlagClose},
			discEvent(testDiscA, EventCheckOK, 2),
		}, "Recovered carries flags 0x01"},
		{"unknown flag bit", []DiscRecord{
			{DiscUUID: testDiscA, Event: EventPacked, Flags: 0x04},
			discEvent(testDiscA, EventPackUndone, 2),
		}, "unknown flag bits"},
		{"bit 1 in Packed", []DiscRecord{
			{DiscUUID: testDiscA, Event: EventPacked, Flags: 0x02},
			discEvent(testDiscA, EventPackUndone, 2),
		}, "unknown flag bits 0x02"},
		{"disc number on a later event", []DiscRecord{
			discEvent(testDiscA, EventPacked, 1),
			{DiscUUID: testDiscA, Event: EventPackUndone, DiscSeq: 3},
			discEvent(testDiscA, EventPacked, 3),
		}, "carries a disc or run number"},
		{"zero disc uuid", []DiscRecord{
			{Event: EventNamedMissing},
			discEvent(testDiscA, EventPacked, 1),
		}, "the disc uuid is zero"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeDiscFile(t, tc.recs...)
			before := readDiscFile(t, dir)
			_, err := OpenDiscLog(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "damaged") {
				t.Fatalf("error %v, want damage that contains %q", err, tc.want)
			}
			if !bytes.Equal(readDiscFile(t, dir), before) {
				t.Fatal("the open changed a damaged log")
			}
		})
	}
}

func TestDiscLogTornTail(t *testing.T) {
	dir := writeDiscFile(t, discEvent(testDiscA, EventPacked, 1), discEvent(testDiscA, EventBurnRecorded, 2))
	path := filepath.Join(dir, discStateFileName)
	full := readDiscFile(t, dir)
	torn := full[:discRecordLen+20]
	if err := os.WriteFile(path, torn, 0o644); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenDiscLogReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := ro.Disc(testDiscA); d.State != DiscPacked || ro.TornBytes() != 20 {
		t.Fatalf("read-only: state %s, torn %d; want packed, 20", d.State, ro.TornBytes())
	}
	if err := ro.Append(discEvent(testDiscA, EventBurnRecorded, 3)); err == nil {
		t.Fatal("a read-only log accepted an append")
	}
	if !bytes.Equal(readDiscFile(t, dir), torn) {
		t.Fatal("the read-only open changed the file")
	}

	l, err := OpenDiscLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.TornBytes() != 20 || len(readDiscFile(t, dir)) != discRecordLen {
		t.Fatalf("torn %d, file %d bytes; want the cut to the first record", l.TornBytes(), len(readDiscFile(t, dir)))
	}
	if err := l.Append(discEvent(testDiscA, EventBurnRecorded, 3)); err != nil {
		t.Fatal(err)
	}
	again, err := OpenDiscLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := again.Disc(testDiscA); d.State != DiscBurned || again.TornBytes() != 0 {
		t.Fatalf("state %s, torn %d; want burned, 0", d.State, again.TornBytes())
	}
}

func TestDiscLogBatchAppend(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenDiscLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendEvents(t, l, testDiscA, 10, EventPacked)

	calls := withRecSeams(t, nil, nil, nil)
	if err := l.Append(discEvent(testDiscA, EventBurnRecorded, 20), discEvent(testDiscA, EventCheckOK, 20)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "write,sync,close" {
		t.Fatalf("calls %q, want one write and one sync for the batch", got)
	}
	if d, _ := l.Disc(testDiscA); d.State != DiscVerified {
		t.Fatalf("state %s, want verified", d.State)
	}

	before := readDiscFile(t, dir)
	err = l.Append(discEvent(testDiscA, EventFreed, 30), discEvent(testDiscA, EventBurnRemoved, 30))
	if !errors.Is(err, ErrDiscEventRefused) {
		t.Fatalf("error %v, want ErrDiscEventRefused for the second event", err)
	}
	if !bytes.Equal(readDiscFile(t, dir), before) {
		t.Fatal("a batch with a refused event changed the file")
	}
	if d, _ := l.Disc(testDiscA); d.State != DiscVerified {
		t.Fatalf("state %s after a refused batch, want verified", d.State)
	}

	if err := l.Append(DiscRecord{TimeSec: 40, DiscUUID: testDiscA, Event: EventFreed, Flags: FlagClose}); err == nil {
		t.Fatal("Append accepted flags on Freed")
	}
	if !bytes.Equal(readDiscFile(t, dir), before) {
		t.Fatal("a refused record changed the file")
	}
}

func TestDiscLogAppendSyncError(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenDiscLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	errSync := errors.New("injected sync error")
	withRecSeams(t, errSync, nil, nil)
	if err := l.Append(discEvent(testDiscA, EventPacked, 1)); !errors.Is(err, errSync) {
		t.Fatalf("error %v, want it to wrap the sync error", err)
	}
	if _, ok := l.Disc(testDiscA); ok {
		t.Fatal("a failed append added the disc")
	}
	if seq, run := l.HighestPacked(); seq != 0 || run != 0 {
		t.Fatalf("HighestPacked %d, %d after a failed append, want 0, 0", seq, run)
	}
}

func TestDiscInfoFields(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenDiscLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := func(sec int64) time.Time { return time.Unix(sec, 0) }
	info := func() DiscInfo {
		t.Helper()
		d, ok := l.Disc(testDiscA)
		if !ok {
			t.Fatal("the disc is not in the log")
		}
		return d
	}

	appendEvents(t, l, testDiscA, 100, EventPacked)
	d := info()
	if d.UUID != testDiscA || d.DiscSeq != 3 || d.RunSeq != 5 || d.Close {
		t.Fatalf("after Packed: %+v", d)
	}
	if d.LastCheck != CheckResultNone || !d.LastCheckTime.IsZero() || !d.VerifiedTime.IsZero() {
		t.Fatalf("after Packed: a check or a verified time: %+v", d)
	}

	appendEvents(t, l, testDiscA, 200, EventCheckFailed)
	if d := info(); d.State != DiscPacked || d.LastCheck != CheckResultFailed || !d.LastCheckTime.Equal(at(200)) {
		t.Fatalf("after a failed check of a packed disc: %+v", d)
	}

	appendEvents(t, l, testDiscA, 300, EventBurnRecorded, EventMarkedVerified)
	if d := info(); d.LastCheck != CheckResultNotChecked || !d.LastCheckTime.Equal(at(301)) || !d.VerifiedTime.Equal(at(301)) {
		t.Fatalf("after MarkedVerified: %+v", d)
	}

	appendEvents(t, l, testDiscA, 400, EventCheckOK)
	if d := info(); d.LastCheck != CheckResultOK || !d.LastCheckTime.Equal(at(400)) || !d.VerifiedTime.Equal(at(301)) {
		t.Fatalf("a check of a verified disc must keep the verified time: %+v", d)
	}

	appendEvents(t, l, testDiscA, 500, EventVerifyUndone)
	if d := info(); d.State != DiscBurned || !d.VerifiedTime.IsZero() || d.LastCheck != CheckResultOK {
		t.Fatalf("after VerifyUndone: %+v", d)
	}

	appendEvents(t, l, testDiscA, 600, EventCheckOK, EventFreed)
	if d := info(); d.State != DiscOnDiscOnly || !d.VerifiedTime.Equal(at(600)) {
		t.Fatalf("after Freed: %+v", d)
	}

	appendEvents(t, l, testDiscA, 700, EventLost)
	if d := info(); d.State != DiscLost || d.BeforeLost != DiscOnDiscOnly || !d.VerifiedTime.Equal(at(600)) {
		t.Fatalf("after Lost: %+v", d)
	}

	appendEvents(t, l, testDiscA, 800, EventLostUndone)
	if d := info(); d.State != DiscOnDiscOnly || d.BeforeLost != DiscUnknown || !d.VerifiedTime.Equal(at(600)) {
		t.Fatalf("after LostUndone: %+v", d)
	}

	appendEvents(t, l, testDiscA, 900, EventCheckFailed)
	if d := info(); d.State != DiscOnDiscOnly || d.LastCheck != CheckResultFailed || !d.LastCheckTime.Equal(at(900)) {
		t.Fatalf("after a failed check of an on disc only disc: %+v", d)
	}

	replayed, err := OpenDiscLogReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := replayed.Disc(testDiscA); got != info() {
		t.Fatalf("replay gives %+v, want %+v", got, info())
	}
}

func TestDiscInfoLostFromVerified(t *testing.T) {
	l, err := OpenDiscLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	appendEvents(t, l, testDiscA, 100, EventPacked, EventBurnRecorded, EventCheckOK, EventLost)
	if d, _ := l.Disc(testDiscA); d.BeforeLost != DiscVerified || !d.VerifiedTime.Equal(time.Unix(102, 0)) {
		t.Fatalf("after Lost: %+v", d)
	}
	appendEvents(t, l, testDiscA, 200, EventLostUndone)
	if d, _ := l.Disc(testDiscA); d.State != DiscBurned || !d.VerifiedTime.IsZero() {
		t.Fatalf("after LostUndone: %+v; want burned with no verified time", d)
	}
}

func TestDiscInfoRecovered(t *testing.T) {
	l, err := OpenDiscLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	appendEvents(t, l, testDiscA, 100, EventNamedMissing, EventRecovered)
	d, _ := l.Disc(testDiscA)
	if d.State != DiscOnDiscOnly || d.Close || d.DiscSeq != 0 || d.LastCheck != CheckResultNone || !d.VerifiedTime.IsZero() {
		t.Fatalf("after Recovered: %+v", d)
	}
}

func TestDiscLogListAndHighestPacked(t *testing.T) {
	l, err := OpenDiscLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packed := func(id byte, discSeq, runSeq uint64) DiscRecord {
		return DiscRecord{TimeSec: 1, DiscUUID: [16]byte{id}, Event: EventPacked, DiscSeq: discSeq, RunSeq: runSeq}
	}
	for _, rec := range []DiscRecord{
		packed(0x03, 2, 2),
		packed(0x01, 0, 0),
		packed(0x02, 7, 11),
		{TimeSec: 2, DiscUUID: [16]byte{0x02}, Event: EventPackUndone},
		packed(0x04, 5, 6),
		{TimeSec: 3, DiscUUID: [16]byte{0x00, 0x09}, Event: EventNamedMissing},
	} {
		if err := l.Append(rec); err != nil {
			t.Fatal(err)
		}
	}

	var got []string
	for _, d := range l.Discs() {
		got = append(got, fmt.Sprintf("%d:%02x%02x:%s", d.DiscSeq, d.UUID[0], d.UUID[1], d.State))
	}
	want := "0:0009:missing 0:0100:packed 2:0300:packed 5:0400:packed 7:0200:undone"
	if strings.Join(got, " ") != want {
		t.Fatalf("Discs() = %s, want %s", strings.Join(got, " "), want)
	}

	if discSeq, runSeq := l.HighestPacked(); discSeq != 7 || runSeq != 11 {
		t.Fatalf("HighestPacked = %d, %d; want 7, 11 from the undone pack", discSeq, runSeq)
	}
	if _, ok := l.Disc([16]byte{0xEE}); ok {
		t.Fatal("Disc found a uuid that the log does not hold")
	}
}

// TestDiscLogCheckAndNext checks the functions that decide a change
// before a command asks a confirmation: they give the answer of Append,
// and they write nothing.
func TestDiscLogCheckAndNext(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenDiscLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendEvents(t, l, testDiscA, 1000, EventPacked, EventBurnRecorded)
	before := readDiscFile(t, dir)

	if to, ok := l.Next(testDiscA, EventBurnRemoved); !ok || to != DiscPacked {
		t.Fatalf("Next(BurnRemoved) = %s, %v; want packed, true", to, ok)
	}
	if to, ok := l.Next(testDiscA, EventBurnRecorded); ok || to != DiscBurned {
		t.Fatalf("Next(BurnRecorded) = %s, %v; want burned, false", to, ok)
	}
	if err := l.Check(discEvent(testDiscA, EventCheckOK, 2000), discEvent(testDiscA, EventFreed, 2001)); err != nil {
		t.Fatalf("Check of a permitted batch: %v", err)
	}
	if err := l.Check(discEvent(testDiscA, EventFreed, 2000)); !errors.Is(err, ErrDiscEventRefused) {
		t.Fatalf("Check of a refused event = %v, want ErrDiscEventRefused", err)
	}
	if !bytes.Equal(readDiscFile(t, dir), before) {
		t.Fatal("Check or Next wrote the file")
	}
	if d, _ := l.Disc(testDiscA); d.State != DiscBurned {
		t.Fatalf("state %s after Check, want burned", d.State)
	}
}

func TestDiscLogInState(t *testing.T) {
	l, err := OpenDiscLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, c := [16]byte{0xB0}, [16]byte{0xC0}
	appendEvents(t, l, testDiscA, 1000, EventPacked)
	appendEvents(t, l, b, 1000, EventNamedMissing)
	appendEvents(t, l, c, 1000, EventRecovered)
	got := l.InState(DiscMissing, DiscOnDiscOnly)
	if len(got) != 2 || got[0].UUID != b || got[1].UUID != c {
		t.Fatalf("InState = %+v, want the missing and the on disc only disc", got)
	}
	if got := l.InState(DiscLost); len(got) != 0 {
		t.Fatalf("InState(lost) = %+v, want none", got)
	}
}
