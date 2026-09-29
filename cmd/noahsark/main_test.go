package main

import (
	"path/filepath"
	"testing"
)

// TestFullSequence runs init, commit, pack, verify and restore in
// sequence against an unpacked tree, and compares the restored source
// with the original, byte for byte.
func TestFullSequence(t *testing.T) {
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

	if code, out := runCmd(t, "verify", treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	restoredDir := filepath.Join(work, "restored")
	if code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+treeDir, snapID, restoredDir); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	compareTrees(t, restoredDir, src)
}
