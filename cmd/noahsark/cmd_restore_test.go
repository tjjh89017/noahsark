package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreAcceptsARefName checks that restore resolves a ref name
// the same way ls and log do, instead of only a snapshot id.
func TestRestoreAcceptsARefName(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)

	restoredDir := filepath.Join(t.TempDir(), "restored")
	code, out := runCmd(t, "restore", treeDir, defaultRefName(), restoredDir)
	if code != 0 {
		t.Fatalf("restore by ref name: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(restoredDir, src), src)

	restoredByID := filepath.Join(t.TempDir(), "restored-by-id")
	if code, out := runCmd(t, "restore", treeDir, snapID, restoredByID); code != 0 {
		t.Fatalf("restore snapID: exit %d: %s", code, out)
	}
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

// TestRestoreTwoArgsWithNoMountPrintsUsage checks that "restore
// DISC-ROOT SNAPSHOT", missing OUT-DIR and given no --mount, names the
// disc root and prints the usage line, instead of silently entering
// disc-swap mode with DISC-ROOT misread as SNAPSHOT.
func TestRestoreTwoArgsWithNoMountPrintsUsage(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)

	code, out := runCmd(t, "restore", treeDir, snapID)
	if code != 2 {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "usage: noahsark restore") {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR) output %q, want the usage line", out)
	}
	if !strings.Contains(out, "OUT-DIR is missing") {
		t.Fatalf("restore DISC-ROOT SNAPSHOT (no OUT-DIR) output %q, want the missing OUT-DIR named", out)
	}
}

// TestRestoreMountWithDiscRootIsAnError checks that "restore
// --mount=DIR DISC-ROOT SNAPSHOT OUT-DIR" is refused by name, instead
// of misreading DISC-ROOT as the disc-swap mode's own disc root and
// reporting SNAPSHOT unknown.
func TestRestoreMountWithDiscRootIsAnError(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	restoredDir := filepath.Join(t.TempDir(), "restored")

	code, out := runCmd(t, "restore", "--mount=/mnt/drive", treeDir, snapID, restoredDir)
	if code != 2 {
		t.Fatalf("restore --mount DISC-ROOT SNAPSHOT OUT-DIR: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--mount takes no DISC-ROOT") {
		t.Fatalf("restore --mount DISC-ROOT SNAPSHOT OUT-DIR output %q, want the --mount-takes-no-DISC-ROOT message", out)
	}
	if !strings.Contains(out, "usage: noahsark restore") {
		t.Fatalf("restore --mount DISC-ROOT SNAPSHOT OUT-DIR output %q, want the usage line too", out)
	}
}

// TestRestoreDryRunWithoutMountIsAnError checks that "restore --dry-run"
// with no --mount is refused by name, instead of falling into the
// all-discs-at-once dispatch and reporting a missing disc.
func TestRestoreDryRunWithoutMountIsAnError(t *testing.T) {
	_, snapID, _ := lsFixture(t)
	restoredDir := filepath.Join(t.TempDir(), "restored")

	code, out := runCmd(t, "restore", "--dry-run", snapID, restoredDir)
	if code != 2 {
		t.Fatalf("restore --dry-run (no --mount): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--dry-run needs --mount") {
		t.Fatalf("restore --dry-run (no --mount) output %q, want the --dry-run-needs---mount message", out)
	}
}
