package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes content to path, creating its parent directories.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// readFile reads path as a string.
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// TestGlobalFlagBeforeCommand asserts that -q works before the command
// name, not only after it.
func TestGlobalFlagBeforeCommand(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runCmd(t, "-q", "init", "--repo="+repo); code != 0 {
		t.Fatalf("-q init: exit %d: %s", code, out)
	}
}

// TestCommandHelpExitsZeroAndShowsPositionals asserts that a command's
// own -h prints its usage line, including its positional arguments, and
// exits 0.
func TestCommandHelpExitsZeroAndShowsPositionals(t *testing.T) {
	code, out := runCmd(t, "commit", "-h")
	if code != 0 {
		t.Fatalf("commit -h: exit %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "SOURCE") {
		t.Fatalf("commit -h output = %q, want it to mention SOURCE", out)
	}
	if !strings.Contains(out, "-ref") {
		t.Fatalf("commit -h output = %q, want it to list -ref", out)
	}
}

// TestInitHelpTextIsPlain asserts init's usage text describes the
// command in plain words, not the old "SOURCE-less setup" phrasing.
func TestInitHelpTextIsPlain(t *testing.T) {
	code, out := runCmd(t, "init", "-h")
	if code != 0 {
		t.Fatalf("init -h: exit %d, want 0; output: %s", code, out)
	}
	if strings.Contains(out, "SOURCE-less") {
		t.Fatalf("init -h output = %q, want plain wording, not \"SOURCE-less\"", out)
	}
	if !strings.Contains(out, "repository") {
		t.Fatalf("init -h output = %q, want it to describe creating a repository", out)
	}
}

// TestCommitDryRunNotYetInBuild asserts that a documented but
// unimplemented commit flag is refused with a clear message and exit 2,
// not the raw flag package error.
func TestCommitDryRunNotYetInBuild(t *testing.T) {
	code, out := runCmd(t, "commit", "--dry-run", "/nowhere")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	if !strings.Contains(out, "noahsark: commit: flag --dry-run is not in this build yet") {
		t.Fatalf("output = %q, want the not-in-this-build message", out)
	}
	if strings.Contains(out, "flag provided but not defined") {
		t.Fatalf("output = %q, want no raw flag package error", out)
	}
}

// TestCommitMessageFlag asserts that -m accepts a commit message.
func TestCommitMessageFlag(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, "-m", "hello world", src); code != 0 {
		t.Fatalf("commit -m: exit %d: %s", code, out)
	}
}

// TestMissingPhase1CommandRefused asserts that a Phase 1 command
// OPERATIONS.md documents but this build does not implement (burn) is
// refused with a clear message and exit 2, not "unknown command".
func TestMissingPhase1CommandRefused(t *testing.T) {
	code, out := runCmd(t, "burn", "--run=1", "--print")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	want := "noahsark: burn is not in this build yet"
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want it to contain %q", out, want)
	}
	if strings.Contains(out, "unknown command") {
		t.Fatalf("output = %q, want no \"unknown command\"", out)
	}
}

// TestLsFlagAfterPositionalReportedClearly asserts that a flag placed
// after ls's positional arguments is reported as a usage error naming
// the flag, instead of being read back as a PATH.
func TestLsFlagAfterPositionalReportedClearly(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)

	code, out := runCmd(t, "ls", treeDir, snapID, "--recursive")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	want := "flags must come before positional arguments: --recursive"
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want it to contain %q", out, want)
	}
	if strings.Contains(out, "matches no entry") {
		t.Fatalf("output = %q, want no \"matches no entry\" misreading", out)
	}
}

// TestRestoreOverwrite asserts that restoring into a directory that
// already holds a differing file leaves that file alone and reports it
// skipped, and that --overwrite replaces it instead.
func TestRestoreOverwrite(t *testing.T) {
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
	preexisting := filepath.Join(restoredDir, src, "a.txt")
	if err := writeFile(preexisting, "pre-existing, different content"); err != nil {
		t.Fatal(err)
	}

	code, out = runCmd(t, "restore", treeDir, snapID, restoredDir)
	if code != 1 {
		t.Fatalf("restore without --overwrite: exit %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, "skipped 1 existing path(s)") {
		t.Fatalf("output = %q, want a skipped-path count", out)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)

	treeDir = filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
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
