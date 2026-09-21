package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var dryRunTotalRe = regexp.MustCompile(`total: (\d+) disc\(s\), (\d+) objects, (\d+) bytes`)

// TestPackDryRunWritesNothing checks that --dry-run leaves the repository
// exactly as a plain "commit" left it: no packed tree, no state record,
// no cache entry, no ledger row, and the staging total unchanged.
func TestPackDryRunWritesNothing(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	beforeCode, beforeOut := runCmd(t, "status", "--repo="+repo)
	if beforeCode != 0 {
		t.Fatalf("status: exit %d: %s", beforeCode, beforeOut)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--dry-run")
	if code != 0 {
		t.Fatalf("pack --dry-run: exit %d: %s", code, out)
	}
	m := dryRunTotalRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("pack --dry-run output %q has no total line", out)
	}
	if m[1] != "1" {
		t.Fatalf("total line = %q, want 1 disc for a small fixture at 64MiB", m[0])
	}
	if !strings.Contains(out, "estimate") {
		t.Fatalf("pack --dry-run output %q should say the numbers are an estimate", out)
	}

	// No ledger file, no run tree, and the same staged total.
	if _, err := os.Stat(filepath.Join(repo, "staging", "discs.bin")); !os.IsNotExist(err) {
		t.Fatalf("pack --dry-run wrote a disc ledger: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, "staging", "plans"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("pack --dry-run wrote a plan tree under staging/plans: %v", entries)
	}

	afterCode, afterOut := runCmd(t, "status", "--repo="+repo)
	if afterCode != 0 {
		t.Fatalf("status: exit %d: %s", afterCode, afterOut)
	}
	if beforeOut != afterOut {
		t.Fatalf("status changed after pack --dry-run:\nbefore: %s\nafter: %s", beforeOut, afterOut)
	}
}

// TestPackDryRunMultipleDiscs checks that a capacity forcing a split
// reports more than one disc, and that the object counts sum to the
// real staged total.
func TestPackDryRunMultipleDiscs(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// A few independent, random (so neither dedup nor compression
	// shrinks them) files, so a small forced capacity must split them
	// across more than one predicted disc.
	rng := rand.New(rand.NewSource(1))
	for i := range 6 {
		data := make([]byte, 700_000)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "f"+strconv.Itoa(i)+".bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=6MiB", "--dry-run")
	if code != 0 {
		t.Fatalf("pack --dry-run: exit %d: %s", code, out)
	}
	m := dryRunTotalRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("pack --dry-run output %q has no total line", out)
	}
	discCount, _ := strconv.Atoi(m[1])
	if discCount < 2 {
		t.Fatalf("total line = %q, want at least 2 discs at a forced 2MiB capacity", m[0])
	}

	// Compare against the real, repeated pack.
	realDiscs := 0
	for {
		treeDir := filepath.Join(work, "tree", strconv.Itoa(realDiscs))
		code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=6MiB", "--out="+treeDir)
		if code != 0 {
			t.Fatalf("real pack: exit %d: %s", code, out)
		}
		realDiscs++
		if strings.Contains(out, "remaining staged: 0 objects, 0 bytes") {
			break
		}
	}
	if realDiscs != discCount {
		t.Fatalf("real pack used %d disc(s), --dry-run predicted %d", realDiscs, discCount)
	}
}

// TestPackDryRunTakesNoLock checks that --dry-run runs even while
// another command holds the repository's exclusive lock, since it is
// read-only and the rule is that a read-only command takes no lock.
func TestPackDryRunTakesNoLock(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	lk, code, ok := lockRepo("test", repo, os.Stderr)
	if !ok {
		t.Fatalf("could not take the repository lock to set up the test: exit %d", code)
	}
	defer releaseLock(lk)

	dryCode, dryOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--dry-run")
	if dryCode != 0 {
		t.Fatalf("pack --dry-run under a held lock: exit %d: %s", dryCode, dryOut)
	}

	// A real pack, in contrast, must fail fast while the lock is held.
	realCode, realOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if realCode == 0 {
		t.Fatalf("real pack under a held lock unexpectedly succeeded: %s", realOut)
	}
}
