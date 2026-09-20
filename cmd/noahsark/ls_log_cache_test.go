package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLsFromCacheWithNoDisc packs a repository, then runs ls with no
// disc given at all: it must resolve the snapshot and list its root
// entries from the local cache pack left behind, the same as the
// disc-based listing.
func TestLsFromCacheWithNoDisc(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	treeDir, snapID, src := lsFixture(t)

	repo := repoDirFromTreeDir(t, treeDir)
	discCode, discOut := runCmd(t, "ls", treeDir, snapID)
	if discCode != 0 {
		t.Fatalf("ls (disc): exit %d: %s", discCode, discOut)
	}

	cacheCode, cacheOut := runCmd(t, "ls", "--repo="+repo, snapID)
	if cacheCode != 0 {
		t.Fatalf("ls (cache): exit %d: %s", cacheCode, cacheOut)
	}
	if cacheOut != discOut {
		t.Fatalf("ls from cache = %q, want %q (same as disc)", cacheOut, discOut)
	}
	_ = src
}

// TestLogFromCacheWithNoDisc checks log resolves the same way, both
// listing every snapshot and printing one snapshot's own details.
func TestLogFromCacheWithNoDisc(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	discCode, discOut := runCmd(t, "log", treeDir, snapID)
	if discCode != 0 {
		t.Fatalf("log (disc): exit %d: %s", discCode, discOut)
	}
	cacheCode, cacheOut := runCmd(t, "log", "--repo="+repo, snapID)
	if cacheCode != 0 {
		t.Fatalf("log (cache): exit %d: %s", cacheCode, cacheOut)
	}
	if cacheOut != discOut {
		t.Fatalf("log from cache = %q, want %q (same as disc)", cacheOut, discOut)
	}

	if code, out := runCmd(t, "log", "--repo="+repo); code != 0 {
		t.Fatalf("log --repo (list all): exit %d: %s", code, out)
	} else if !strings.Contains(out, snapID) {
		t.Fatalf("log --repo listing = %q, does not name %s", out, snapID)
	}
}

// TestLsFromCacheReportsIncompleteSnapshot builds a multi-disc
// repository, wipes the cache, then rebuilds it from only the last
// disc: the snapshot's tree spans earlier discs too, so the cache ends
// up genuinely incomplete. ls with no disc given must exit 3 and name
// rebuild-cache as the fix.
func TestLsFromCacheReportsIncompleteSnapshot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	// Two small forced capacities split the commit across two runs, on
	// two discs: the snapshot's tree needs both.
	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, "disc"+string(rune('0'+i)))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}

	cacheDir := repoCacheDir(t, repo)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}

	lastDisc := discRoots[len(discRoots)-1]
	if code, out := runCmd(t, "rebuild-cache", "--repo="+repo, "--disc="+lastDisc); code == 2 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "ls", "--repo="+repo, "--recursive", snapID)
	if code != 1 {
		t.Fatalf("ls: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not complete in the cache") || !strings.Contains(out, "rebuild-cache") {
		t.Fatalf("ls output %q does not report an incomplete cache", out)
	}
}

// repoDirFromTreeDir recovers a fixture's repository directory from its
// packed tree directory, both children of the same lsFixture work
// directory ("work/tree" and "work/repo").
func repoDirFromTreeDir(t *testing.T, treeDir string) string {
	t.Helper()
	return filepath.Join(filepath.Dir(treeDir), "repo")
}

// TestLsNonexistentPathReportsNoSuchDiscRoot checks that a nonexistent
// path given as ls's first positional is reported as a missing disc
// root, not resolved as a SNAPSHOT arg through cache mode.
func TestLsNonexistentPathReportsNoSuchDiscRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-disc")
	code, out := runCmd(t, "ls", missing, "SOMESNAP")
	if code != 2 {
		t.Fatalf("ls: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "no such disc root: "+missing) {
		t.Fatalf("ls output %q does not name the missing disc root", out)
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
