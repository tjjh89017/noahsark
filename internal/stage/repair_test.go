package stage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// openTestLogs opens both logs in dir writable.
func openTestLogs(t *testing.T, dir string) *Logs {
	t.Helper()
	logs, err := OpenLogs(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	return logs
}

// packedDisc writes a disc with the items ids Packed on it, and the
// events after its Packed event.
func packedDisc(t *testing.T, logs *Logs, disc [16]byte, ids []object.ID, events ...DiscEvent) {
	t.Helper()
	if err := logs.Items.EnsureStaged(ids...); err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkPacked(1, disc, ids...); err != nil {
		t.Fatal(err)
	}
	appendEvents(t, logs.Discs, disc, 100, append([]DiscEvent{EventPacked}, events...)...)
}

func TestRepairsAfterEachEvent(t *testing.T) {
	ids := []object.ID{fillID(1), fillID(2)}
	index := func(disc [16]byte) ([]object.ID, uint64, error) { return ids, 7, nil }
	cases := []struct {
		name    string
		events  []DiscEvent
		prepare func(t *testing.T, logs *Logs, disc [16]byte)
		cmd     string
		want    State
		reason  Reason
	}{
		{name: "disc lost of a packed disc", events: []DiscEvent{EventLost}, cmd: "disc lost", want: Staged, reason: ReasonDiscLost},
		{name: "pack --undo", events: []DiscEvent{EventPackUndone}, cmd: "pack --undo", want: Staged, reason: ReasonPackUndone},
		{name: "gc", events: []DiscEvent{EventBurnRecorded, EventCheckOK, EventFreed}, cmd: "gc", want: OnDisc},
		{
			name: "disc lost of an on disc only disc", events: []DiscEvent{EventBurnRecorded, EventCheckOK, EventFreed},
			prepare: func(t *testing.T, logs *Logs, disc [16]byte) {
				if err := logs.Items.MarkOnDisc(ids...); err != nil {
					t.Fatal(err)
				}
				appendEvents(t, logs.Discs, disc, 200, EventLost)
			},
			cmd: "disc lost", want: Lost, reason: ReasonDiscLost,
		},
		{
			name: "disc lost --undo of an on disc only disc", events: []DiscEvent{EventBurnRecorded, EventCheckOK, EventFreed},
			prepare: func(t *testing.T, logs *Logs, disc [16]byte) {
				if err := logs.Items.MarkOnDisc(ids...); err != nil {
					t.Fatal(err)
				}
				if err := logs.Items.MarkLost(ids...); err != nil {
					t.Fatal(err)
				}
				appendEvents(t, logs.Discs, disc, 200, EventLost, EventLostUndone)
			},
			cmd: "disc lost --undo", want: OnDisc, reason: ReasonLostUndone,
		},
		{
			name: "disc lost --undo of a verified disc", events: []DiscEvent{EventBurnRecorded, EventCheckOK},
			prepare: func(t *testing.T, logs *Logs, disc [16]byte) {
				if err := logs.Items.MarkStaged(ReasonDiscLost, ids...); err != nil {
					t.Fatal(err)
				}
				appendEvents(t, logs.Discs, disc, 200, EventLost, EventLostUndone)
			},
			cmd: "disc lost --undo", want: Packed, reason: ReasonLostUndone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			disc := fillDisc(0xC3)
			logs := openTestLogs(t, dir)
			packedDisc(t, logs, disc, ids, tc.events...)
			if tc.prepare != nil {
				tc.prepare(t, logs, disc)
			}
			before := itemFile(t, dir)
			repairs, err := logs.Repairs(index)
			if err != nil {
				t.Fatal(err)
			}
			if len(repairs) != 1 || repairs[0].Command != tc.cmd || repairs[0].Items != len(ids) || repairs[0].Disc.UUID != disc {
				t.Fatalf("Repairs = %+v, want %d items of %s", repairs, len(ids), tc.cmd)
			}
			if string(itemFile(t, dir)) != string(before) {
				t.Fatal("Repairs wrote the item log")
			}
			done, err := logs.Complete(index)
			if err != nil || len(done) != 1 {
				t.Fatalf("Complete = %+v, %v", done, err)
			}
			again := openTestLogs(t, dir)
			for _, id := range ids {
				rec := mustState(t, again.Items, id, tc.want)
				if rec.Reason != tc.reason {
					t.Errorf("item %x: reason %d, want %d", id[:2], rec.Reason, tc.reason)
				}
			}
			if left, err := again.Repairs(index); err != nil || len(left) != 0 {
				t.Fatalf("Repairs after Complete = %+v, %v; want none", left, err)
			}
		})
	}
}

// TestRepairsLostUndoNeedsNoPackedItem checks that a burned disc that
// disc lost --undo gave back needs no repair once one item is Packed on
// it, also when the INDEX lists a Staged item: a later disc that took the
// item was lost.
func TestRepairsLostUndoNeedsNoPackedItem(t *testing.T) {
	dir := t.TempDir()
	disc := fillDisc(0xC4)
	logs := openTestLogs(t, dir)
	packedDisc(t, logs, disc, []object.ID{fillID(1)}, EventBurnRecorded, EventCheckOK, EventLost, EventLostUndone)
	if err := logs.Items.EnsureStaged(fillID(2)); err != nil {
		t.Fatal(err)
	}
	index := func([16]byte) ([]object.ID, uint64, error) { return []object.ID{fillID(1), fillID(2)}, 1, nil }
	if repairs, err := logs.Repairs(index); err != nil || len(repairs) != 0 {
		t.Fatalf("Repairs = %+v, %v; want none", repairs, err)
	}
}

func TestRepairsIndexError(t *testing.T) {
	dir := t.TempDir()
	disc := fillDisc(0xC5)
	logs := openTestLogs(t, dir)
	packedDisc(t, logs, disc, []object.ID{fillID(1)}, EventBurnRecorded, EventCheckOK)
	if err := logs.Items.MarkStaged(ReasonDiscLost, fillID(1)); err != nil {
		t.Fatal(err)
	}
	appendEvents(t, logs.Discs, disc, 200, EventLost, EventLostUndone)
	bad := errors.New("no catalog")
	if _, err := logs.Complete(func([16]byte) ([]object.ID, uint64, error) { return nil, 0, bad }); !errors.Is(err, bad) {
		t.Fatalf("Complete error %v, want %v", err, bad)
	}
}

func TestCompleteDiscOnlyTouchesItsDisc(t *testing.T) {
	dir := t.TempDir()
	a, b := fillDisc(0xD1), fillDisc(0xD2)
	logs := openTestLogs(t, dir)
	packedDisc(t, logs, a, []object.ID{fillID(1)}, EventLost)
	packedDisc(t, logs, b, []object.ID{fillID(2)}, EventLost)
	n, err := logs.CompleteDisc(a, nil)
	if err != nil || n != 1 {
		t.Fatalf("CompleteDisc = %d, %v; want 1", n, err)
	}
	mustState(t, logs.Items, fillID(1), Staged)
	mustState(t, logs.Items, fillID(2), Packed)
}

func TestMarkDetectsARollBack(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(t.TempDir(), "staging", MarkFileName)
	logs := openTestLogs(t, dir)
	if back, err := logs.UseMark(mark); err != nil || len(back) != 0 {
		t.Fatalf("UseMark with no mark = %+v, %v", back, err)
	}
	saved := itemFile(t, dir)
	packedDisc(t, logs, fillDisc(0xE1), []object.ID{fillID(1)})
	if data, err := os.ReadFile(mark); err != nil || string(data) != "state.db 2\ndiscstate.db 1\n" {
		t.Fatalf("mark after the appends = %q, %v", data, err)
	}

	// Put an old item log back, as a git checkout does.
	if err := os.WriteFile(filepath.Join(dir, stateFileName), saved, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, discStateFileName)); err != nil {
		t.Fatal(err)
	}
	for _, writable := range []bool{true, false} {
		old, err := OpenLogs(dir, writable)
		if err != nil {
			t.Fatal(err)
		}
		back, err := old.UseMark(mark)
		if err != nil || len(back) != 2 || back[0].Last != 0 || back[0].Marked != 2 || back[1].Last != 0 || back[1].Marked != 1 {
			t.Fatalf("UseMark after the roll back = %+v, %v", back, err)
		}
	}
	back, err := MarkRollbacks(dir, mark)
	if err != nil || len(back) != 2 {
		t.Fatalf("MarkRollbacks = %+v, %v", back, err)
	}
	if data, _ := os.ReadFile(mark); string(data) != "state.db 2\ndiscstate.db 1\n" {
		t.Fatalf("a roll back changed the mark: %q", data)
	}
}

func TestMarkBelowTheLogIsNoRollBack(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(t.TempDir(), MarkFileName)
	logs := openTestLogs(t, dir)
	packedDisc(t, logs, fillDisc(0xE2), []object.ID{fillID(1)})
	if err := os.WriteFile(mark, []byte("state.db 1\ndiscstate.db 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := openTestLogs(t, dir)
	if back, err := again.UseMark(mark); err != nil || len(back) != 0 {
		t.Fatalf("UseMark = %+v, %v; want no roll back", back, err)
	}
	if data, _ := os.ReadFile(mark); string(data) != "state.db 2\ndiscstate.db 1\n" {
		t.Fatalf("mark = %q, want the last sequences", data)
	}
	if back, err := MarkRollbacks(dir, mark); err != nil || len(back) != 0 {
		t.Fatalf("MarkRollbacks = %+v, %v", back, err)
	}
}

func TestMarkBadFile(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(t.TempDir(), MarkFileName)
	if err := os.WriteFile(mark, []byte("state.db x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openTestLogs(t, dir).UseMark(mark); err == nil {
		t.Fatal("UseMark accepts a bad mark")
	}
}

// TestReadOnlyUseMarkWritesNothing checks that a command without the
// lock never writes the mark.
func TestReadOnlyUseMarkWritesNothing(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(t.TempDir(), MarkFileName)
	logs, err := OpenLogs(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := logs.UseMark(mark); err != nil || len(back) != 0 {
		t.Fatalf("UseMark = %+v, %v", back, err)
	}
	if _, err := os.Stat(mark); !os.IsNotExist(err) {
		t.Fatalf("a read-only open wrote the mark: %v", err)
	}
}
