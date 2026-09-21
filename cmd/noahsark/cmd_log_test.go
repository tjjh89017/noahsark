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
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	srcA := writeRefsCarryFixture(t, "A")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, "--ref=A", srcA)
	if code != 0 {
		t.Fatalf("commit A: exit %d: %s", code, out)
	}
	snapA := snapshotIDFromCommit(t, out)
	before := time.Now()
	packAndVerifyDisc(t, work, repo, srcA)

	// Past the fixed 7-day retention: gc frees disc A's run, including
	// its own staged snapshot object.
	gcClock = func() time.Time { return before.Add(8 * 24 * time.Hour) }
	if code, out := runCmd(t, "gc", "--repo="+repo); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}

	srcB := writeRefsCarryFixture(t, "B")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=B", srcB); code != 0 {
		t.Fatalf("commit B: exit %d: %s", code, out)
	}
	code, out = runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+filepath.Join(work, "disc-b"))
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	next = base.Add(1 * time.Millisecond)
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	snap1 := snapshotIDFromCommit(t, out)

	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("changed content of a"), 0o644); err != nil {
		t.Fatal(err)
	}
	next = base.Add(2 * time.Millisecond)
	code, out = runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	snap2 := snapshotIDFromCommit(t, out)
	if snap1 == snap2 {
		t.Fatal("the two commits produced the same snapshot id; fixture did not change")
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
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
