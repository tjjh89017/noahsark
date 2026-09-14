package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetEjectLatches clears the once-per-run warning latches ejectDrive
// keeps, restoring them after the test, so one test's warning does not
// silence another's.
func resetEjectLatches(t *testing.T) {
	t.Helper()
	oldNoBinary, oldPermission := ejectWarnedNoBinary, ejectWarnedPermission
	ejectWarnedNoBinary, ejectWarnedPermission = false, false
	t.Cleanup(func() { ejectWarnedNoBinary, ejectWarnedPermission = oldNoBinary, oldPermission })
}

// TestEjectDriveSkipsMissingBinaryOnce points PATH at a directory with
// no eject binary and checks ejectDrive prints one informational skip
// line, not a per-call warning, across two calls.
func TestEjectDriveSkipsMissingBinaryOnce(t *testing.T) {
	resetEjectLatches(t)
	t.Setenv("PATH", t.TempDir())

	var out bytes.Buffer
	ejectDrive(t.TempDir(), &out)
	ejectDrive(t.TempDir(), &out)

	got := out.String()
	if n := strings.Count(got, "eject: not found on PATH"); n != 1 {
		t.Fatalf("eject: not found on PATH printed %d time(s) across two calls, want 1: %q", n, got)
	}
	if strings.Contains(got, "warning: eject") {
		t.Fatalf("output %q, want no per-call eject warning when the binary is simply missing", got)
	}
}

// fakeBinOnPath writes an executable shell script named name that
// prints output and exits 1, prepends its directory to PATH for the
// test, and returns nothing: the caller only needs it on PATH.
func fakeBinOnPath(t *testing.T, name, output string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\necho %q >&2\nexit 1\n", output)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestEjectDriveReportsUmountPermissionOnce points PATH at a fake
// umount that always fails with "Permission denied" and checks
// ejectDrive prints the sudo/--no-eject hint once, across two calls,
// not a generic warning per call.
func TestEjectDriveReportsUmountPermissionOnce(t *testing.T) {
	resetEjectLatches(t)
	fakeBinOnPath(t, "umount", "umount: only root can do that: Permission denied")

	var out bytes.Buffer
	ejectDrive(t.TempDir(), &out)
	ejectDrive(t.TempDir(), &out)

	got := out.String()
	if n := strings.Count(got, "run restore with sudo, or pass --no-eject"); n != 1 {
		t.Fatalf("sudo/--no-eject hint printed %d time(s) across two calls, want 1: %q", n, got)
	}
	if strings.Contains(got, "warning: umount") {
		t.Fatalf("output %q, want no generic umount warning once the permission hint applies", got)
	}
}
