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
// recover as the fix.
func TestLsFromCacheReportsIncompleteSnapshot(t *testing.T) {
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
	// ls reads the staging store first, so the staged trees must go
	// too, the way gc frees them once both copies are verified.
	if err := os.RemoveAll(filepath.Join(repo, "staging", "objects")); err != nil {
		t.Fatal(err)
	}

	lastDisc := discRoots[len(discRoots)-1]
	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+lastDisc); code == 2 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "ls", "--repo="+repo, "--recursive", snapID)
	if code != 1 {
		t.Fatalf("ls: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not complete in the cache") || !strings.Contains(out, "recover") {
		t.Fatalf("ls output %q does not report an incomplete cache", out)
	}
}

// TestLsAndLogAgreeOnAnEmptyCache checks that ls and log report a cache
// with no disc in it the same way: it is a failure at run time, exit 1,
// for the listing form and for the one-snapshot form alike.
func TestLsAndLogAgreeOnAnEmptyCache(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := [][]string{
		{"ls", "--repo=" + repo, "latest"},
		{"log", "--repo=" + repo, "latest"},
		{"log", "--repo=" + repo},
	}
	for _, args := range cases {
		code, out := runCmd(t, args...)
		if code != 1 {
			t.Fatalf("%v: exit %d, want 1: %s", args, code, out)
		}
		if !strings.Contains(out, "no disc is cached yet") {
			t.Fatalf("%v output %q does not name the empty cache", args, out)
		}
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

// TestLsAndLogBeforeTheFirstPack checks that log and ls -r resolve a
// just-committed ref and its trees from the staging store, before any
// pack has filled the local cache.
func TestLsAndLogBeforeTheFirstPack(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-21", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	if code, out := runCmd(t, "log", "--repo="+repo); code != 0 {
		t.Fatalf("log: exit %d, want 0: %s", code, out)
	} else if !strings.Contains(out, snapID) || !strings.Contains(out, "2026-09-21") {
		t.Fatalf("log output %q, want the staged snapshot and its ref", out)
	}

	if code, out := runCmd(t, "ls", "--repo="+repo, "--recursive", "2026-09-21"); code != 0 {
		t.Fatalf("ls -r: exit %d, want 0: %s", code, out)
	} else if !strings.Contains(out, "a.txt") || !strings.Contains(out, "b.txt") {
		t.Fatalf("ls -r output %q, want the committed files", out)
	}

	// A second commit with a pack in between: the first snapshot comes
	// from the cache, the second one from staging, and log lists both.
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(src, "c.txt"), []byte("content of c"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-22", src)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	snapID2 := snapshotIDFromCommit(t, out)

	code, out = runCmd(t, "log", "--repo="+repo)
	if code != 0 {
		t.Fatalf("log (after the second commit): exit %d: %s", code, out)
	}
	if !strings.Contains(out, snapID) || !strings.Contains(out, snapID2) {
		t.Fatalf("log output %q, want both snapshots", out)
	}
	if code, out := runCmd(t, "ls", "--repo="+repo, "--recursive", "2026-09-22"); code != 0 {
		t.Fatalf("ls -r (second): exit %d: %s", code, out)
	} else if !strings.Contains(out, "c.txt") {
		t.Fatalf("ls -r output %q, want the new file", out)
	}
}
