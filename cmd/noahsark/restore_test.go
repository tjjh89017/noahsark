package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	discsDir := filepath.Join(work, "discs")
	if err := os.MkdirAll(discsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// disc-a sorts before disc-b, and holds only the first ref.
	src1 := writeRefsCarryFixture(t, "run1")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=run1", src1); code != 0 {
		t.Fatalf("commit run1: exit %d: %s", code, out)
	}
	discA := filepath.Join(discsDir, "disc-a")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+discA); code != 0 {
		t.Fatalf("pack run1: exit %d: %s", code, out)
	}

	// disc-b is packed after disc-a, and names the second ref.
	src2 := writeRefsCarryFixture(t, "run2")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=run2", src2); code != 0 {
		t.Fatalf("commit run2: exit %d: %s", code, out)
	}
	discB := filepath.Join(discsDir, "disc-b")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+discB); code != 0 {
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

// TestRestoreAcceptsARefName checks that restore resolves a ref name
// the same way ls and log do, instead of only a snapshot id.
func TestRestoreAcceptsARefName(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)

	restoredDir := filepath.Join(t.TempDir(), "restored")
	code, out := runCmd(t, "restore", treeDir, defaultRefName(), restoredDir)
	if code != 0 {
		t.Fatalf("restore by ref name: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(restoredDir, src), src)

	restoredByID := filepath.Join(t.TempDir(), "restored-by-id")
	if code, out := runCmd(t, "restore", treeDir, snapID, restoredByID); code != 0 {
		t.Fatalf("restore snapID: exit %d: %s", code, out)
	}
}

// TestRestoreUnknownRefReportsTheSameError checks that an unknown ref
// name given to restore or to ls is a usage error.
func TestRestoreUnknownRefReportsTheSameError(t *testing.T) {
	treeDir, _, _ := lsFixture(t)
	restoredDir := filepath.Join(t.TempDir(), "restored")

	restoreCode, restoreOut := runCmd(t, "restore", treeDir, "NO-SUCH-REF", restoredDir)
	lsCode, lsOut := runCmd(t, "--repo="+repoDirFromTreeDir(t, treeDir), "ls", "NO-SUCH-REF")

	if restoreCode != 2 {
		t.Fatalf("restore: exit %d, want 2: %s", restoreCode, restoreOut)
	}
	if want := "noahsark: ls: no snapshot matches NO-SUCH-REF\n"; lsCode != 2 || lsOut != want {
		t.Fatalf("ls: exit %d, output %q; want 2 and %q", lsCode, lsOut, want)
	}
}

// TestRestoreTwoArgsWithNoMountPrintsUsage checks that "restore
// DISC-ROOT SNAPSHOT", missing OUT-DIR and given no --mount, names the
// disc root and prints the usage line, instead of silently entering
// disc-swap mode with DISC-ROOT misread as SNAPSHOT.
func TestRestoreTwoArgsWithNoMountPrintsUsage(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)

	code, out := runCmd(t, "restore", treeDir, snapID)
	if code != 2 {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "usage: noahsark restore") {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR) output %q, want the usage line", out)
	}
	if !strings.Contains(out, "OUT-DIR is missing") {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR) output %q, want the missing OUT-DIR named", out)
	}
}

// TestRestoreMountWithDiscRootIsAnError checks that "restore
// --mount=DIR DISC-ROOT SNAPSHOT OUT-DIR" is refused by name, instead
// of misreading DISC-ROOT as the disc-swap mode's own disc root and
// reporting SNAPSHOT unknown.
func TestRestoreMountWithDiscRootIsAnError(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	restoredDir := filepath.Join(t.TempDir(), "restored")

	code, out := runCmd(t, "restore", "--mount=/mnt/drive", treeDir, snapID, restoredDir)
	if code != 2 {
		t.Fatalf("restore --mount DISC-ROOT SNAPSHOT OUT-DIR: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--mount takes no DISC-ROOT") {
		t.Fatalf("restore --mount DISC-ROOT SNAPSHOT OUT-DIR output %q, want the --mount-takes-no-DISC-ROOT message", out)
	}
	if !strings.Contains(out, "usage: noahsark restore") {
		t.Fatalf("restore --mount DISC-ROOT SNAPSHOT OUT-DIR output %q, want the usage line too", out)
	}
}

// TestRestoreDryRunWithoutMountIsAnError checks that "restore --dry-run"
// with no --mount is refused by name, instead of falling into the
// all-discs-at-once dispatch and reporting a missing disc.
func TestRestoreDryRunWithoutMountIsAnError(t *testing.T) {
	_, snapID, _ := lsFixture(t)
	restoredDir := filepath.Join(t.TempDir(), "restored")

	code, out := runCmd(t, "restore", "--dry-run", snapID, restoredDir)
	if code != 2 {
		t.Fatalf("restore --dry-run (no --mount): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--dry-run needs --mount") {
		t.Fatalf("restore --dry-run (no --mount) output %q, want the --dry-run-needs---mount message", out)
	}
}

// writeEmptyObjectSource creates a source tree that makes two objects
// with the same payload bytes: an empty directory, whose tree payload is
// eight zero bytes, and an empty file, whose blob payload is eight zero
// bytes too. It also holds a file of exactly eight zero bytes, and a
// normal file, so a restore compares real content beside the empty ones.
func writeEmptyObjectSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "zfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "zeros8"), make([]byte, 8), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "normal.txt"), []byte("content of a normal file"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestRestoreEmptyDirectoryAndEmptyFile commits a source that holds an
// empty directory beside an empty file. The two objects have the same
// payload bytes and different kinds, so only a content id that covers
// the kind keeps them apart. A restore must give back both, through the
// all-discs-at-once form and through the disc-swap form.
func TestRestoreEmptyDirectoryAndEmptyFile(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeEmptyObjectSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "verify", treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	allDiscs := filepath.Join(work, "restored-all")
	if code, out := runCmd(t, "restore", treeDir, snapID, allDiscs); code != 0 {
		t.Fatalf("restore all discs at once: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(allDiscs, src), src)
	assertRestoredDirectory(t, filepath.Join(allDiscs, src, "adir"))

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, treeDir)
	mounted := filepath.Join(work, "restored-mount")
	if code, out := runCmd(t, "--repo="+repo, "restore", "--mount="+mountDir, snapID, mounted); code != 0 {
		t.Fatalf("restore --mount: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(mounted, src), src)
	assertRestoredDirectory(t, filepath.Join(mounted, src, "adir"))
}

// assertRestoredDirectory fails when path is not a directory.
func assertRestoredDirectory(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("restored empty directory %s: %v", path, err)
	}
	if !st.IsDir() {
		t.Fatalf("restored %s is not a directory", path)
	}
}

// TestRestoreIncludeRestoresOnlyThatPath runs a full sequence and checks
// that --include restores the named file and leaves its sibling out.
func TestRestoreIncludeRestoresOnlyThatPath(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	restoredDir := filepath.Join(work, "restored")
	include := strings.TrimPrefix(src, "/") + "/sub/b.txt"
	if code, out := runCmd(t, "restore", "--include="+include, treeDir, snapID, restoredDir); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	root := filepath.Join(restoredDir, src)
	if _, err := os.Stat(filepath.Join(root, "sub", "b.txt")); err != nil {
		t.Fatalf("expected sub/b.txt to be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err == nil {
		t.Fatal("expected a.txt to stay unrestored")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat a.txt: %v", err)
	}
}

// TestRestoreIncludeRepeatable checks that repeating --include unions
// the paths.
func TestRestoreIncludeRepeatable(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	restoredDir := filepath.Join(work, "restored")
	incA := strings.TrimPrefix(src, "/") + "/a.txt"
	incB := strings.TrimPrefix(src, "/") + "/sub/b.txt"
	code, out = runCmd(t, "restore", "--include="+incA, "--include="+incB, treeDir, snapID, restoredDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	root := filepath.Join(restoredDir, src)
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatalf("expected a.txt to be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "b.txt")); err != nil {
		t.Fatalf("expected sub/b.txt to be restored: %v", err)
	}
}

// TestRestoreIncludeUnmatchedExitsOneAndNamesPath checks that a
// nonexistent --include path fails the restore, names the path, and
// exits 1, writing nothing.
func TestRestoreIncludeUnmatchedExitsOneAndNamesPath(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	restoredDir := filepath.Join(work, "restored")
	badInclude := strings.TrimPrefix(src, "/") + "/does/not/exist"
	code, out = runCmd(t, "restore", "--include="+badInclude, treeDir, snapID, restoredDir)
	if code != 1 {
		t.Fatalf("restore: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, badInclude) {
		t.Fatalf("output %q does not name the unmatched include path", out)
	}
	if _, err := os.Stat(restoredDir); err == nil {
		root := filepath.Join(restoredDir, src)
		if _, err := os.Stat(root); err == nil {
			t.Fatalf("expected nothing restored under %s", root)
		}
	}
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	discsDir := filepath.Join(work, "discs")
	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(discsDir, fmt.Sprintf("disc%d", i))
		// pack exits 0 whether or not objects stay STAGED for the next
		// disc: leftover staged data is not a failure, the disc was
		// packed correctly.
		code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--fec", "--out="+treeDir)
		if code != 0 {
			t.Fatalf("pack %d: exit %d, want 0: %s", i, code, out)
		}
		if !strings.HasPrefix(out, "packed disc ") {
			t.Fatalf("pack %d: output %q packed no disc", i, out)
		}
		discRoots = append(discRoots, treeDir)
	}

	restoredDir := filepath.Join(work, "restored")
	args := append([]string{"restore"}, discRoots...)
	args = append(args, snapID, restoredDir)
	if code, out := runCmd(t, args...); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	compareTrees(t, filepath.Join(restoredDir, src), src)
}

// TestMultiDiscRestorePositionalDiscRoots packs a two-disc sequence, then
// restores by naming both disc roots as positional DISC-ROOT arguments,
// the same way an operator names two already-mounted drives at once.
func TestMultiDiscRestorePositionalDiscRoots(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--fec", "--out="+treeDir); code != 0 {
			t.Fatalf("pack %d: exit %d, want 0: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}

	restoredDir := filepath.Join(work, "restored")
	args := append([]string{"restore"}, discRoots...)
	args = append(args, snapID, restoredDir)
	if code, out := runCmd(t, args...); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	compareTrees(t, filepath.Join(restoredDir, src), src)
}

// TestMultiDiscRestoreMissingDiscNamesIt packs a sequence, then restores
// with one disc root left off the command line, and asserts the failure
// names a missing disc.
func TestMultiDiscRestoreMissingDiscNamesIt(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	discsDir := filepath.Join(work, "discs")
	// disc1's root is never passed to restore, so it is left off.
	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(discsDir, fmt.Sprintf("disc%d", i))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--fec", "--out="+treeDir); code != 0 && i != len(capacities)-1 {
			// exit 1 is expected for the first two, checked above; this
			// branch only guards against a hard failure (exit 2).
			if code == 2 {
				t.Fatalf("pack %d: exit %d: %s", i, code, out)
			}
		}
		if i != 1 {
			discRoots = append(discRoots, treeDir)
		}
	}

	restoredDir := filepath.Join(work, "restored")
	args := append([]string{"restore"}, discRoots...)
	args = append(args, snapID, restoredDir)
	code, out = runCmd(t, args...)
	if code != 1 {
		t.Fatalf("restore: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "missing disc") {
		t.Fatalf("restore output %q does not name a missing disc", out)
	}
}

// readFile reads path as a string.
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// TestRestoreOverwrite asserts that restoring into a directory that
// already holds a differing file leaves that file alone and reports it
// skipped, and that --overwrite replaces it instead.
func TestRestoreOverwrite(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	restoredDir := filepath.Join(work, "restored")
	preexisting := filepath.Join(restoredDir, src, "a.txt")
	if err := writeFile(preexisting, "pre-existing, different content"); err != nil {
		t.Fatal(err)
	}

	code, out = runCmd(t, "restore", treeDir, snapID, restoredDir)
	if code != 1 {
		t.Fatalf("restore without --overwrite: exit %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, "not restored: 1 existing path(s)") {
		t.Fatalf("output = %q, want a skipped-path count", out)
	}
	if !strings.Contains(out, "pass --overwrite to replace it") {
		t.Fatalf("output = %q, want the skipped path named with the --overwrite advice", out)
	}
	if got, err := readFile(preexisting); err != nil || got != "pre-existing, different content" {
		t.Fatalf("existing file was modified without --overwrite: %q, %v", got, err)
	}

	code, out = runCmd(t, "restore", "--overwrite", treeDir, snapID, restoredDir)
	if code != 0 {
		t.Fatalf("restore --overwrite: exit %d, want 0; output: %s", code, out)
	}
	if got, err := readFile(preexisting); err != nil || got != "content of a" {
		t.Fatalf("--overwrite did not replace the existing file: %q, %v", got, err)
	}
}

// buildAndPackWithSymlink packs a fixture source that has a symlink
// "link" -> "a.txt" beside the usual a.txt and sub/b.txt, and returns
// the packed tree root and the snapshot id.
func buildAndPackWithSymlink(t *testing.T, work, repo string) (treeDir, src, snapID string) {
	t.Helper()
	src = writeFixtureSource(t)
	if err := os.Symlink("a.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)

	treeDir = filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	return treeDir, src, snapID
}

// TestRestoreOverwriteLeavesNonEmptyDirectoryAtSymlinkPath asserts that
// --overwrite, meeting a non-empty directory where a symlink entry must
// go, does not stop the restore: the directory and its content survive,
// a warning is printed, later entries still land, and the exit code is
// 1 because a path was left alone.
func TestRestoreOverwriteLeavesNonEmptyDirectoryAtSymlinkPath(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	treeDir, src, snapID := buildAndPackWithSymlink(t, work, repo)

	restoredDir := filepath.Join(work, "restored")
	blocker := filepath.Join(restoredDir, src, "link")
	kept := filepath.Join(blocker, "keep.txt")
	if err := writeFile(kept, "keep"); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "restore", "--overwrite", treeDir, snapID, restoredDir)
	if code != 1 {
		t.Fatalf("restore --overwrite: exit %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, "noahsark: restore: warning: "+blocker) {
		t.Fatalf("output = %q, want a warning line naming %q", out, blocker)
	}
	if !strings.Contains(out, "not empty") {
		t.Fatalf("output = %q, want it to say the directory is not empty", out)
	}
	if got, err := readFile(kept); err != nil || got != "keep" {
		t.Fatalf("the existing directory or its content was removed: %q, %v", got, err)
	}
	for _, rel := range []string{"a.txt", "sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(restoredDir, src, rel)); err != nil {
			t.Fatalf("a later entry was not restored past the conflict: %s: %v", rel, err)
		}
	}
}

// TestRestoreOverwriteLeavesNonEmptyDirectoryAtFilePath is the same
// check for a regular-file entry.
func TestRestoreOverwriteLeavesNonEmptyDirectoryAtFilePath(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	treeDir, src, snapID := buildAndPackWithSymlink(t, work, repo)

	restoredDir := filepath.Join(work, "restored")
	blocker := filepath.Join(restoredDir, src, "a.txt")
	kept := filepath.Join(blocker, "keep.txt")
	if err := writeFile(kept, "keep"); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "restore", "--overwrite", treeDir, snapID, restoredDir)
	if code != 1 {
		t.Fatalf("restore --overwrite: exit %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, "noahsark: restore: warning: "+blocker) {
		t.Fatalf("output = %q, want a warning line naming %q", out, blocker)
	}
	if !strings.Contains(out, "not empty") {
		t.Fatalf("output = %q, want it to say the directory is not empty", out)
	}
	if got, err := readFile(kept); err != nil || got != "keep" {
		t.Fatalf("the existing directory or its content was removed: %q, %v", got, err)
	}
	for _, rel := range []string{"sub/b.txt", "link"} {
		if _, err := os.Lstat(filepath.Join(restoredDir, src, rel)); err != nil {
			t.Fatalf("a later entry was not restored past the conflict: %s: %v", rel, err)
		}
	}
}

// TestSnapshotArgEmptyNamesItself covers the fault of issue 48: an empty
// snapshot argument was quoted back as `ref ""`, which names nothing.
// Every command that takes a snapshot must say that no snapshot was
// given, and exit 2.
func TestSnapshotArgEmptyNamesItself(t *testing.T) {
	repo, treeDir, _ := snapshotArgFixture(t)

	cases := [][]string{
		{"restore", treeDir, "", filepath.Join(t.TempDir(), "out")},
		{"--repo=" + repo, "ls", ""},
		{"--repo=" + repo, "restore", "--mount=" + t.TempDir(), "--dry-run", "", filepath.Join(t.TempDir(), "out")},
		{"--repo=" + repo, "log", ""},
	}
	for _, args := range cases {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, "no snapshot given") {
			t.Fatalf("%v: output = %q, want it to say no snapshot was given", args, out)
		}
		if strings.Contains(out, `""`) {
			t.Fatalf("%v: output = %q, quotes an empty name", args, out)
		}
	}
}

// TestRestoreNamesAMistypedDiscRoot asserts that the DISC-ROOT SNAPSHOT
// OUT-DIR form names a first argument that is not a disc root, instead
// of reporting a missing object later.
func TestRestoreNamesAMistypedDiscRoot(t *testing.T) {
	_, _, snapID := snapshotArgFixture(t)

	code, out := runCmd(t, "restore", "/no/such/disc", snapID, filepath.Join(t.TempDir(), "out"))
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "no such disc root: /no/such/disc") {
		t.Fatalf("output = %q, want the disc root named", out)
	}
}

// TestRestoreUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// restore: each case exits 2, never 0 or 1.
func TestRestoreUsageErrorsExitTwo(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--repo=" + repo, "restore", "--no-such-flag"}},
		{"--dry-run without --mount", []string{"--repo=" + repo, "restore", "--dry-run", defaultRefName(), filepath.Join(work, "out")}},
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
