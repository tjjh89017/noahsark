package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreAcceptsARefName checks that restore resolves a ref name
// the same way ls and log do, instead of only a snapshot id.
func TestRestoreAcceptsARefName(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)

	restoredDir := filepath.Join(t.TempDir(), "restored")
	code, out := runCmd(t, "restore", treeDir, "LATEST", restoredDir)
	if code != 0 {
		t.Fatalf("restore LATEST: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(restoredDir, src), src)

	restoredByID := filepath.Join(t.TempDir(), "restored-by-id")
	if code, out := runCmd(t, "restore", treeDir, snapID, restoredByID); code != 0 {
		t.Fatalf("restore snapID: exit %d: %s", code, out)
	}
}

// TestRestoreDiscsDirFollowsASymlinkedDiscRoot checks that --discs-dir
// finds a disc root reached through a symlink, not only a real
// directory: os.ReadDir's entries report a symlink's own type, not the
// directory it points to, so resolveDiscRoots must stat through it.
func TestRestoreDiscsDirFollowsASymlinkedDiscRoot(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)

	discsDir := t.TempDir()
	link := filepath.Join(discsDir, "disc-link")
	if err := os.Symlink(treeDir, link); err != nil {
		t.Fatal(err)
	}

	restoredDir := filepath.Join(t.TempDir(), "restored")
	code, out := runCmd(t, "restore", "--discs-dir="+discsDir, snapID, restoredDir)
	if code != 0 {
		t.Fatalf("restore --discs-dir with a symlinked disc root: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(restoredDir, src), src)
}

// TestRestoreUnknownRefReportsTheSameError checks that an unknown ref
// name given to restore reports the same error ls reports for it.
func TestRestoreUnknownRefReportsTheSameError(t *testing.T) {
	treeDir, _, _ := lsFixture(t)
	restoredDir := filepath.Join(t.TempDir(), "restored")

	restoreCode, restoreOut := runCmd(t, "restore", treeDir, "NO-SUCH-REF", restoredDir)
	lsCode, lsOut := runCmd(t, "ls", treeDir, "NO-SUCH-REF")

	if restoreCode != 2 {
		t.Fatalf("restore: exit %d, want 2: %s", restoreCode, restoreOut)
	}
	restoreMsg := strings.TrimPrefix(strings.TrimSpace(restoreOut), "noahsark: restore: ")
	lsMsg := strings.TrimPrefix(strings.TrimSpace(lsOut), "noahsark: ls: ")
	if restoreMsg != lsMsg {
		t.Fatalf("restore error %q, want same as ls error %q (ls exit %d)", restoreMsg, lsMsg, lsCode)
	}
}

// TestRestoreMountWithDiscFlagsIsAnError checks that --mount combined
// with --disc, or with --discs-dir, is refused: the two select
// different, incompatible restore modes.
func TestRestoreMountWithDiscFlagsIsAnError(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	restoredDir := filepath.Join(t.TempDir(), "restored")

	code, out := runCmd(t, "restore", "--mount=/mnt/drive", "--disc="+treeDir, snapID, restoredDir)
	if code != 2 {
		t.Fatalf("restore --mount --disc: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--mount") || !strings.Contains(out, "--disc") {
		t.Fatalf("restore --mount --disc output %q, want it to name both flags", out)
	}

	discsDir := t.TempDir()
	code, out = runCmd(t, "restore", "--mount=/mnt/drive", "--discs-dir="+discsDir, snapID, restoredDir)
	if code != 2 {
		t.Fatalf("restore --mount --discs-dir: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--mount") || !strings.Contains(out, "--discs-dir") {
		t.Fatalf("restore --mount --discs-dir output %q, want it to name both flags", out)
	}
}

// TestRestoreTwoArgsWithNoMountPrintsUsage checks that "restore
// DISC-ROOT SNAPSHOT", missing OUT-DIR and given no --mount, prints the
// usage line instead of silently entering disc-swap mode with DISC-ROOT
// misread as SNAPSHOT.
func TestRestoreTwoArgsWithNoMountPrintsUsage(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)

	code, out := runCmd(t, "restore", treeDir, snapID)
	if code != 2 {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR): exit %d, want 2: %s", code, out)
	}
	if !strings.HasPrefix(out, "usage: noahsark restore") {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR) output %q, want the usage line", out)
	}
}
