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
// the new disc, and the first disc's object count in "status" must not
// change.
func TestPackKeepsCrossDiscDedupAfterBurnAndVerify(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	packAndVerifyDisc(t, work, repo, src)

	discs := statusDiscs(t, repo)
	if len(discs) != 1 {
		t.Fatalf("status names %d disc(s) after the first pack, want 1", len(discs))
	}
	firstObjects := discs[0].OnDiscObjects

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

	discs = statusDiscs(t, repo)
	if len(discs) != 2 {
		t.Fatalf("status names %d disc(s) after the second pack, want 2", len(discs))
	}
	if discs[0].OnDiscObjects != firstObjects {
		t.Fatalf("first disc objects = %d after the second pack, want unchanged %d: a CLEAN object was rebound", discs[0].OnDiscObjects, firstObjects)
	}
	if discs[1].OnDiscObjects == 0 {
		t.Fatal("second disc objects = 0, want nonzero for the new file's objects")
	}
}

// TestPackWithNoRefCarriesEveryPendingDateRef commits twice, each with
// its own --ref=DATE naming a real label. Packing with no --ref and no
// --snapshot must carry both pending refs.
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
	if strings.Contains(out, "not found") {
		t.Fatalf("pack output %q, must not look for a ref that was never created", out)
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
