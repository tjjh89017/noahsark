package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestDiscBurnedThenVerifyReachesClean runs "disc burned", then verify:
// the disc goes from packed to burned to verified, and a second verify
// logs one more check and changes no state.
func TestDiscBurnedThenVerifyReachesClean(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)

	out := fx.mustRun(t, "disc", "burned", fx.uuid)
	if !strings.Contains(out, fx.name()+": burn recorded\n") {
		t.Fatalf("disc burned output %q, want the burn-recorded line", out)
	}
	if words := itemWords(t, fx.repo, fx.uuid); len(words) != 1 || words[stage.WordBurned] == 0 {
		t.Fatalf("item words %v after disc burned, want every item burned", words)
	}

	out = fx.mustRun(t, "verify", fx.root)
	if !strings.Contains(out, "\nverified\n") {
		t.Fatalf("verify (burned) output %q, want the verified line", out)
	}
	if words := itemWords(t, fx.repo, fx.uuid); len(words) != 1 || words[stage.WordClean] == 0 {
		t.Fatalf("item words %v after verify, want every item clean", words)
	}

	out = fx.mustRun(t, "verify", fx.root)
	if !strings.Contains(out, "already verified; check logged") {
		t.Fatalf("verify (second pass) output %q, want the check-logged line", out)
	}
	if d := discState(t, fx.repo, fx.uuid); d.State != stage.DiscVerified {
		t.Fatalf("disc state %s, want verified", d.State)
	}
}

// TestDiscBurnedUndo records a burn, then removes it: the disc returns
// to packed, and a following verify records the burn and the verified
// record together.
func TestDiscBurnedUndo(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)

	out := fx.mustRun(t, "--yes", "disc", "burned", "--undo", fx.uuid)
	if !strings.Contains(out, fx.name()+": burn record removed\n") {
		t.Fatalf("disc burned --undo output %q, want the burn-record-removed line", out)
	}
	if d := discState(t, fx.repo, fx.uuid); d.State != stage.DiscPacked {
		t.Fatalf("disc state %s after the undo, want packed", d.State)
	}

	out = fx.mustRun(t, "verify", fx.root)
	if !strings.Contains(out, "burn recorded; verified") {
		t.Fatalf("verify (after undo) output %q, want the burn-recorded line", out)
	}
}

// TestDiscBurnedUndoRefusedOnceClean checks that "disc burned --undo"
// refuses, asks nothing, and changes nothing, once a verify has made
// the disc verified.
func TestDiscBurnedUndoRefusedOnceClean(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	before := discLogBytes(t, fx.repo)

	code, out := fx.run(t, "--yes", "disc", "burned", "--undo", fx.uuid)
	if code != 1 {
		t.Fatalf("disc burned --undo (verified disc): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "disc 0 is not burned") || strings.Contains(out, "warning:") {
		t.Fatalf("disc burned --undo output %q, want the not-burned refusal and no warning", out)
	}
	if string(discLogBytes(t, fx.repo)) != string(before) {
		t.Fatal("a refused undo wrote the disc state log")
	}
}

// TestDiscBurnedBySeqAndUUIDPrefix checks that "disc burned" accepts
// the disc number and an upper case uuid prefix with a hyphen in place
// of the full uuid.
func TestDiscBurnedBySeqAndUUIDPrefix(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)

	out := fx.mustRun(t, "disc", "burned", "0")
	if !strings.Contains(out, "burn recorded") {
		t.Fatalf("disc burned 0 output %q, want the burn-recorded line", out)
	}

	prefix := strings.ToUpper(defaultDiscUUID(t, fx.repo, 0)[:13])
	out = fx.mustRun(t, "--yes", "disc", "burned", "--undo", prefix)
	if !strings.Contains(out, "burn record removed") {
		t.Fatalf("disc burned --undo %s output %q, want the burn-record-removed line", prefix, out)
	}
}

// defaultDiscUUID returns the uuid text of disc seq from "status".
func defaultDiscUUID(t *testing.T, repo string, seq uint64) string {
	t.Helper()
	for _, r := range statusDiscs(t, repo) {
		if r.Seq == seq {
			return r.UUID
		}
	}
	t.Fatalf("status names no disc %d", seq)
	return ""
}

// TestDiscBurnedUndoFlagBeforeUUID checks that --undo comes before the
// DISC argument, as the usage text gives.
func TestDiscBurnedUndoFlagBeforeUUID(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)

	out := fx.mustRun(t, "--yes", "disc", "burned", "--undo", fx.uuid)
	if !strings.Contains(out, "burn record removed") {
		t.Fatalf("disc burned --undo output %q, want the undo line", out)
	}
	if code, out := fx.run(t, "disc", "burned", fx.uuid, "--undo"); code != 2 {
		t.Fatalf("disc burned DISC --undo: exit %d, want 2: %s", code, out)
	}
}

// TestDiscBurnedAlreadyBurnedIsRefused checks that "disc burned" of a
// burned disc refuses with exit code 1 and writes nothing.
func TestDiscBurnedAlreadyBurnedIsRefused(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	before := discLogBytes(t, fx.repo)

	code, out := fx.run(t, "disc", "burned", fx.uuid)
	if code != 1 {
		t.Fatalf("disc burned (again): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "disc 0 already has a burn record") {
		t.Fatalf("disc burned (again) output %q, want the already-burned refusal", out)
	}
	if strings.Contains(out, nextStatusLine) {
		t.Fatalf("disc burned (again) output %q, want no next line after a refusal", out)
	}
	if string(discLogBytes(t, fx.repo)) != string(before) {
		t.Fatal("a refused disc burned wrote the disc state log")
	}
}

// TestDiscBurnedUsageErrorsExitTwo checks the usage-error convention of
// the exit code registry for disc burned: each case exits 2, never 0 or
// 1.
func TestDiscBurnedUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"missing DISC", []string{"--repo=" + repo, "disc", "burned"}},
		{"two DISC arguments", []string{"--repo=" + repo, "disc", "burned", "0", "1"}},
		{"no disc matches", []string{"--repo=" + repo, "disc", "burned", "0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runCmd(t, c.args...)
			if code != 2 {
				t.Fatalf("args %v: exit %d, want 2: %s", c.args, code, out)
			}
		})
	}
}

// TestResolveDiscHidesAnUndoneDisc checks that an undone disc matches no
// disc argument, also when its ledger row is still there.
func TestResolveDiscHidesAnUndoneDisc(t *testing.T) {
	dir := t.TempDir()
	logs, err := stage.OpenLogs(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	undone := [16]byte{0xAA, 1}
	kept := [16]byte{0xBB, 2}
	events := []stage.DiscRecord{
		{TimeSec: 1, DiscUUID: undone, Event: stage.EventPacked, DiscSeq: 0, RunSeq: 1},
		{TimeSec: 2, DiscUUID: undone, Event: stage.EventPackUndone},
		{TimeSec: 3, DiscUUID: kept, Event: stage.EventPacked, DiscSeq: 1, RunSeq: 2},
	}
	if err := logs.Discs.Append(events...); err != nil {
		t.Fatal(err)
	}
	rows := []format.DiscsRow{{DiscSeq: 0, RunSeq: 1, DiscUUID: undone}, {DiscSeq: 1, RunSeq: 2, DiscUUID: kept}}

	for _, arg := range []string{"0", "aa01"} {
		if _, err := resolveDisc(rows, logs.Discs, arg); err == nil || err.Error() != "no disc matches "+arg+"; noahsark status lists the discs" {
			t.Fatalf("resolveDisc(%q) error = %v, want no match", arg, err)
		}
	}
	if got, err := resolveDisc(rows, logs.Discs, "1"); err != nil || got != kept {
		t.Fatalf("resolveDisc(1) = %x, %v; want the kept disc", got, err)
	}
}
