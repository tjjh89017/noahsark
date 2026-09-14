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
