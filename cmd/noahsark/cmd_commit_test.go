package main

import (
	"path/filepath"
	"regexp"
	"testing"
)

var stagedLineRe = regexp.MustCompile(`staged: (\d+) objects, (\d+) bytes`)

// TestCommitPrintsStagedTotals checks that commit prints the
// repository-wide staged total after its own summary, and that the
// total drops to zero once everything staged has been packed.
func TestCommitPrintsStagedTotals(t *testing.T) {
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
	m := stagedLineRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("commit output %q has no staged line", out)
	}
	if m[1] == "0" || m[2] == "0" {
		t.Fatalf("staged line = %q, want nonzero objects and bytes after a fresh commit", m[0])
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	m = stagedLineRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("second commit output %q has no staged line", out)
	}
	// The source is unchanged, so every tree, blob and chunk is already
	// packed and only the new commit's own snapshot object is staged.
	if m[1] != "1" {
		t.Fatalf("staged line = %q, want 1 object staged (the new snapshot)", m[0])
	}
}
