//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestRestoreContinuesPastFIFO asserts that a FIFO in the snapshot does
// not stop the restore, and that it alone does not fail the run: every
// other file lands, the entry is reported as a warning, and the exit
// code is 0.
func TestRestoreContinuesPastFIFO(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Skipf("this platform has no FIFO: %v", err)
	}

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
	if !strings.Contains(out, "not restored: 1 unsupported entry(ies)") {
		t.Fatalf("output = %q, want the unsupported-entry summary", out)
	}
	if !strings.Contains(out, "noahsark: restore: warning: not restored:") {
		t.Fatalf("output = %q, want the per-entry line to read as a warning", out)
	}
	if !strings.Contains(out, "(entry type 6)") {
		t.Fatalf("output = %q, want the FIFO's own line", out)
	}
	for _, rel := range []string{"a.txt", "sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(restoredDir, src, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
}
