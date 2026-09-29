package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDiscBurnedThenVerifyReachesClean runs "disc burned", then verify:
// only after "disc burned" has moved the run's objects to BURNED can a
// passing verify move them on to CLEAN. "disc burned --undo" reverses
// that, back to PACKED, so a following verify again reports the disc
// not marked burned.
func TestDiscBurnedThenVerifyReachesClean(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID)
	if code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "marked burned") || strings.Contains(out, "marked burned, 0 objects") {
		t.Fatalf("disc burned output %q did not mark objects burned", out)
	}

	code, out = runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify (burned): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "object(s) verified on") || strings.Contains(out, "0 object(s) verified on") {
		t.Fatalf("verify (burned) output %q did not report the objects verified", out)
	}
	if strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (burned) output %q still warned about an unmarked disc", out)
	}

	// A second verify of the same disc is idempotent: every object is
	// already CLEAN, so nothing more is marked, and the CLEAN line does
	// not print at all, since no object was BURNED this time.
	code, out = runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify (mounted, second pass): exit %d: %s", code, out)
	}
	if strings.Contains(out, "object(s) verified on") {
		t.Fatalf("verify (mounted, second pass) output %q, want no verified-count line when nothing was BURNED", out)
	}
}

// TestDiscBurnedUndo marks a disc burned, then undoes it: the run's
// objects must return to PACKED, and a following verify must again
// report the disc as not marked burned rather than reaching CLEAN.
func TestDiscBurnedUndo(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "disc", "burned", "--undo", discUUID)
	if code != 0 {
		t.Fatalf("disc burned --undo: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "returned to packed") || strings.Contains(out, "returned to packed, 0 objects") {
		t.Fatalf("disc burned --undo output %q did not return objects to packed", out)
	}

	code, out = runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify (after undo): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (after undo) output %q, want the not-marked-burned line again", out)
	}
}

// TestDiscBurnedUndoRefusedOnceClean checks that "disc burned --undo"
// refuses, and changes nothing, once verify has already moved a disc's
// objects on to CLEAN: a verified disc cannot be returned to packed.
func TestDiscBurnedUndoRefusedOnceClean(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "disc", "burned", "--undo", discUUID)
	if code != 1 {
		t.Fatalf("disc burned --undo (verified disc): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "is verified and cannot be returned to packed") {
		t.Fatalf("disc burned --undo output %q missing the verified-disc refusal", out)
	}

	// Nothing changed: a following verify still reports every object
	// CLEAN, not reset to PACKED.
	code, out = runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify (after refused undo): exit %d: %s", code, out)
	}
	if strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (after refused undo) output %q, want the disc still burned and clean", out)
	}
}

// TestDiscBurnedBySeqAndUUIDPrefix checks that "disc burned" accepts
// the disc number and an upper case uuid prefix with a hyphen in place
// of the full uuid.
func TestDiscBurnedBySeqAndUUIDPrefix(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--label=spare-1"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "disc", "burned", "0")
	if code != 0 {
		t.Fatalf("disc burned 0 (by seq): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "marked burned") || strings.Contains(out, "marked burned, 0 objects") {
		t.Fatalf("disc burned 0 output %q did not mark objects burned", out)
	}

	prefix := strings.ToUpper(defaultDiscUUID(t, repo, 0)[:13])
	code, out = runCmd(t, "--repo="+repo, "disc", "burned", "--undo", prefix)
	if code != 0 {
		t.Fatalf("disc burned --undo %s (by uuid prefix): exit %d: %s", prefix, code, out)
	}
	if !strings.Contains(out, "returned to packed") || strings.Contains(out, "returned to packed, 0 objects") {
		t.Fatalf("disc burned --undo (by uuid prefix) output %q did not return objects to packed", out)
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
	t.Fatalf("status --json names no disc %d", seq)
	return ""
}

// TestDiscBurnedUndoFlagAfterUUID checks that "disc burned UUID --undo"
// works, matching the corrected usage text: --undo before UUID.
func TestDiscBurnedUndoFlagBeforeUUID(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	discUUID := packedDiscUUID(t, packOut)

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "disc", "burned", "--undo", discUUID)
	if code != 0 {
		t.Fatalf("disc burned --undo UUID: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "undo: returned to packed") {
		t.Fatalf("disc burned --undo output %q, want the undo line", out)
	}
}

// TestDiscBurnedAlreadyBurnedReportsZero checks that running "disc
// burned" again on an already-burned disc prints the already-burned
// line and exits 0, rather than "marked burned, 0 objects".
func TestDiscBurnedAlreadyBurnedReportsZero(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	discUUID := packedDiscUUID(t, packOut)

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID)
	if code != 0 {
		t.Fatalf("disc burned (again): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "already burned, 0 objects to mark") {
		t.Fatalf("disc burned (again) output %q, want the already-burned line", out)
	}
}

// TestDiscBurnedUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// disc burned: each case exits 2, never 0 or 1.
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
