package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreDryRunWarnsOnTornDiscLog checks that restore, which takes no
// lock, ignores a torn tail of the disc state log, warns on stderr only,
// and leaves the log file as it was.
func TestRestoreDryRunWarnsOnTornDiscLog(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)
	dest := filepath.Join(t.TempDir(), "out")
	args := []string{"--repo=" + repo, "restore", "--dry-run", "--disc=" + t.TempDir(), snapID, dest}

	te := newTestEnv(t.TempDir())
	if code := run(te.env, args); code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, te.errOut.String())
	}
	wantOut := te.out.String()
	if strings.Contains(te.errOut.String(), "ignored") {
		t.Fatalf("intact log: unexpected warning %q", te.errOut.String())
	}

	logPath := testLayout(t, repo).discLogFile()
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	te = newTestEnv(t.TempDir())
	if code := run(te.env, args); code != 0 {
		t.Fatalf("restore --dry-run with torn tail: exit %d: %s", code, te.errOut.String())
	}
	if got := te.out.String(); got != wantOut {
		t.Fatalf("stdout = %q, want %q", got, wantOut)
	}
	wantErr := "noahsark: restore: the disc state log's tail was truncated; 3 byte(s) after the last valid record were ignored, matching a crash during an earlier append\n"
	if got := te.errOut.String(); got != wantErr {
		t.Fatalf("stderr = %q, want %q", got, wantErr)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("restore changed the disc state log")
	}
}
