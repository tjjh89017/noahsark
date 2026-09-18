package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreIncludeRestoresOnlyThatPath runs a full sequence and checks
// that --include restores the named file and leaves its sibling out.
func TestRestoreIncludeRestoresOnlyThatPath(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

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
