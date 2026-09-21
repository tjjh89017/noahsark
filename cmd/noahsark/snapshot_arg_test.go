package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// snapshotArgFixture commits and packs one source tree, and returns the
// repository, the packed disc root and the snapshot id commit printed.
func snapshotArgFixture(t *testing.T) (repo, treeDir, snapID string) {
	t.Helper()
	work := t.TempDir()
	repo = filepath.Join(work, "repo")
	src := writeFixtureSource(t)

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
	return repo, treeDir, snapID
}

// firstColumn returns the first whitespace-separated field of the first
// non-empty line of out.
func firstColumn(t *testing.T, out string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			return fields[0]
		}
	}
	t.Fatalf("no line in output: %q", out)
	return ""
}

// TestSnapshotArgFormsFromLog covers the forms the operator guide shows
// for a snapshot argument: a ref name, and the snapshot id that log
// prints in its first column. restore, ls and restore --dry-run must
// all accept both.
func TestSnapshotArgFormsFromLog(t *testing.T) {
	repo, treeDir, snapID := snapshotArgFixture(t)

	code, out := runCmd(t, "log", "--repo="+repo)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	logged := firstColumn(t, out)
	if logged != snapID {
		t.Fatalf("log printed %q in its first column, commit printed %q", logged, snapID)
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, treeDir)

	outRoot := t.TempDir()
	for i, arg := range []string{logged, defaultRefName()} {
		if code, out := runCmd(t, "restore", treeDir, arg, filepath.Join(outRoot, string(rune('a'+i)))); code != 0 {
			t.Fatalf("restore %q: exit %d, want 0: %s", arg, code, out)
		}
		if code, out := runCmd(t, "ls", treeDir, arg); code != 0 {
			t.Fatalf("ls %q: exit %d, want 0: %s", arg, code, out)
		}
		if code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "--dry-run", arg, filepath.Join(outRoot, "dry-run")); code != 0 {
			t.Fatalf("restore --dry-run %q: exit %d, want 0: %s", arg, code, out)
		}
		if code, out := runCmd(t, "log", "--repo="+repo, arg); code != 0 {
			t.Fatalf("log %q: exit %d, want 0: %s", arg, code, out)
		}
	}
}

// TestSnapshotArgEmptyNamesItself covers the fault of issue 48: an empty
// snapshot argument was quoted back as `ref ""`, which names nothing.
// Every command that takes a snapshot must say that no snapshot was
// given, and exit 2.
func TestSnapshotArgEmptyNamesItself(t *testing.T) {
	repo, treeDir, _ := snapshotArgFixture(t)

	cases := [][]string{
		{"restore", treeDir, "", filepath.Join(t.TempDir(), "out")},
		{"ls", treeDir, ""},
		{"ls", "--repo=" + repo, ""},
		{"restore", "--repo=" + repo, "--mount=" + t.TempDir(), "--dry-run", "", filepath.Join(t.TempDir(), "out")},
		{"log", "--repo=" + repo, ""},
	}
	for _, args := range cases {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, "no snapshot given") {
			t.Fatalf("%v: output = %q, want it to say no snapshot was given", args, out)
		}
		if strings.Contains(out, `""`) {
			t.Fatalf("%v: output = %q, quotes an empty name", args, out)
		}
	}
}

// TestRestoreNamesAMistypedDiscRoot asserts that the DISC-ROOT SNAPSHOT
// OUT-DIR form names a first argument that is not a disc root, instead
// of reporting a missing object later.
func TestRestoreNamesAMistypedDiscRoot(t *testing.T) {
	_, _, snapID := snapshotArgFixture(t)

	code, out := runCmd(t, "restore", "/no/such/disc", snapID, filepath.Join(t.TempDir(), "out"))
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "no such disc root: /no/such/disc") {
		t.Fatalf("output = %q, want the disc root named", out)
	}
}
