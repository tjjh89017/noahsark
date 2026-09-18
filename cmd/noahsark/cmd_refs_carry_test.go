package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRefsCarryFixture writes a small source tree whose content depends
// on tag, so two calls produce two distinct commits.
func writeRefsCarryFixture(t *testing.T, tag string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("content of "+tag), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestPackCarriesEveryPendingRef commits three refs, then packs once
// naming only the last one on the command line. The single run must
// still carry all three: OPERATIONS.md's pack rule 2 copies every local
// ref record with run_seq 0 into the run it packs, not only the ref
// named by --ref.
func TestPackCarriesEveryPendingRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	srcByRef := make(map[string]string)
	for _, name := range []string{"A", "B", "C"} {
		src := writeRefsCarryFixture(t, name)
		srcByRef[name] = src
		if code, out := runCmd(t, "commit", "--repo="+repo, "--ref="+name, src); code != 0 {
			t.Fatalf("commit %s: exit %d: %s", name, code, out)
		}
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=C", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "log", treeDir)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	for _, name := range []string{"A", "B", "C"} {
		if !strings.Contains(out, name) {
			t.Fatalf("log output does not mention ref %s: %s", name, out)
		}
	}

	outDir := filepath.Join(work, "out-b")
	if code, out := runCmd(t, "restore", treeDir, "B", outDir); code != 0 {
		t.Fatalf("restore B: exit %d: %s", code, out)
	}
	restored := filepath.Join(outDir, srcByRef["B"], "a.txt")
	if _, err := os.Stat(restored); err != nil {
		t.Fatalf("restored file for ref B missing: %v", err)
	}
}

// TestRestoreDiscsDirWrongOrderFindsEveryRef commits two refs and packs
// each one onto its own disc, then restores the second ref by
// --discs-dir, with the disc that does not hold that ref's own snapshot
// sorting first. Before pack carried every known ref forward, and
// before Refs merged every provided disc's REFS, this failed with "is
// not on the provided disc(s)" whenever the disc holding the wanted ref
// did not sort first.
func TestRestoreDiscsDirWrongOrderFindsEveryRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	discsDir := filepath.Join(work, "discs")
	if err := os.MkdirAll(discsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// disc-a sorts before disc-b, and holds only the first ref.
	src1 := writeRefsCarryFixture(t, "run1")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=run1", src1); code != 0 {
		t.Fatalf("commit run1: exit %d: %s", code, out)
	}
	discA := filepath.Join(discsDir, "disc-a")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=run1", "--out="+discA); code != 0 {
		t.Fatalf("pack run1: exit %d: %s", code, out)
	}

	// disc-b is packed after disc-a, and names the second ref.
	src2 := writeRefsCarryFixture(t, "run2")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=run2", src2); code != 0 {
		t.Fatalf("commit run2: exit %d: %s", code, out)
	}
	discB := filepath.Join(discsDir, "disc-b")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=run2", "--out="+discB); code != 0 {
		t.Fatalf("pack run2: exit %d: %s", code, out)
	}

	// os.ReadDir, which resolveDiscRoots uses for --discs-dir, lists
	// disc-a before disc-b: the disc that does not hold run2's own
	// snapshot object is the one restore reads first.
	outDir := filepath.Join(work, "out")
	code, out := runCmd(t, "restore", "--discs-dir="+discsDir, "run2", outDir)
	if code != 0 {
		t.Fatalf("restore run2 by --discs-dir: exit %d: %s", code, out)
	}
	restored := filepath.Join(outDir, src2, "a.txt")
	data, err := os.ReadFile(restored)
	if err != nil {
		t.Fatalf("restored file missing: %v", err)
	}
	if string(data) != "content of run2" {
		t.Fatalf("restored content %q, want %q", data, "content of run2")
	}

	// run1 must still resolve too: REFS on the newest disc carries
	// every ref, not only the one packed onto it.
	outDir1 := filepath.Join(work, "out1")
	if code, out := runCmd(t, "restore", "--discs-dir="+discsDir, "run1", outDir1); code != 0 {
		t.Fatalf("restore run1 by --discs-dir: exit %d: %s", code, out)
	}
}
