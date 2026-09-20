package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/object"
)

// repoCacheDir opens repo's config and resolves the cache directory the
// same way pack and rebuild-cache do.
func repoCacheDir(t *testing.T, repo string) string {
	t.Helper()
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPackPopulatesCache runs init, commit and pack, then checks pack
// left a local cache behind that ls and plan could use with no disc
// present: the run's catalog, the snapshot object, at least one tree,
// and a completeness record.
func TestPackPopulatesCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

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
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=BASE", "--out="+treeDir); code != 0 {
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

	if _, err := c.IndexForRun(1); err != nil {
		t.Fatalf("IndexForRun(1): %v", err)
	}
	if _, err := c.Refs(); err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if _, err := c.Discs(); err != nil {
		t.Fatalf("Discs: %v", err)
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
// left behind, along with the repository, and checks rebuild-cache
// from the packed tree alone puts back an equally complete cache.
func TestRebuildCacheRestoresCacheContent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

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
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=BASE", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	cacheDir := repoCacheDir(t, repo)
	beforeTrees, err := os.ReadDir(filepath.Join(cacheDir, "trees"))
	if err != nil {
		t.Fatalf("read trees before: %v", err)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "rebuild-cache", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
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
