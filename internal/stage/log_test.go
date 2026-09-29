package stage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// openTestLog opens the item log in dir writable.
func openTestLog(t *testing.T, dir string) *Log {
	t.Helper()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// mustState fails the test when the newest record of id is not in want.
func mustState(t *testing.T, l *Log, id object.ID, want State) Record {
	t.Helper()
	rec, ok := l.Get(id)
	if !ok || rec.State != want {
		t.Fatalf("item %x: record %+v, known %v; want %s", id[:2], rec, ok, want)
	}
	return rec
}

// itemFile returns the bytes of the item log in dir.
func itemFile(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return data
}

func TestItemLogReplaysAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	disc := fillDisc(0xA1)
	l := openTestLog(t, dir)
	if err := l.EnsureStaged(fillID(1), fillID(2), fillID(3)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(4, disc, fillID(1), fillID(2)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkOnDisc(fillID(2)); err != nil {
		t.Fatal(err)
	}

	again, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := mustState(t, again, fillID(1), Packed); rec.RunSeq != 4 || rec.DiscUUID != disc || rec.Sequence != 4 {
		t.Fatalf("item 1: %+v", rec)
	}
	if rec := mustState(t, again, fillID(2), OnDisc); rec.RunSeq != 4 || rec.DiscUUID != disc || rec.Sequence != 6 {
		t.Fatalf("item 2: %+v", rec)
	}
	mustState(t, again, fillID(3), Staged)
}

// TestEnsureStagedRule checks the rule of commit: an unknown or Lost
// item becomes Staged, and every other item keeps its record.
func TestEnsureStagedRule(t *testing.T) {
	dir := t.TempDir()
	disc := fillDisc(0xB2)
	l := openTestLog(t, dir)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(l.EnsureStaged(fillID(1), fillID(2), fillID(3), fillID(4)))
	must(l.MarkPacked(1, disc, fillID(2), fillID(3), fillID(4)))
	must(l.MarkOnDisc(fillID(3), fillID(4)))
	must(l.MarkLost(fillID(4)))
	before := map[object.ID]Record{}
	for i := byte(1); i <= 4; i++ {
		before[fillID(i)], _ = l.Get(fillID(i))
	}

	must(l.EnsureStaged(fillID(1), fillID(2), fillID(3), fillID(4), fillID(5), fillID(5)))

	for i := byte(1); i <= 3; i++ {
		if rec, _ := l.Get(fillID(i)); rec != before[fillID(i)] {
			t.Fatalf("item %d changed: %+v, was %+v", i, rec, before[fillID(i)])
		}
	}
	if rec := mustState(t, l, fillID(4), Staged); rec.DiscUUID != ([16]byte{}) || rec.Reason != ReasonNormal {
		t.Fatalf("the Lost item: %+v", rec)
	}
	mustState(t, l, fillID(5), Staged)
	if n := len(itemFile(t, dir)) / recordLen; n != 12 {
		t.Fatalf("the log holds %d records, want 12: one new record for the Lost item and one for the new item", n)
	}
}

// TestEnsureOnDiscRule checks the rule of recover: an unknown or Lost
// item becomes OnDisc on the given disc, every other item keeps its
// record, and a repeat call appends nothing.
func TestEnsureOnDiscRule(t *testing.T) {
	dir := t.TempDir()
	old, disc := fillDisc(0xC1), fillDisc(0xC2)
	l := openTestLog(t, dir)
	if err := l.EnsureStaged(fillID(1), fillID(2), fillID(3)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(1, old, fillID(2), fillID(3)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkOnDisc(fillID(3)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkLost(fillID(3)); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := l.EnsureOnDisc(8, disc, fillID(1), fillID(2), fillID(3), fillID(4)); err != nil {
			t.Fatal(err)
		}
	}
	mustState(t, l, fillID(1), Staged)
	if rec := mustState(t, l, fillID(2), Packed); rec.DiscUUID != old {
		t.Fatalf("the Packed item moved to %x", rec.DiscUUID)
	}
	for _, id := range []object.ID{fillID(3), fillID(4)} {
		if rec := mustState(t, l, id, OnDisc); rec.DiscUUID != disc || rec.RunSeq != 8 {
			t.Fatalf("item %x: %+v, want OnDisc on the recovered disc", id[:1], rec)
		}
	}
	if n := len(itemFile(t, dir)) / recordLen; n != 9 {
		t.Fatalf("the log holds %d records, want 9: the repeat call appended", n)
	}
}

// TestItemTransitions checks each change function in each start state:
// a permitted change writes one record, and a refused change writes
// nothing and wraps ErrItemTransition.
func TestItemTransitions(t *testing.T) {
	disc, other := fillDisc(0xD1), fillDisc(0xD2)
	type change struct {
		name string
		from []State
		run  func(l *Log, id object.ID) error
		want Record
	}
	changes := []change{
		{"MarkPacked", []State{Staged}, func(l *Log, id object.ID) error { return l.MarkPacked(5, other, id) },
			Record{State: Packed, RunSeq: 5, DiscUUID: other}},
		{"MarkPackUndone", []State{Packed}, func(l *Log, id object.ID) error { return l.MarkPackUndone(id) },
			Record{State: Staged, Reason: ReasonPackUndone}},
		{"MarkStaged disc lost", []State{Packed}, func(l *Log, id object.ID) error { return l.MarkStaged(ReasonDiscLost, id) },
			Record{State: Staged, Reason: ReasonDiscLost}},
		{"MarkOnDisc", []State{Packed}, func(l *Log, id object.ID) error { return l.MarkOnDisc(id) },
			Record{State: OnDisc, RunSeq: 3, DiscUUID: disc}},
		{"MarkLost", []State{OnDisc}, func(l *Log, id object.ID) error { return l.MarkLost(id) },
			Record{State: Lost, RunSeq: 3, DiscUUID: disc, Reason: ReasonDiscLost}},
		{"MarkLostUndone", []State{Lost}, func(l *Log, id object.ID) error { return l.MarkLostUndone(id) },
			Record{State: OnDisc, RunSeq: 3, DiscUUID: disc, Reason: ReasonLostUndone}},
		{"ReturnToDisc", []State{Staged}, func(l *Log, id object.ID) error { return l.ReturnToDisc(6, other, id) },
			Record{State: Packed, RunSeq: 6, DiscUUID: other, Reason: ReasonLostUndone}},
	}
	// start builds an item in state s. A disc state uses the disc disc
	// and the run 3.
	start := func(t *testing.T, l *Log, id object.ID, s State) {
		t.Helper()
		steps := map[State][]func() error{
			Staged: {func() error { return l.EnsureStaged(id) }},
			Packed: {func() error { return l.EnsureStaged(id) }, func() error { return l.MarkPacked(3, disc, id) }},
			OnDisc: {func() error { return l.EnsureOnDisc(3, disc, id) }},
			Lost:   {func() error { return l.EnsureOnDisc(3, disc, id) }, func() error { return l.MarkLost(id) }},
		}
		for _, step := range steps[s] {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range changes {
		for _, from := range []State{0, Staged, Packed, OnDisc, Lost} {
			t.Run(c.name+" from "+from.String(), func(t *testing.T) {
				dir := t.TempDir()
				l := openTestLog(t, dir)
				id := fillID(0x42)
				if from != 0 {
					start(t, l, id, from)
				}
				before := itemFile(t, dir)
				err := c.run(l, id)
				if !slices.Contains(c.from, from) {
					if !errors.Is(err, ErrItemTransition) {
						t.Fatalf("error %v, want ErrItemTransition", err)
					}
					if !bytes.Equal(itemFile(t, dir), before) {
						t.Fatal("a refused change wrote the log")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				rec, _ := l.Get(id)
				want := c.want
				want.ContentID, want.Sequence = id, rec.Sequence
				if rec != want {
					t.Fatalf("record %+v, want %+v", rec, want)
				}
			})
		}
	}
}

// TestItemChangeRefusesTheWholeBatch checks that one refused id stops
// the whole batch.
func TestItemChangeRefusesTheWholeBatch(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir)
	if err := l.EnsureStaged(fillID(1)); err != nil {
		t.Fatal(err)
	}
	before := itemFile(t, dir)
	if err := l.MarkPacked(1, fillDisc(1), fillID(1), fillID(2)); !errors.Is(err, ErrItemTransition) {
		t.Fatalf("error %v, want ErrItemTransition for the unknown item", err)
	}
	if !bytes.Equal(itemFile(t, dir), before) {
		t.Fatal("a refused batch wrote the log")
	}
	mustState(t, l, fillID(1), Staged)
}

// TestItemBatchIsOneSync checks that a batch writes all records with one
// write and one sync, and syncs the directory when it creates the file.
func TestItemBatchIsOneSync(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir)
	calls := withRecSeams(t, nil, nil, nil)
	if err := l.EnsureStaged(fillID(1), fillID(2), fillID(3)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "write,sync,close,syncdir" {
		t.Fatalf("calls %q, want one write, one sync and a directory sync", got)
	}
	*calls = nil
	if err := l.MarkPacked(1, fillDisc(1), fillID(1), fillID(2)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "write,sync,close" {
		t.Fatalf("calls %q, want one write and one sync", got)
	}
}

// TestItemAppendErrorKeepsState checks that a failed sync or close
// returns the error and leaves the replayed state as it was.
func TestItemAppendErrorKeepsState(t *testing.T) {
	for _, tc := range []struct {
		name              string
		syncErr, closeErr error
	}{
		{"sync", errors.New("injected sync error"), nil},
		{"close", nil, errors.New("injected close error")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			l := openTestLog(t, dir)
			if err := l.EnsureStaged(fillID(1)); err != nil {
				t.Fatal(err)
			}
			withRecSeams(t, tc.syncErr, tc.closeErr, nil)
			err := l.MarkPacked(1, fillDisc(1), fillID(1))
			if err == nil || !errors.Is(err, cmpErr(tc.syncErr, tc.closeErr)) {
				t.Fatalf("error %v, want the injected error", err)
			}
			mustState(t, l, fillID(1), Staged)
		})
	}
}

// cmpErr returns the first error that is not nil.
func cmpErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func TestItemLogTornTail(t *testing.T) {
	for _, writable := range []bool{true, false} {
		dir := t.TempDir()
		l := openTestLog(t, dir)
		if err := l.EnsureStaged(fillID(1), fillID(2)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, stateFileName)
		torn := append(itemFile(t, dir), 1, 2, 3)
		if err := os.WriteFile(path, torn, 0o644); err != nil {
			t.Fatal(err)
		}

		again, err := openLog(dir, writable)
		if err != nil {
			t.Fatal(err)
		}
		if again.TornBytes() != 3 {
			t.Fatalf("writable %v: torn bytes %d, want 3", writable, again.TornBytes())
		}
		mustState(t, again, fillID(2), Staged)
		wantLen := len(torn)
		if writable {
			wantLen -= 3
		}
		if got := len(itemFile(t, dir)); got != wantLen {
			t.Fatalf("writable %v: file size %d, want %d", writable, got, wantLen)
		}
		if !writable {
			if err := again.EnsureStaged(fillID(3)); err == nil {
				t.Fatal("a read-only log accepted an append")
			}
		}
	}
}

// TestItemLogDamage checks that a record that breaks the rules of the
// record, a bad CRC in the middle, and a sequence that does not grow are
// damage: the open fails, names the record, and changes no byte.
func TestItemLogDamage(t *testing.T) {
	good := func(seq uint64, rec Record) []byte {
		rec.Sequence = seq
		buf := make([]byte, recordLen)
		rec.encode(buf)
		return buf
	}
	staged := Record{ContentID: fillID(1), State: Staged}
	badMiddle := append(good(1, staged), good(2, staged)...)
	badMiddle = append(badMiddle, good(3, staged)...)
	badMiddle[recordLen+20] ^= 0x01
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"bad CRC in the middle", badMiddle, "record 2 of 3 has a bad CRC"},
		{"sequence repeats", append(good(1, staged), good(1, staged)...), "record 2 has sequence 1 after sequence 1"},
		{"unknown state", append(good(1, Record{ContentID: fillID(1), State: 9}), good(2, staged)...), "unknown state code 9"},
		{"unknown reason", append(good(1, Record{ContentID: fillID(1), State: Staged, Reason: 4}), good(2, staged)...), "unknown reason code 4"},
		{"Staged with a disc", append(good(1, Record{ContentID: fillID(1), State: Staged, DiscUUID: fillDisc(1)}), good(2, staged)...), "names a run or a disc"},
		{"Packed with no disc", append(good(1, Record{ContentID: fillID(1), State: Packed, RunSeq: 1}), good(2, staged)...), "names no disc"},
	}
	for _, tc := range cases {
		for _, writable := range []bool{true, false} {
			dir := t.TempDir()
			path := filepath.Join(dir, stateFileName)
			if err := os.WriteFile(path, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := openLog(dir, writable)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "damaged") {
				t.Fatalf("%s, writable %v: error %v, want one that contains %q", tc.name, writable, err, tc.want)
			}
			if !bytes.Equal(itemFile(t, dir), tc.data) {
				t.Fatalf("%s: the open changed a damaged file", tc.name)
			}
		}
	}
}

func TestItemQueries(t *testing.T) {
	dir := t.TempDir()
	discA, discB := fillDisc(0xA0), fillDisc(0xB0)
	l := openTestLog(t, dir)
	if err := l.EnsureStaged(fillID(1), fillID(2), fillID(3), fillID(4), fillID(5)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(1, discA, fillID(3), fillID(2)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(2, discB, fillID(4)); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkOnDisc(fillID(3)); err != nil {
		t.Fatal(err)
	}

	if got := l.IDsInState(Staged); !slices.Equal(got, []object.ID{fillID(1), fillID(5)}) {
		t.Fatalf("IDsInState(Staged) = %x", got)
	}
	if got := l.ItemsOfDisc(discA); !slices.Equal(got, []object.ID{fillID(2), fillID(3)}) {
		t.Fatalf("ItemsOfDisc(A) = %x", got)
	}
	if got := l.ItemsOfDiscInState(discA, Packed); !slices.Equal(got, []object.ID{fillID(2)}) {
		t.Fatalf("ItemsOfDiscInState(A, Packed) = %x", got)
	}
	if got := l.CountByDisc(Packed); got[discA] != 1 || got[discB] != 1 || len(got) != 2 {
		t.Fatalf("CountByDisc(Packed) = %v", got)
	}
	if got := l.CountByDisc(Staged); len(got) != 0 {
		t.Fatalf("CountByDisc(Staged) = %v, want no disc", got)
	}
	if l.CountState(Staged) != 2 || l.CountState(Packed) != 2 || l.CountState(OnDisc) != 1 || l.CountOnDisc() != 3 {
		t.Fatal("wrong counts by state")
	}
	if got := l.DiscsNamed(); !slices.Equal(got, [][16]byte{discA, discB}) {
		t.Fatalf("DiscsNamed = %x", got)
	}
	n, total, err := l.Totals(Staged, func(id object.ID) (uint64, error) { return uint64(id[0]) * 10, nil })
	if err != nil || n != 2 || total != 60 {
		t.Fatalf("Totals(Staged) = %d, %d, %v; want 2, 60", n, total, err)
	}
	injected := errors.New("injected")
	if _, _, err := l.Totals(Staged, func(object.ID) (uint64, error) { return 0, injected }); !errors.Is(err, injected) {
		t.Fatalf("Totals error = %v, want the size error", err)
	}
}

// TestItemWord checks the derived item words of docs/states.md, "Item
// states".
func TestItemWord(t *testing.T) {
	disc := fillDisc(0xE0)
	packed := Record{State: Packed, DiscUUID: disc}
	cases := []struct {
		rec  Record
		disc DiscState
		want ItemWord
	}{
		{Record{State: Staged}, DiscUnknown, WordStaged},
		{packed, DiscPacked, WordPacked},
		{packed, DiscBurned, WordBurned},
		{packed, DiscVerified, WordClean},
		{Record{State: OnDisc, DiscUUID: disc}, DiscOnDiscOnly, WordOnDisc},
		{Record{State: Lost, DiscUUID: disc}, DiscLost, WordLost},
	}
	for _, c := range cases {
		if got := Word(c.rec, DiscInfo{UUID: disc, State: c.disc}); got != c.want {
			t.Errorf("Word(%s, disc %s) = %q, want %q", c.rec.State, c.disc, got, c.want)
		}
	}

	dir := t.TempDir()
	logs, err := OpenLogs(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.EnsureStaged(fillID(1)); err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkPacked(1, disc, fillID(1)); err != nil {
		t.Fatal(err)
	}
	appendEvents(t, logs.Discs, disc, 100, EventPacked, EventBurnRecorded, EventCheckOK)
	if w, ok := logs.Word(fillID(1)); !ok || w != WordClean {
		t.Fatalf("Logs.Word = %q, %v; want clean", w, ok)
	}
	if _, ok := logs.Word(fillID(2)); ok {
		t.Fatal("Logs.Word knows an item with no record")
	}
}

// TestOpenLogsLockRules checks that a holder of the lock cuts the torn
// tail of both logs, and that a command without the lock changes no
// file. Both report each tail.
func TestOpenLogsLockRules(t *testing.T) {
	for _, holdsLock := range []bool{true, false} {
		dir := t.TempDir()
		logs, err := OpenLogs(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := logs.Items.EnsureStaged(fillID(1)); err != nil {
			t.Fatal(err)
		}
		appendEvents(t, logs.Discs, fillDisc(1), 100, EventPacked)
		for _, name := range []string{stateFileName, discStateFileName} {
			f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte{9, 9}); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}

		again, err := OpenLogs(dir, holdsLock)
		if err != nil {
			t.Fatal(err)
		}
		tails := again.TornTails()
		if len(tails) != 2 || tails[0].Name != "state log" || tails[1].Name != "disc state log" || tails[0].Bytes != 2 || tails[1].Bytes != 2 {
			t.Fatalf("holds lock %v: torn tails %+v", holdsLock, tails)
		}
		wantItems, wantDiscs := recordLen+2, discRecordLen+2
		if holdsLock {
			wantItems, wantDiscs = recordLen, discRecordLen
		}
		if got := len(itemFile(t, dir)); got != wantItems {
			t.Fatalf("holds lock %v: item log size %d, want %d", holdsLock, got, wantItems)
		}
		if got := len(readDiscFile(t, dir)); got != wantDiscs {
			t.Fatalf("holds lock %v: disc log size %d, want %d", holdsLock, got, wantDiscs)
		}
	}
}
