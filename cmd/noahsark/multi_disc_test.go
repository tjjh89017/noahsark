package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeMultiDiscFixtureSource creates a source tree of several
// subdirectories, each holding one file of pseudo-random content, sized
// so a small forced capacity has to split the commit across several
// runs.
func writeMultiDiscFixtureSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	rng := rand.New(rand.NewSource(42))
	for i := range 6 {
		dir := filepath.Join(src, fmt.Sprintf("sub%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 600_000)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// packSectors converts a byte budget to a --capacity sector count string.
func packSectors(bytes uint64) string {
	const sectorSize = 2048
	return strconv.FormatUint((bytes+sectorSize-1)/sectorSize, 10)
}

// TestMultiDiscPackAndRestore runs init, commit, three pack calls over
// small forced capacities and a multi-disc restore, entirely through
// run(). It asserts the exit code and the remaining-staged report at
// each pack, then that the restored tree matches the source byte for
// byte.
func TestMultiDiscPackAndRestore(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	var discRoots []string
	capacities := []string{packSectors(2_500_000), packSectors(2_500_000), packSectors(8_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--out="+treeDir)
		wantCode := 1
		if i == len(capacities)-1 {
			wantCode = 0
		}
		if code != wantCode {
			t.Fatalf("pack %d: exit %d, want %d: %s", i, code, wantCode, out)
		}
		if !strings.Contains(out, "remaining staged:") {
			t.Fatalf("pack %d: output %q missing the remaining-staged report", i, out)
		}
		discRoots = append(discRoots, treeDir)
	}

	restoredDir := filepath.Join(work, "restored")
	args := []string{"restore"}
	for _, r := range discRoots {
		args = append(args, "--disc="+r)
	}
	args = append(args, snapID, restoredDir)
	if code, out := runCmd(t, args...); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	compareTrees(t, filepath.Join(restoredDir, src), src)
}

// TestMultiDiscRestoreMissingDiscNamesIt packs a sequence, then restores
// with one disc root left out, and asserts the failure names a missing
// disc.
func TestMultiDiscRestoreMissingDiscNamesIt(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	var discRoots []string
	capacities := []string{packSectors(2_500_000), packSectors(2_500_000), packSectors(8_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--out="+treeDir); code != 0 && i != len(capacities)-1 {
			// exit 1 is expected for the first two, checked above; this
			// branch only guards against a hard failure (exit 2).
			if code == 2 {
				t.Fatalf("pack %d: exit %d: %s", i, code, out)
			}
		}
		discRoots = append(discRoots, treeDir)
	}

	restoredDir := filepath.Join(work, "restored")
	args := []string{"restore", "--disc=" + discRoots[0], "--disc=" + discRoots[2], snapID, restoredDir}
	code, out = runCmd(t, args...)
	if code != 1 {
		t.Fatalf("restore: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "missing disc") {
		t.Fatalf("restore output %q does not name a missing disc", out)
	}
}
