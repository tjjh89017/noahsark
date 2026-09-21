package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeEmptyObjectSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
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
	if code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, mounted); code != 0 {
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

// TestTwoCommitsOneDayMoveOneRefAndKeepBoth commits two times with no
// --ref. Both commits take the same ref name, the date of today, so the
// ref moves to the newer snapshot. The older snapshot must stay
// reachable: log still lists it.
func TestTwoCommitsOneDayMoveOneRefAndKeepBoth(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("first commit: exit %d: %s", code, out)
	}
	first := snapshotIDFromCommit(t, out)
	if err := os.WriteFile(filepath.Join(src, "second.txt"), []byte("content of the second commit"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	second := snapshotIDFromCommit(t, out)
	if first == second {
		t.Fatal("the two commits gave one snapshot id")
	}
	if !strings.Contains(out, "ref "+defaultRefName()+" -> "+second) {
		t.Fatalf("second commit output %q does not move the date ref to the newer snapshot", out)
	}

	code, logOut := runCmd(t, "log", "--repo="+repo)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, logOut)
	}
	if !strings.Contains(logOut, first) {
		t.Fatalf("log output %q does not reach the older snapshot %s", logOut, first)
	}
	if !strings.Contains(logOut, second) {
		t.Fatalf("log output %q does not name the newer snapshot %s", logOut, second)
	}
}
