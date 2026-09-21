package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/object"
)

// repoCacheDir resolves the cache directory the same way pack and
// recover do.
func repoCacheDir(t *testing.T, repo string) string {
	t.Helper()
	return cache.Dir(repo)
}

// TestPackPopulatesCache runs init, commit and pack, then checks pack
// left a local cache behind that ls and plan could use with no disc
// present: the run's catalog, the snapshot object, at least one tree,
// and a completeness record.
func TestPackPopulatesCache(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, "--ref=BASE", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapIDText := snapshotIDFromCommit(t, out)
	snapID, err := object.ParseID(snapIDText)
	if err != nil {
		t.Fatal(err)
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	c, err := cache.Open(repoCacheDir(t, repo))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}

	ids, err := c.ListSnapshots()
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 1 || ids[0] != snapID {
		t.Fatalf("ListSnapshots = %v, want [%s]", ids, snapID.TextForm())
	}

	if _, err := c.Refs(); err != nil {
		t.Fatalf("Refs: %v", err)
	}
	discs, err := c.Discs()
	if err != nil {
		t.Fatalf("Discs: %v", err)
	}
	if len(discs.Rows) != 1 {
		t.Fatalf("cached DISCS row count = %d, want 1", len(discs.Rows))
	}
	if _, err := c.IndexForDisc(discs.Rows[0].DiscUUID); err != nil {
		t.Fatalf("IndexForDisc: %v", err)
	}

	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if _, err := c.ReadTree(object.ID(snap.RootTree)); err != nil {
		t.Fatalf("ReadTree(root): %v", err)
	}

	if !c.Complete(snapID) {
		t.Fatalf("Complete(%s) = false, want true", snapID.TextForm())
	}
	if err := c.CheckComplete(snapID); err != nil {
		t.Fatalf("CheckComplete: %v", err)
	}
}

// TestRebuildCacheRestoresCacheContent deletes the whole cache pack
// left behind, along with the repository, and checks recover
// from the packed tree alone puts back an equally complete cache.
func TestRebuildCacheRestoresCacheContent(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, "--ref=BASE", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapIDText := snapshotIDFromCommit(t, out)
	snapID, err := object.ParseID(snapIDText)
	if err != nil {
		t.Fatal(err)
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	beforeTrees, err := os.ReadDir(filepath.Join(repoCacheDir(t, repo), "trees"))
	if err != nil {
		t.Fatalf("read trees before: %v", err)
	}

	// The cache lives inside the repository directory, so removing the
	// repository removes the cache with it: this is the rebuild case
	// recover must handle, the cache lost along with everything else.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "recover", "--repo="+repo, treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	c, err := cache.Open(repoCacheDir(t, repo))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	ids, err := c.ListSnapshots()
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 1 || ids[0] != snapID {
		t.Fatalf("ListSnapshots = %v, want [%s]", ids, snapID.TextForm())
	}
	if !c.Complete(snapID) {
		t.Fatalf("Complete(%s) = false, want true", snapID.TextForm())
	}

	afterTrees, err := os.ReadDir(filepath.Join(repoCacheDir(t, repo), "trees"))
	if err != nil {
		t.Fatalf("read trees after: %v", err)
	}
	if len(afterTrees) != len(beforeTrees) {
		t.Fatalf("tree count after rebuild = %d, want %d", len(afterTrees), len(beforeTrees))
	}
}
