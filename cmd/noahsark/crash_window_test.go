package main

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// logSummary is what a test compares between a run that stopped and a
// run with no stop: the state of the disc, and the number of items in
// each state and reason, in the whole log and on the disc.
type logSummary struct {
	Disc   stage.DiscState
	All    map[string]int
	OnDisc map[string]int
}

func (s logSummary) String() string {
	keys := func(m map[string]int) string {
		var parts []string
		for _, k := range slices.Sorted(maps.Keys(m)) {
			parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
		}
		return strings.Join(parts, " ")
	}
	return fmt.Sprintf("disc %s; all: %s; on the disc: %s", s.Disc, keys(s.All), keys(s.OnDisc))
}

// summarize reads the logs of the repository of fx.
func summarize(t *testing.T, fx *discFixture) logSummary {
	t.Helper()
	logs := readLogs(t, fx.repo)
	u := fx.uuidBytes(t)
	s := logSummary{All: map[string]int{}, OnDisc: map[string]int{}}
	d, _ := logs.Discs.Disc(u)
	s.Disc = d.State
	for _, st := range []stage.State{stage.Staged, stage.Packed, stage.OnDisc, stage.Lost} {
		for _, id := range logs.Items.IDsInState(st) {
			rec, _ := logs.Items.Get(id)
			key := fmt.Sprintf("%s/%d", st, rec.Reason)
			s.All[key]++
			if st != stage.Staged && rec.DiscUUID == u {
				s.OnDisc[key]++
			}
		}
	}
	return s
}

// appendEventOnly appends the event e of the disc of fx, and nothing
// else: the state that a command leaves when it stops after its first
// write.
func appendEventOnly(t *testing.T, fx *discFixture, e stage.DiscEvent) {
	t.Helper()
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Discs.Append(discEvent(fakeNow(), fx.uuidBytes(t), e)); err != nil {
		t.Fatal(err)
	}
}

// completeByHand appends the event e of the disc of fx, then its item
// records: the state that a command leaves when it stops after its
// second write, before it removes a file.
func completeByHand(t *testing.T, fx *discFixture, e stage.DiscEvent) {
	t.Helper()
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	u := fx.uuidBytes(t)
	if err := logs.Discs.Append(discEvent(fakeNow(), u, e)); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.CompleteDisc(u, catalogIndexItems(fx.repo), catalogHolds(fx.repo)); err != nil {
		t.Fatal(err)
	}
}

// repairCommands are commands that take the lock. Each one completes
// the item records of a command that stopped after its disc event.
var repairCommands = [][]string{
	{"gc"},
	{"commit", "{SRC}"},
	{"--force-yes", "disc", "burned", "{UUID}"},
}

// crashCase is one command, the event that it writes first, and the
// state that the fixture starts in.
type crashCase struct {
	name  string
	start stage.DiscState
	// lost marks the disc lost with disc lost before the command.
	lost  bool
	event stage.DiscEvent
	args  []string
}

var crashCases = []crashCase{
	{"disc lost of a packed disc", stage.DiscPacked, false, stage.EventLost, []string{"--force-yes", "disc", "lost", "{UUID}"}},
	{"disc lost of a verified disc", stage.DiscVerified, false, stage.EventLost, []string{"--force-yes", "disc", "lost", "{UUID}"}},
	{"disc lost of an on disc only disc", stage.DiscOnDiscOnly, false, stage.EventLost, []string{"--force-yes", "disc", "lost", "{UUID}"}},
	{"disc lost --undo of a verified disc", stage.DiscVerified, true, stage.EventLostUndone, []string{"--yes", "disc", "lost", "--undo", "{UUID}"}},
	{"disc lost --undo of an on disc only disc", stage.DiscOnDiscOnly, true, stage.EventLostUndone, []string{"--yes", "disc", "lost", "--undo", "{UUID}"}},
	{"pack --undo", stage.DiscPacked, false, stage.EventPackUndone, []string{"--yes", "pack", "--undo", "{UUID}"}},
	{"gc", stage.DiscVerified, false, stage.EventFreed, []string{"gc"}},
}

// crashCommand is the command name that the repair note gives for the
// arguments args of a crash case: the arguments with no answer flag and
// no disc argument.
func crashCommand(args []string) string {
	var words []string
	for _, a := range args {
		switch a {
		case "--yes", "--force-yes", "{UUID}":
			continue
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

// crashFixture builds the start state of c.
func crashFixture(t *testing.T, c crashCase) *discFixture {
	t.Helper()
	fx := repoWithDisc(t, c.start)
	if c.lost {
		fx.mustRun(t, "--force-yes", "disc", "lost", fx.uuid)
	}
	return fx
}

func fill(fx *discFixture, args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = fx.filler()(a)
	}
	return out
}

// TestCrashAfterTheDiscEvent stops each command that writes a disc event
// and item records after its first write: the event. Each command that
// takes the lock then writes the item records, and prints one note. The
// state then equals the state of a run with no stop.
func TestCrashAfterTheDiscEvent(t *testing.T) {
	for _, c := range crashCases {
		for _, next := range repairCommands {
			t.Run(c.name+", then "+strings.Join(next, " "), func(t *testing.T) {
				want := crashFixture(t, c)
				want.mustRun(t, fill(want, c.args)...)
				wantCode, _ := want.run(t, fill(want, next)...)

				got := crashFixture(t, c)
				appendEventOnly(t, got, c.event)
				gotCode, out := got.run(t, fill(got, next)...)
				if gotCode != wantCode {
					t.Fatalf("%v after the stop: exit %d, want %d: %s", next, gotCode, wantCode, out)
				}
				if !strings.Contains(out, "stopped before it wrote the records of its items") {
					t.Errorf("%v after the stop: no repair note: %s", next, out)
				}
				if g, w := summarize(t, got), summarize(t, want); g.String() != w.String() {
					t.Errorf("after the repair:\n got  %s\n want %s", g, w)
				}
			})
		}
	}
}

// TestCrashAfterTheItemRecords stops each command after its second
// write: the item records. The logs already equal the logs of a run with
// no stop, and the next command prints no repair note.
func TestCrashAfterTheItemRecords(t *testing.T) {
	for _, c := range crashCases {
		t.Run(c.name, func(t *testing.T) {
			want := crashFixture(t, c)
			want.mustRun(t, fill(want, c.args)...)

			got := crashFixture(t, c)
			completeByHand(t, got, c.event)
			if g, w := summarize(t, got), summarize(t, want); g.String() != w.String() {
				t.Errorf("after the item records:\n got  %s\n want %s", g, w)
			}
			if _, out := got.run(t, "gc"); strings.Contains(out, "stopped before it wrote") {
				t.Errorf("gc printed a repair note with nothing to repair: %s", out)
			}
		})
	}
}

// TestCrashAfterTheDiscEventLogRepairs checks the data of the status
// advice: logRepairs names the disc and the command that stopped, and
// writes nothing.
func TestCrashAfterTheDiscEventLogRepairs(t *testing.T) {
	for _, c := range crashCases {
		t.Run(c.name, func(t *testing.T) {
			fx := crashFixture(t, c)
			appendEventOnly(t, fx, c.event)
			logs := readLogs(t, fx.repo)
			repairs, err := logRepairs(testLayout(t, fx.repo), logs)
			if err != nil {
				t.Fatal(err)
			}
			wantCmd := crashCommand(c.args)
			if len(repairs) != 1 || repairs[0].Disc.UUID != fx.uuidBytes(t) || repairs[0].Command != wantCmd || repairs[0].Items == 0 {
				t.Fatalf("repairs %+v, want one repair of %s for the disc", repairs, wantCmd)
			}
			again, err := logRepairs(testLayout(t, fx.repo), readLogs(t, fx.repo))
			if err != nil || len(again) != 1 {
				t.Fatalf("logRepairs changed the logs: %+v, %v", again, err)
			}
		})
	}
}

// TestPackAfterAStoppedPackUndo is the case of the review: pack --undo
// stops after its event. The next pack writes the item records, removes
// the files of the undone disc, and packs the items on disc 1. Disc 1 does
// not name disc 0.
func TestPackAfterAStoppedPackUndo(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	u := fx.uuidBytes(t)
	appendEventOnly(t, fx, stage.EventPackUndone)
	out := fx.mustRun(t, "pack", "--capacity=64MiB")
	if !strings.Contains(out, "packed disc 1 ") || !strings.Contains(out, "an earlier pack --undo stopped before it wrote the records of its items") {
		t.Fatalf("pack output %q, want the repair note and disc 1", out)
	}
	wantUndoneDiscGone(t, fx.repo, u)
	for _, r := range ledgerRows(t, fx.repo) {
		if r.DiscSeq == 0 {
			t.Errorf("the ledger names disc 0: %s", uuidText(r.DiscUUID))
		}
	}
}

// TestLostUndoAfterAStoppedDiscLost stops disc lost after its event. A
// repeat of disc lost refuses, and disc lost --undo then gives each item
// back to the disc.
func TestLostUndoAfterAStoppedDiscLost(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	countItemsSetup(t, fx)
	appendEventOnly(t, fx, stage.EventLost)
	code, out := fx.run(t, "--force-yes", "disc", "lost", fx.uuid)
	if code != 1 || !strings.Contains(out, "already marked lost") || !strings.Contains(out, "an earlier disc lost stopped") {
		t.Fatalf("disc lost again: exit %d, want 1, the repair note and the refusal: %s", code, out)
	}
	if n := countByState(t, fx.repo, stage.Packed); n != 0 {
		t.Fatalf("%d item(s) Packed on the lost disc after the repair", n)
	}
	out = fx.mustRun(t, "--yes", "disc", "lost", "--undo", fx.uuid)
	if !strings.Contains(out, fmt.Sprintf("%d item(s) back on this disc", itemCount(t, fx))) {
		t.Fatalf("disc lost --undo: %s", out)
	}
}

// TestDiscLostKeepsThePlanDirectoryAfterAStop checks the third write of
// disc lost: a stop before the removal of the plan directory leaves the
// directory, and the logs are complete.
func TestDiscLostKeepsThePlanDirectoryAfterAStop(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	completeByHand(t, fx, stage.EventLost)
	if _, err := os.Stat(testLayout(t, fx.repo).planDir(fx.uuidBytes(t))); err != nil {
		t.Fatalf("plan directory: %v", err)
	}
	if n := countByState(t, fx.repo, stage.Packed); n != 0 {
		t.Fatalf("%d item(s) Packed after the item records", n)
	}
}
