package main

import (
	"bytes"
	"errors"
	"fmt"
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

// TestMetadataFailureLineFormat asserts the exact warning line format
// for one metadata_not_applied event.
func TestMetadataFailureLineFormat(t *testing.T) {
	f := restore.MetadataFailure{Path: "/out/a.txt", Field: "mode", Err: errors.New("permission denied")}
	got := metadataFailureLine(f)
	want := "noahsark: restore: warning: /out/a.txt: mode not applied: permission denied"
	if got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

// TestPrintMetadataFailuresCapsAt20 asserts that more than 20 metadata
// failures print only the first 20 lines, folding the rest into one
// count line, and that the function reports true whenever any failure
// exists (the exit-code signal cmd_restore.go relies on).
func TestPrintMetadataFailuresCapsAt20(t *testing.T) {
	var fails []restore.MetadataFailure
	for i := range 25 {
		fails = append(fails, restore.MetadataFailure{
			Path:  fmt.Sprintf("/out/f%d.txt", i),
			Field: "times",
			Err:   errors.New("boom"),
		})
	}
	var stderr bytes.Buffer
	loss := printMetadataFailures(&stderr, fails)
	if !loss {
		t.Fatal("printMetadataFailures reported no loss for 25 failures")
	}
	out := stderr.String()
	if n := strings.Count(out, "not applied: boom"); n != 20 {
		t.Fatalf("printed %d per-failure lines, want 20", n)
	}
	if !strings.Contains(out, "5 more metadata failure(s) not shown") {
		t.Fatalf("output = %q, want the overflow count line", out)
	}
}

// TestPrintMetadataFailuresEmpty asserts that no failures print nothing
// and report no loss.
func TestPrintMetadataFailuresEmpty(t *testing.T) {
	var stderr bytes.Buffer
	if printMetadataFailures(&stderr, nil) {
		t.Fatal("printMetadataFailures reported loss for an empty list")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
