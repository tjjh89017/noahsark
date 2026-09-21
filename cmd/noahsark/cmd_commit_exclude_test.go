package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeExcludeFixture builds a source tree with paths an exclude pattern
// test can target: a name matched at every depth, an anchored path, and
// a directory-only pattern's target.
func writeExcludeFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	mustMkdirCmd(t, filepath.Join(src, "node_modules"))
	mustWriteCmd(t, filepath.Join(src, "node_modules", "x.js"), "dep")
	mustWriteCmd(t, filepath.Join(src, "a.tmp"), "temp")
	mustMkdirCmd(t, filepath.Join(src, "build", "out"))
	mustWriteCmd(t, filepath.Join(src, "build", "out", "x"), "built")
	mustMkdirCmd(t, filepath.Join(src, "keep", "build", "out"))
	mustWriteCmd(t, filepath.Join(src, "keep", "build", "out", "y"), "kept")
	mustWriteCmd(t, filepath.Join(src, "keep.txt"), "keep")
	return src
}

func mustMkdirCmd(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteCmd(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// packAndLs packs repo's staged snapshot onto a disc, then runs
// "ls --recursive" over it from the packed tree, and returns the output.
func packAndLs(t *testing.T, repo, snapID string) string {
	t.Helper()
	treeDir := filepath.Join(t.TempDir(), "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "ls", "--recursive", treeDir, snapID)
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	return out
}

func TestCommitExcludeFlag(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, "--exclude=node_modules/", "--exclude=*.tmp", "--exclude=/build/out", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "excluded: 3 path(s)") {
		t.Fatalf("commit output %q, want an excluded summary line of 3", out)
	}

	ls := packAndLs(t, repo, snapshotIDFromCommit(t, out))
	for _, want := range []string{"node_modules", "a.tmp", "keep.txt"} {
		present := strings.Contains(ls, want)
		wantPresent := want == "keep.txt"
		if present != wantPresent {
			t.Errorf("ls output contains %q = %v, want %v\n%s", want, present, wantPresent, ls)
		}
	}
	if !strings.Contains(ls, "keep/build/out/y") {
		t.Errorf("ls output missing keep/build/out/y (only /build/out is anchored, not keep/build/out):\n%s", ls)
	}
	if strings.Contains(ls, "build/out/x") && !strings.Contains(ls, "keep/build/out") {
		t.Errorf("ls output should not contain the root build/out/x:\n%s", ls)
	}
}

func TestCommitExcludeBadFlagPatternIsUsageError(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, "--exclude=!keep.txt", src)
	if code != 2 {
		t.Fatalf("commit: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "negation is not supported") {
		t.Fatalf("commit output %q, want it to name the negation problem", out)
	}
}

func TestCommitNoahsarkIgnoreFile(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	mustWriteCmd(t, filepath.Join(src, ignoreFileName), "# comment\n\nnode_modules/\n*.tmp\n/build/out\n")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	ls := packAndLs(t, repo, snapshotIDFromCommit(t, out))
	if strings.Contains(ls, "a.tmp") || strings.Contains(ls, "node_modules") {
		t.Fatalf("ls output %q should not contain excluded paths", ls)
	}
	if !strings.Contains(ls, ignoreFileName) {
		t.Fatalf("ls output %q should still contain the ignore file itself", ls)
	}
}

func TestCommitNoahsarkIgnoreBadPatternIsConfigError(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	mustWriteCmd(t, filepath.Join(src, ignoreFileName), "!negated\n")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 2 {
		t.Fatalf("commit: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, ignoreFileName) || !strings.Contains(out, "negation is not supported") {
		t.Fatalf("commit output %q, want it to name the ignore file and the negation problem", out)
	}
}

func TestCommitOneFileSystemFlagExists(t *testing.T) {
	// This build has no seam to fake a real mount for an end-to-end CLI
	// test; internal/object's writer tests cover the device-id logic
	// with a seam. Here, check only that the flag is accepted and that
	// a normal, single-filesystem commit still succeeds with it set.
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, "--one-file-system", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
}
