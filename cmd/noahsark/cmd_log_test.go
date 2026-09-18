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
	if !strings.Contains(out, "refs: LATEST") {
		t.Fatalf("log output %q missing the ref name", out)
	}
	if !strings.Contains(out, "roots: "+rootPath(src)) {
		t.Fatalf("log output %q missing the root path", out)
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
	code, out := runCmd(t, "log", treeDir, "LATEST")
	if code != 0 {
		t.Fatalf("log LATEST: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "snapshot "+snapID) {
		t.Fatalf("log LATEST output %q missing the resolved snapshot id", out)
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

// TestLogLimit checks --limit caps the number of listed entries, using a
// repository committed twice so log has two snapshots to choose from.
func TestLogLimit(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	_ = snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "log", treeDir)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	full := strings.TrimRight(out, "\n")
	fullLines := strings.Split(full, "\n")
	if len(fullLines) != 2 {
		t.Fatalf("log listed %d snapshot(s), want 2: %q", len(fullLines), out)
	}

	code, out = runCmd(t, "log", "--limit=1", treeDir)
	if code != 0 {
		t.Fatalf("log --limit=1: exit %d: %s", code, out)
	}
	limited := strings.TrimRight(out, "\n")
	if strings.Count(limited, "\n")+1 != 1 {
		t.Fatalf("log --limit=1 listed more than one line: %q", out)
	}
	if limited != fullLines[0] {
		t.Fatalf("log --limit=1 = %q, want the newest entry %q", limited, fullLines[0])
	}
}

// TestLogJSON checks --json prints a JSON array naming the snapshot id.
func TestLogJSON(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	code, out := runCmd(t, "log", "--json", treeDir)
	if code != 0 {
		t.Fatalf("log --json: exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"id": "`+snapID+`"`) {
		t.Fatalf("log --json output %q missing the snapshot id", out)
	}
}
