package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
)

// multiDiscPlanFixture packs writeMultiDiscFixtureSource's tree across
// two small forced capacities, into two discs, and returns the
// repository directory, the snapshot id, one include path known to
// exist under it (sub0), and the disc roots pack built.
func multiDiscPlanFixture(t *testing.T) (repo, snapID, includePath string, discRoots []string) {
	t.Helper()
	work := t.TempDir()
	repo = filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)

	capacities := []string{packSectors(7_000_000), packSectors(7_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, "disc"+string(rune('0'+i)))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}
	return repo, snapID, strings.TrimPrefix(filepath.Join(src, "sub0"), "/"), discRoots
}

// runRestoreDryRun runs "restore --dry-run" against repo for snapID
// (plus any extra flags such as --include), with throwaway --mount and
// OUT-DIR arguments, and returns its exit code and output.
func runRestoreDryRun(t *testing.T, repo, snapID string, extraFlags ...string) (int, string) {
	t.Helper()
	args := append([]string{"restore", "--repo=" + repo}, extraFlags...)
	args = append(args, "--mount="+t.TempDir(), "--dry-run", snapID, filepath.Join(t.TempDir(), "out"))
	return runCmd(t, args...)
}

// TestRestoreDryRunSingleDisc packs one small snapshot onto a single
// disc and checks restore --dry-run reports exactly that one disc, with
// every object accounted for and no missing run.
func TestRestoreDryRunSingleDisc(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	code, out := runRestoreDryRun(t, repo, snapID)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if n := countDiscLines(out); n != 1 {
		t.Fatalf("restore --dry-run output = %q, want exactly one disc line, got %d", out, n)
	}
	if !strings.Contains(out, "totals: 1 discs") {
		t.Fatalf("restore --dry-run output = %q, want totals: 1 discs", out)
	}
	if strings.Contains(out, "missing:") {
		t.Fatalf("restore --dry-run output = %q, did not expect a missing entry", out)
	}
}

// TestRestoreDryRunTwoDiscChainIncludeNarrows packs a snapshot across
// two discs and checks a plain dry run names both, while --include on
// one small subtree names no more objects or bytes than the
// whole-snapshot dry run.
func TestRestoreDryRunTwoDiscChainIncludeNarrows(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, includePath, _ := multiDiscPlanFixture(t)

	code, fullOut := runRestoreDryRun(t, repo, snapID)
	if code != 0 {
		t.Fatalf("restore --dry-run (whole snapshot): exit %d: %s", code, fullOut)
	}
	if n := countDiscLines(fullOut); n != 2 {
		t.Fatalf("restore --dry-run (whole snapshot) = %q, want two disc lines, got %d", fullOut, n)
	}

	code, narrowOut := runRestoreDryRun(t, repo, snapID, "--include="+includePath)
	if code != 0 {
		t.Fatalf("restore --dry-run (--include): exit %d: %s", code, narrowOut)
	}

	fullObjects := dryRunTotalObjects(t, fullOut)
	narrowObjects := dryRunTotalObjects(t, narrowOut)
	if narrowObjects == 0 || narrowObjects >= fullObjects {
		t.Fatalf("restore --dry-run --include totals objects = %d, want > 0 and < whole-snapshot total %d", narrowObjects, fullObjects)
	}
}

// TestRestoreDryRunTwoIncludesBothResolve checks that restore --dry-run
// with two --include flags resolves both, instead of the second failing
// with "matches no entry" because resolving the first corrupted the
// shared root entries.
func TestRestoreDryRunTwoIncludesBothResolve(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	treeDir, snapID, src := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	aPath := rootPath(filepath.Join(src, "a.txt"))
	bPath := rootPath(filepath.Join(src, "sub", "b.txt"))

	code, out := runRestoreDryRun(t, repo, snapID, "--include="+aPath, "--include="+bPath)
	if code != 0 {
		t.Fatalf("restore --dry-run --include=%s --include=%s: exit %d: %s", aPath, bPath, code, out)
	}
	if strings.Contains(out, "matches no entry") {
		t.Fatalf("restore --dry-run output = %q, want both includes resolved", out)
	}
}

// dryRunTotalObjects extracts the "totals: N discs, M objects, K bytes"
// line's objects value from restore --dry-run's text output.
func dryRunTotalObjects(t *testing.T, out string) int {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "totals:") {
			continue
		}
		var discs, objects, bytes int
		if _, err := fmt.Sscanf(line, "totals: %d discs, %d objects, %d bytes", &discs, &objects, &bytes); err != nil {
			t.Fatalf("parse totals line %q: %v", line, err)
		}
		return objects
	}
	t.Fatalf("no totals line in restore --dry-run output %q", out)
	return 0
}

// TestRestoreDryRunMissingDisc deletes one cached disc's INDEX after a
// two-disc pack, so some objects that disc alone stored become
// unresolvable, and checks restore --dry-run reports them under
// "missing:" and exits 1.
func TestRestoreDryRunMissingDisc(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, _ := multiDiscPlanFixture(t)

	cacheDir := repoCacheDir(t, repo)
	if err := os.RemoveAll(filepath.Join(cacheDir, "discs", firstCachedDiscUUID(t, cacheDir))); err != nil {
		t.Fatal(err)
	}

	code, out := runRestoreDryRun(t, repo, snapID)
	if code != 1 {
		t.Fatalf("restore --dry-run: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "missing:") {
		t.Fatalf("restore --dry-run output %q does not report a missing group", out)
	}
	if !strings.Contains(out, "disc unknown") {
		t.Fatalf("restore --dry-run output %q does not name what is missing", out)
	}
}

// firstCachedDiscUUID returns the text form of the uuid of the disc
// with disc_seq 0, the first disc the fixture packed.
func firstCachedDiscUUID(t *testing.T, cacheDir string) string {
	t.Helper()
	c, err := cache.Open(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	discs, err := c.Discs()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range discs.Rows {
		if row.DiscSeq == 0 {
			return uuidText(row.DiscUUID)
		}
	}
	t.Fatal("no cached DISCS row has disc_seq 0")
	return ""
}

// TestRestoreDryRunEmptyCacheNamesTheFix checks that "restore --dry-run"
// against a repository that has never packed or rebuilt anything fails
// with a message naming the fix, not a bare "no run is cached" with no
// next step.
func TestRestoreDryRunEmptyCacheNamesTheFix(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runRestoreDryRun(t, repo, "LATEST")
	if code == 0 {
		t.Fatalf("restore --dry-run (empty cache): exit 0, want a failure: %s", out)
	}
	if !strings.Contains(out, "no disc is cached yet") {
		t.Fatalf("restore --dry-run (empty cache) output %q missing \"no disc is cached yet\"", out)
	}
	if !strings.Contains(out, "recover") {
		t.Fatalf("restore --dry-run (empty cache) output %q missing the fix, recover", out)
	}
}

// countDiscLines counts the per-disc lines of restore --dry-run's text
// output.
func countDiscLines(out string) int {
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "disc ") {
			n++
		}
	}
	return n
}
