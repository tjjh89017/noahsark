package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/restore"
)

// TestRestoreOwnerSkippedWhenUnprivileged runs a real restore, as the
// test process's own (non-root) user, and asserts the expected-owner
// case of OPERATIONS.md's metadata restore policy: a restore that does
// not run as root never attempts Chown, so it never reports an "owner
// not applied" line and never loses exit code 0 to it.
func TestRestoreOwnerSkippedWhenUnprivileged(t *testing.T) {
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
	code, out = runCmd(t, "restore", treeDir, snapID, restoredDir)
	if code != 0 {
		t.Fatalf("restore: exit %d, want 0; output: %s", code, out)
	}
	if strings.Contains(out, "owner not applied") {
		t.Fatalf("output = %q, want no owner-field warning for an unprivileged restore", out)
	}
}

// TestPrintProblemsLineFormat asserts the exact line format of the one
// print loop: a warning for a metadata field, and a plain failure line
// for a file the restore could not write.
func TestPrintProblemsLineFormat(t *testing.T) {
	rep := restore.Report{Problems: []restore.Problem{
		{Path: "/out/a.txt", Kind: restore.KindMetadata, Err: errors.New("mode not applied: permission denied")},
		{Path: "/out/b.bin", Kind: restore.KindFile, Err: errors.New("content id does not verify")},
	}}
	var stderr bytes.Buffer
	printProblems(&stderr, rep)
	want := "noahsark: restore: warning: /out/a.txt: mode not applied: permission denied\n" +
		"noahsark: restore: /out/b.bin: content id does not verify\n"
	if stderr.String() != want {
		t.Fatalf("output = %q, want %q", stderr.String(), want)
	}
}

// TestPrintProblemsEmpty asserts that a report with no problem prints
// nothing.
func TestPrintProblemsEmpty(t *testing.T) {
	var stderr bytes.Buffer
	printProblems(&stderr, restore.Report{})
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
