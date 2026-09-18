package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackKeepsCrossDiscDedupAfterBurnAndVerify commits and packs one
// disc, burns and verifies it so its objects reach CLEAN, then commits a
// one-line change and packs again. The second pack must hold only the
// new objects: a CLEAN object must never be copied again or rebound to
// the new disc, and the first disc's object count in "disc list" must
// not change.
func TestPackKeepsCrossDiscDedupAfterBurnAndVerify(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	packAndVerifyDisc(t, work, repo, src)

	code, out := runCmd(t, "disc", "list", "--repo="+repo)
	if code != 0 {
		t.Fatalf("disc list (after first disc): exit %d: %s", code, out)
	}
	firstDiscLine, _, _ := strings.Cut(out, "\n")
	m := discListLineRe.FindStringSubmatch(firstDiscLine)
	if m == nil {
		t.Fatalf("disc line %q does not match the expected column order", firstDiscLine)
	}
	firstObjects := m[7]

	// Commit a one-line change: most objects (the unchanged file, every
	// tree above it) are unchanged content, already CLEAN.
	if err := os.WriteFile(filepath.Join(src, "new.txt"), []byte("one new line"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("second pack: exit %d: %s", code, packOut)
	}

	code, out = runCmd(t, "disc", "list", "--repo="+repo)
	if code != 0 {
		t.Fatalf("disc list (after second disc): exit %d: %s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("disc list output = %q, want two disc lines and the staged line", out)
	}
	m1 := discListLineRe.FindStringSubmatch(lines[0])
	if m1 == nil {
		t.Fatalf("disc line %q does not match the expected column order", lines[0])
	}
	if m1[7] != firstObjects {
		t.Fatalf("first disc objects = %q after the second pack, want unchanged %q: a CLEAN object was rebound", m1[7], firstObjects)
	}

	m2 := discListLineRe.FindStringSubmatch(lines[1])
	if m2 == nil {
		t.Fatalf("disc line %q does not match the expected column order", lines[1])
	}
	if m2[7] == "0" {
		t.Fatalf("second disc objects = %q, want nonzero for the new file's objects", m2[7])
	}
}

// TestPackWithNoRefCarriesEveryPendingDateRef commits twice, each with
// its own --ref=DATE naming a real label, and never touches LATEST.
// Packing with no --ref and no --snapshot must carry both pending refs
// rather than fail looking for a LATEST ref that was never created.
func TestPackWithNoRefCarriesEveryPendingDateRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-21", src); code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-22", src); code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack with no --ref: exit %d: %s", code, out)
	}
	if strings.Contains(out, `ref "LATEST" not found`) {
		t.Fatalf("pack output %q, must not fail looking for LATEST", out)
	}
}

// TestDiscBurnedUndoFlagAfterUUID checks that "disc burned UUID --undo"
// works, matching the corrected usage text: --undo before UUID.
func TestDiscBurnedUndoFlagBeforeUUID(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	discUUID := packedDiscUUID(t, packOut)

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "burned", "--undo", "--repo="+repo, discUUID)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	discUUID := packedDiscUUID(t, packOut)

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID)
	if code != 0 {
		t.Fatalf("disc burned (again): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "already burned, 0 objects to mark") {
		t.Fatalf("disc burned (again) output %q, want the already-burned line", out)
	}
}
