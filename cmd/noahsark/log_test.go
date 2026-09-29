package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/object"
)

// TestLogListsKnownSnapshots checks that a bare log lists the one
// committed snapshot, naming its id, ref and root path.
func TestLogListsKnownSnapshots(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	code, out := runCmd(t, "log", treeDir)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	if !strings.Contains(out, snapID) {
		t.Fatalf("log output %q missing the snapshot id", out)
	}
	if !strings.Contains(out, "refs: "+defaultRefName()) {
		t.Fatalf("log output %q missing the ref name", out)
	}
	if !strings.Contains(out, "roots: "+rootPath(src)) {
		t.Fatalf("log output %q missing the root path", out)
	}
}

// TestLogNamesARefOnAnotherDiscAfterGC checks that log on a disc root
// still lists a ref whose own snapshot object gc has freed from
// staging: REFS.bin carries the ref forward on every later run, but
// packing stops carrying the freed snapshot object itself, so it is no
// longer physically on the newest disc.
func TestLogNamesARefOnAnotherDiscAfterGC(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	srcA := writeRefsCarryFixture(t, "A")

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", "--ref=A", srcA)
	if code != 0 {
		t.Fatalf("commit A: exit %d: %s", code, out)
	}
	snapA := snapshotIDFromCommit(t, out)
	before := time.Now()
	packAndVerifyDisc(t, work, repo, srcA)

	// Past the fixed 7-day retention: gc frees disc A's run, including
	// its own staged snapshot object.
	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })
	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}

	srcB := writeRefsCarryFixture(t, "B")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=B", srcB); code != 0 {
		t.Fatalf("commit B: exit %d: %s", code, out)
	}
	code, out = runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+filepath.Join(work, "disc-b"))
	if code != 0 {
		t.Fatalf("pack B: exit %d: %s", code, out)
	}
	discB := packedTreeDir(t, out)

	code, out = runCmd(t, "log", discB)
	if code != 0 {
		t.Fatalf("log disc-b: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "refs: B") {
		t.Fatalf("log output %q is missing ref B", out)
	}
	if !strings.Contains(out, snapA) || !strings.Contains(out, "refs: A") || !strings.Contains(out, "on another disc") {
		t.Fatalf("log output %q does not name ref A's snapshot on another disc", out)
	}
}

// TestLogWithArgumentPrintsOneSnapshotsDetails checks that log SNAPSHOT
// prints that snapshot's own details instead of a listing line.
func TestLogWithArgumentPrintsOneSnapshotsDetails(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	code, out := runCmd(t, "log", treeDir, snapID)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "snapshot "+snapID) {
		t.Fatalf("log output %q missing the snapshot header line", out)
	}
	if !strings.Contains(out, "parent: (none)") {
		t.Fatalf("log output %q missing the parent line", out)
	}
	if !strings.Contains(out, "root paths: "+rootPath(src)) {
		t.Fatalf("log output %q missing the root paths line", out)
	}
}

// TestLogAcceptsARefName checks log resolves a ref name the same way it
// resolves a snapshot id text form.
func TestLogAcceptsARefName(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	code, out := runCmd(t, "log", treeDir, defaultRefName())
	if code != 0 {
		t.Fatalf("log by ref name: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "snapshot "+snapID) {
		t.Fatalf("log by ref name output %q missing the resolved snapshot id", out)
	}
}

// TestLogSameSecondSnapshotsStayNewestFirst commits twice with the
// clock pinned to the same second (but two different nanoseconds
// within it, since a real clock never repeats a nanosecond in
// practice), so both snapshots tie on log's printed, second-precision
// time, and checks that the truly newer one still lists first, not
// whichever sort.Slice happened to leave on top.
func TestLogSameSecondSnapshotsStayNewestFirst(t *testing.T) {
	oldNewWriter := newWriter
	defer func() { newWriter = oldNewWriter }()
	base := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	next := base
	newWriter = func(stagingDir string) *object.Writer {
		w := oldNewWriter(stagingDir)
		w.Now = func() time.Time { return next }
		return w
	}

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	next = base.Add(1 * time.Millisecond)
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	snap1 := snapshotIDFromCommit(t, out)

	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("changed content of a"), 0o644); err != nil {
		t.Fatal(err)
	}
	next = base.Add(2 * time.Millisecond)
	code, out = runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	snap2 := snapshotIDFromCommit(t, out)
	if snap1 == snap2 {
		t.Fatal("the two commits produced the same snapshot id; fixture did not change")
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "log", treeDir)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	i1, i2 := strings.Index(out, snap1), strings.Index(out, snap2)
	if i1 < 0 || i2 < 0 {
		t.Fatalf("log output %q missing one of the two snapshot ids", out)
	}
	if i2 > i1 {
		t.Fatalf("log output %q lists the older snapshot %s before the newer %s", out, snap1, snap2)
	}
}

// TestLogFromCacheWithNoDisc checks log resolves the same way, both
// listing every snapshot and printing one snapshot's own details.
func TestLogFromCacheWithNoDisc(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	discCode, discOut := runCmd(t, "log", treeDir, snapID)
	if discCode != 0 {
		t.Fatalf("log (disc): exit %d: %s", discCode, discOut)
	}
	cacheCode, cacheOut := runCmd(t, "--repo="+repo, "log", snapID)
	if cacheCode != 0 {
		t.Fatalf("log (cache): exit %d: %s", cacheCode, cacheOut)
	}
	if cacheOut != discOut {
		t.Fatalf("log from cache = %q, want %q (same as disc)", cacheOut, discOut)
	}

	if code, out := runCmd(t, "--repo="+repo, "log"); code != 0 {
		t.Fatalf("--repo log (list all): exit %d: %s", code, out)
	} else if !strings.Contains(out, snapID) {
		t.Fatalf("--repo log listing = %q, does not name %s", out, snapID)
	}
}

// TestLogNonexistentPathReportsNoSuchDiscRoot is TestLsNonexistentPathReportsNoSuchDiscRoot for log.
func TestLogNonexistentPathReportsNoSuchDiscRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-disc")
	code, out := runCmd(t, "log", missing)
	if code != 2 {
		t.Fatalf("log: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "no such disc root: "+missing) {
		t.Fatalf("log output %q does not name the missing disc root", out)
	}
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

	code, out := runCmd(t, "--repo="+repo, "log")
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
		if code, out := runCmd(t, "--repo="+repo, "restore", "--mount="+mountDir, "--dry-run", arg, filepath.Join(outRoot, "dry-run")); code != 0 {
			t.Fatalf("restore --dry-run %q: exit %d, want 0: %s", arg, code, out)
		}
		if code, out := runCmd(t, "--repo="+repo, "log", arg); code != 0 {
			t.Fatalf("log %q: exit %d, want 0: %s", arg, code, out)
		}
	}
}

// TestLogUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// log: each case exits 2, never 0 or 1.
func TestLogUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--repo=" + repo, "log", "--no-such-flag"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runCmd(t, c.args...)
			if code != 2 {
				t.Fatalf("args %v: exit %d, want 2: %s", c.args, code, out)
			}
		})
	}
}
