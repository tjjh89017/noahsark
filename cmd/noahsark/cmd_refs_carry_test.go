package main

import (
	"os"
	"path/filepath"
	"strconv"
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

// TestPackCarriesEveryPendingRef commits three refs, then packs once.
// The single run must carry all three: pack copies every local ref
// record with run_seq 0 into the run it packs; it never narrows which
// refs a run carries.
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
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
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

// TestRestoreDiscRootWrongOrderFindsEveryRef commits two refs and packs
// each one onto its own disc, then restores the second ref by naming
// both disc roots, with the disc that does not hold that ref's own
// snapshot listed first. Before pack carried every known ref forward,
// and before Refs merged every provided disc's REFS, this failed with
// "is not on the provided disc(s)" whenever the disc holding the wanted
// ref was not listed first.
func TestRestoreDiscRootWrongOrderFindsEveryRef(t *testing.T) {
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
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+discA); code != 0 {
		t.Fatalf("pack run1: exit %d: %s", code, out)
	}

	// disc-b is packed after disc-a, and names the second ref.
	src2 := writeRefsCarryFixture(t, "run2")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=run2", src2); code != 0 {
		t.Fatalf("commit run2: exit %d: %s", code, out)
	}
	discB := filepath.Join(discsDir, "disc-b")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+discB); code != 0 {
		t.Fatalf("pack run2: exit %d: %s", code, out)
	}

	// disc-a is listed before disc-b: the disc that does not hold run2's
	// own snapshot object is the one restore reads first.
	outDir := filepath.Join(work, "out")
	code, out := runCmd(t, "restore", discA, discB, "run2", outDir)
	if code != 0 {
		t.Fatalf("restore run2 by disc root: exit %d: %s", code, out)
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
	if code, out := runCmd(t, "restore", discA, discB, "run1", outDir1); code != 0 {
		t.Fatalf("restore run1 by disc root: exit %d: %s", code, out)
	}
}

// TestPackObjectCountMatchesBurnedAndVerify packs two discs, the second
// carrying run1's snapshot object forward for disc-b's own
// self-description. pack's own object count for disc-b must equal the
// count disc burned marks and the count verify reports for that same
// disc: the carried snapshot object already belongs to disc-a, so it is
// not disc-b's own object, in any of the three commands.
func TestPackObjectCountMatchesBurnedAndVerify(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	src1 := writeRefsCarryFixture(t, "run1")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=run1", src1); code != 0 {
		t.Fatalf("commit run1: exit %d: %s", code, out)
	}
	discA := filepath.Join(work, "disc-a")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+discA); code != 0 {
		t.Fatalf("pack run1: exit %d: %s", code, out)
	}

	src2 := writeRefsCarryFixture(t, "run2")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=run2", src2); code != 0 {
		t.Fatalf("commit run2: exit %d: %s", code, out)
	}
	discB := filepath.Join(work, "disc-b")
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+discB)
	if code != 0 {
		t.Fatalf("pack run2: exit %d: %s", code, out)
	}
	m := packedDiscRe.FindStringSubmatch(strings.SplitN(out, "\n", 2)[0])
	if m == nil {
		t.Fatalf("pack run2: first line of %q is not a packed-disc line", out)
	}
	packedCount, err := strconv.Atoi(m[3])
	if err != nil {
		t.Fatalf("pack run2: object count %q does not parse: %v", m[3], err)
	}

	// disc-b is the second, and only the second, disc this repository has
	// ever packed, so its disc_seq is 1.
	code, out = runCmd(t, "disc", "burned", "--repo="+repo, "1")
	if code != 0 {
		t.Fatalf("disc burned disc-b: exit %d: %s", code, out)
	}
	if !strings.Contains(out, strconv.Itoa(packedCount)+" object(s) marked") {
		t.Fatalf("disc burned output %q does not mark the %d object(s) pack reported", out, packedCount)
	}

	code, out = runCmd(t, "verify", "--repo="+repo, discB)
	if code != 0 {
		t.Fatalf("verify disc-b: exit %d: %s", code, out)
	}
	if !strings.Contains(out, strconv.Itoa(packedCount)+" object(s) verified") {
		t.Fatalf("verify output %q does not verify the %d object(s) pack reported", out, packedCount)
	}
}
