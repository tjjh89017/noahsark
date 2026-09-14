package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestRebuildCacheFromDiscRestoresState packs one disc, deletes the
// whole repository directory, then rebuilds it from that disc alone: the
// state log's packed count must match the disc's own INDEX object
// count, and the ref must resolve again.
func TestRebuildCacheFromDiscRestoresState(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, "--ref=BASE", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--ref=BASE", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	rr, err := image.Read(treeDir)
	if err != nil {
		t.Fatalf("image.Read: %v", err)
	}
	wantPacked := len(rr.Index.Objects)

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	code, out = runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir)
	if code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
	}

	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	log, err := stage.Open(cfg.StagingDir)
	if err != nil {
		t.Fatalf("stage.Open: %v", err)
	}
	if got := log.CountState(stage.Packed); got != wantPacked {
		t.Fatalf("packed count = %d, want %d (disc INDEX object count)", got, wantPacked)
	}

	if id, err := resolveRef(repo, "BASE"); err != nil || id.TextForm() != snapID {
		t.Fatalf("resolveRef(BASE) = %v, %v, want %s", id, err, snapID)
	}
}

// TestRebuildCacheIsIdempotent runs rebuild-cache twice from the same
// disc and asserts both calls exit 0 with the same packed count.
func TestRebuildCacheIsIdempotent(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("rebuild-cache #1: exit %d: %s", code, out)
	}
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	log1, err := stage.Open(cfg.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	count1 := log1.CountState(stage.Packed)

	if code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("rebuild-cache #2: exit %d: %s", code, out)
	}
	log2, err := stage.Open(cfg.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	count2 := log2.CountState(stage.Packed)

	if count1 != count2 {
		t.Fatalf("packed count changed across a repeat rebuild: %d then %d", count1, count2)
	}
}

// TestRebuildCachePartialNamesMissingDisc packs a sequence across three
// small discs, deletes the repository, and rebuilds from only the last
// disc: the rebuild must exit 1 and name the earlier discs' uuids.
func TestRebuildCachePartialNamesMissingDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, "disc"+string(rune('0'+i)))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--fec", "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}

	var uuid1 string
	{
		rr, err := image.Read(discRoots[0])
		if err != nil {
			t.Fatalf("image.Read disc0: %v", err)
		}
		uuid1 = uuidText(rr.Disc.DiscUUID)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+discRoots[len(discRoots)-1])
	if code != 1 {
		t.Fatalf("rebuild-cache: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "partial") {
		t.Fatalf("output %q does not say the rebuild is partial", out)
	}
	if !strings.Contains(out, uuid1) {
		t.Fatalf("output %q does not name the missing disc %s", out, uuid1)
	}
}

// TestRebuildCacheNoUsableDisc asserts exit 3 when every named disc
// root fails to read.
func TestRebuildCacheNoUsableDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	empty := filepath.Join(work, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+empty)
	if code != 3 {
		t.Fatalf("exit %d, want 3: %s", code, out)
	}
}

// newObjectsFromCommit parses a commit's "new objects: N, existing
// objects: M" line and returns N.
func newObjectsFromCommit(t *testing.T, output string) int {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		var newObjects, existingObjects int
		if _, err := fmt.Sscanf(line, "new objects: %d, existing objects: %d", &newObjects, &existingObjects); err == nil {
			return newObjects
		}
	}
	t.Fatalf("no \"new objects\" line in commit output: %q", output)
	return -1
}

// TestCommitAfterRebuildCacheReportsNoNewObjects packs a commit, rebuilds
// the repository from that disc alone, then commits the same source
// again: every object the disc already carries must count as existing,
// not new, even though rebuild-cache never restored the staging bytes
// for them.
func TestCommitAfterRebuildCacheReportsNoNewObjects(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	// Every commit stamps a fresh snapshot object with the current
	// time and Phase 1 chains no parent, so two commits of unchanged
	// content only produce byte-identical objects, snapshot included,
	// when both run under the same fixed clock.
	fixed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	oldNewWriter := newWriter
	defer func() { newWriter = oldNewWriter }()
	newWriter = func(stagingDir string) *object.Writer {
		w := object.NewWriter(stagingDir)
		w.Now = func() time.Time { return fixed }
		return w
	}

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	if got := newObjectsFromCommit(t, out); got != 0 {
		t.Fatalf("new objects = %d, want 0: %s", got, out)
	}
}

// TestRebuildCacheRequiresFromDisc asserts rebuild-cache refuses to run
// without --from-disc: this build keeps no cache to rebuild otherwise.
func TestRebuildCacheRequiresFromDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	code, out := runCmd(t, "rebuild-cache", "--repo="+repo, "--disc="+work)
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--from-disc") {
		t.Fatalf("output %q does not mention --from-disc", out)
	}
}

// TestRebuildCacheRefusesLevel2And3 asserts a clear refusal naming the
// missing object cache, not a generic flag error.
func TestRebuildCacheRefusesLevel2And3(t *testing.T) {
	for _, level := range []string{"2", "3"} {
		code, out := runCmd(t, "rebuild-cache", "--from-disc", "--level="+level, "--disc=/nowhere")
		if code != 2 {
			t.Fatalf("level %s: exit %d, want 2: %s", level, code, out)
		}
		if !strings.Contains(out, "no object cache") {
			t.Fatalf("level %s: output %q does not explain the missing cache", level, out)
		}
	}
}
