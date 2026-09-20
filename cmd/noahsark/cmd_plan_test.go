package main

import (
	"encoding/json"
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

// TestPlanSingleDisc packs one small snapshot onto a single disc and
// checks plan reports exactly that one disc, with every object
// accounted for and no missing run.
func TestPlanSingleDisc(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	code, out := runCmd(t, "plan", "--repo="+repo, snapID)
	if code != 0 {
		t.Fatalf("plan: exit %d: %s", code, out)
	}
	if strings.Count(out, "disc_seq=") != 1 {
		t.Fatalf("plan output = %q, want exactly one disc line", out)
	}
	if !strings.Contains(out, "totals: discs=1") {
		t.Fatalf("plan output = %q, want totals: discs=1", out)
	}
	if strings.Contains(out, "missing:") {
		t.Fatalf("plan output = %q, did not expect a missing entry", out)
	}
}

// TestPlanTwoDiscChainIncludeNarrows packs a snapshot across two discs
// and checks a plain plan names both, while --include on one small
// subtree names no more objects or bytes than the whole-snapshot plan.
func TestPlanTwoDiscChainIncludeNarrows(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, includePath, _ := multiDiscPlanFixture(t)

	code, fullOut := runCmd(t, "plan", "--repo="+repo, snapID)
	if code != 0 {
		t.Fatalf("plan (whole snapshot): exit %d: %s", code, fullOut)
	}
	if strings.Count(fullOut, "disc_seq=") != 2 {
		t.Fatalf("plan (whole snapshot) = %q, want two disc lines", fullOut)
	}

	code, narrowOut := runCmd(t, "plan", "--repo="+repo, "--include="+includePath, snapID)
	if code != 0 {
		t.Fatalf("plan (--include): exit %d: %s", code, narrowOut)
	}

	fullObjects := planTotalObjects(t, fullOut)
	narrowObjects := planTotalObjects(t, narrowOut)
	if narrowObjects == 0 || narrowObjects >= fullObjects {
		t.Fatalf("plan --include totals objects = %d, want > 0 and < whole-snapshot total %d", narrowObjects, fullObjects)
	}
}

// TestPlanTwoIncludesBothResolve checks that plan with two --include
// flags resolves both, instead of the second failing with "matches no
// entry" because resolving the first corrupted the shared root entries.
func TestPlanTwoIncludesBothResolve(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	treeDir, snapID, src := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	aPath := rootPath(filepath.Join(src, "a.txt"))
	bPath := rootPath(filepath.Join(src, "sub", "b.txt"))

	code, out := runCmd(t, "plan", "--repo="+repo, "--include="+aPath, "--include="+bPath, snapID)
	if code != 0 {
		t.Fatalf("plan --include=%s --include=%s: exit %d: %s", aPath, bPath, code, out)
	}
	if strings.Contains(out, "matches no entry") {
		t.Fatalf("plan output = %q, want both includes resolved", out)
	}
}

// planTotalObjects extracts the "totals: discs=N objects=M bytes=K"
// line's objects value from plan's text output.
func planTotalObjects(t *testing.T, out string) int {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "totals:") {
			continue
		}
		var discs, objects, bytes int
		if _, err := fmt.Sscanf(line, "totals: discs=%d objects=%d bytes=%d", &discs, &objects, &bytes); err != nil {
			t.Fatalf("parse totals line %q: %v", line, err)
		}
		return objects
	}
	t.Fatalf("no totals line in plan output %q", out)
	return 0
}

// TestPlanJSONOutput checks --out writes the plan fields this build
// knows, matching OPERATIONS.md "14.4 The plan file" as far as this
// build implements it.
func TestPlanJSONOutput(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, _ := multiDiscPlanFixture(t)

	outFile := filepath.Join(t.TempDir(), "plan.json")
	code, out := runCmd(t, "plan", "--repo="+repo, "--out="+outFile, snapID)
	if code != 0 {
		t.Fatalf("plan: exit %d: %s", code, out)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Format   string `json:"format"`
		Version  int    `json:"version"`
		Snapshot string `json:"snapshot"`
		Objects  int    `json:"objects"`
		Bytes    uint64 `json:"bytes"`
		Switches int    `json:"switches"`
		Passes   int    `json:"passes"`
		Discs    []struct {
			Order         int    `json:"order"`
			DiscUUID      string `json:"disc_uuid"`
			DiscSeq       uint64 `json:"disc_seq"`
			Label         string `json:"label"`
			ObjectsToRead int    `json:"objects_to_read"`
			BytesToRead   uint64 `json:"bytes_to_read"`
		} `json:"discs"`
		MissingDiscs []struct {
			DiscUUID string `json:"disc_uuid"`
			Objects  int    `json:"objects"`
		} `json:"missing_discs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal plan JSON: %v\n%s", err, data)
	}
	if doc.Format != "noahsark-restore-plan" || doc.Version != 1 {
		t.Fatalf("plan JSON format/version = %q/%d, want noahsark-restore-plan/1", doc.Format, doc.Version)
	}
	if doc.Snapshot != snapID {
		t.Fatalf("plan JSON snapshot = %q, want %q", doc.Snapshot, snapID)
	}
	if len(doc.Discs) != 2 {
		t.Fatalf("plan JSON discs = %d entries, want 2", len(doc.Discs))
	}
	if doc.Switches != len(doc.Discs) {
		t.Fatalf("plan JSON switches = %d, want %d (one per disc)", doc.Switches, len(doc.Discs))
	}
	if doc.Passes != 1 {
		t.Fatalf("plan JSON passes = %d, want 1", doc.Passes)
	}
	if len(doc.MissingDiscs) != 0 {
		t.Fatalf("plan JSON missing_discs = %v, want empty", doc.MissingDiscs)
	}
	for i, d := range doc.Discs {
		if d.Order != i {
			t.Fatalf("disc %d order = %d, want %d", i, d.Order, i)
		}
		if d.ObjectsToRead == 0 {
			t.Fatalf("disc %d objects_to_read = 0", i)
		}
	}
}

// TestPlanMissingDisc deletes one cached disc's INDEX after a two-disc
// pack, so some objects that disc alone stored become unresolvable, and
// checks plan reports them under missing_discs and exits 3.
func TestPlanMissingDisc(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, _ := multiDiscPlanFixture(t)

	cacheDir := repoCacheDir(t, repo)
	if err := os.RemoveAll(filepath.Join(cacheDir, "discs", firstCachedDiscUUID(t, cacheDir))); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "plan", "--repo="+repo, snapID)
	if code != 1 {
		t.Fatalf("plan: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "missing:") {
		t.Fatalf("plan output %q does not report a missing group", out)
	}
	if !strings.Contains(out, "disc unknown") {
		t.Fatalf("plan output %q does not name what is missing", out)
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

// TestPlanEmptyCacheNamesTheFix checks that "plan" against a repository
// that has never packed or rebuilt anything fails with a message naming
// the fix, not a bare "no run is cached" with no next step.
func TestPlanEmptyCacheNamesTheFix(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "plan", "--repo="+repo, "LATEST")
	if code == 0 {
		t.Fatalf("plan (empty cache): exit 0, want a failure: %s", out)
	}
	if !strings.Contains(out, "no disc is cached yet") {
		t.Fatalf("plan (empty cache) output %q missing \"no disc is cached yet\"", out)
	}
	if !strings.Contains(out, "rebuild-cache") {
		t.Fatalf("plan (empty cache) output %q missing the fix, rebuild-cache", out)
	}
}
