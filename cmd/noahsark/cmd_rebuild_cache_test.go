package main

import (
	"encoding/json"
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, "--ref=BASE", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=BASE", "--out="+treeDir); code != 0 {
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
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

// TestRebuildCacheWordingDoesNotClaimClean checks that rebuild-cache's
// summary line never claims CLEAN objects are PACKED: after disc burned
// and a passing verify move a run's objects to CLEAN, a rebuild-cache
// of the same disc, with the state log still in place, must report
// those objects as already past packed, not as newly recorded packed.
func TestRebuildCacheWordingDoesNotClaimClean(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	treeDir := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "verify", "--repo="+repo, "--image="+treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir)
	if code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
	}
	if strings.Contains(out, "recorded packed:") {
		t.Fatalf("rebuild-cache output %q uses the old wording, which would claim CLEAN objects are PACKED", out)
	}
	if !strings.Contains(out, "already past packed") {
		t.Fatalf("rebuild-cache output %q missing a count of objects already past packed", out)
	}
}

// TestRebuildCachePartialNamesMissingDisc packs a sequence across three
// small discs, deletes the repository, and rebuilds from only the last
// disc: the rebuild must exit 1 and name the earlier discs' uuids.
func TestRebuildCachePartialNamesMissingDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
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

// TestRebuildCachePartialUntilEveryDiscFed packs a three-disc chain,
// deletes the repository, feeds only the newest disc, then feeds the
// remaining two: "ok" must wait until every disc named in DISCS has
// itself been fed at least once, not merely copied in from a sibling
// disc's own DISCS table.
func TestRebuildCachePartialUntilEveryDiscFed(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, "fed-disc"+string(rune('0'+i)))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--fec", "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	// Feed only the newest disc: its own DISCS table names the earlier
	// two, but neither was itself read. The rebuild must be partial.
	newest := discRoots[len(discRoots)-1]
	code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+newest)
	if code != 1 {
		t.Fatalf("rebuild-cache (newest only): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not fed yet") {
		t.Fatalf("output %q does not say a disc was not fed yet", out)
	}
	if strings.Contains(out, "rebuild-cache: ok") {
		t.Fatalf("output %q says ok before every disc was fed", out)
	}

	// Feed the remaining two discs: now every disc named in DISCS has
	// itself been fed, and the rebuild must say ok.
	code, out = runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo,
		"--disc="+discRoots[0], "--disc="+discRoots[1])
	if code != 0 {
		t.Fatalf("rebuild-cache (remaining two): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "rebuild-cache: ok") {
		t.Fatalf("output %q does not say ok once every disc is fed", out)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
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

// discListUUIDCount runs "disc list --json" and returns how many discs
// the ledger reports.
func discListUUIDCount(t *testing.T, repo string) int {
	t.Helper()
	code, out := runCmd(t, "disc", "list", "--repo="+repo, "--json")
	if code != 0 {
		t.Fatalf("disc list: exit %d: %s", code, out)
	}
	var listed struct {
		Discs []struct {
			UUID string `json:"uuid"`
		} `json:"discs"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("disc list output: %v: %s", err, out)
	}
	return len(listed.Discs)
}

// TestRebuildCacheOneDiscAtATimeMergesLedger packs a three-disc chain,
// then rebuilds the repository state one disc at a time, in both
// newest-first and oldest-first order. A rebuild-cache call must merge
// into whatever an earlier call already saved, not replace it: before
// the fix, each single-disc call overwrote the ledger and the refs with
// only that call's own disc, so the last call always left the ledger
// down to one disc.
func TestRebuildCacheOneDiscAtATimeMergesLedger(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--fec", "--out="+treeDir); code != 0 && i != len(capacities)-1 {
			if code == 2 {
				t.Fatalf("pack %d: exit %d: %s", i, code, out)
			}
		}
		discRoots = append(discRoots, treeDir)
	}

	for _, order := range []struct {
		name string
		seq  []int
	}{
		{"newest-first", []int{2, 1, 0}},
		{"oldest-first", []int{0, 1, 2}},
	} {
		t.Run(order.name, func(t *testing.T) {
			if err := os.RemoveAll(repo); err != nil {
				t.Fatal(err)
			}

			var lastCode int
			var lastOut string
			for _, i := range order.seq {
				lastCode, lastOut = runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+discRoots[i])
			}
			if lastCode != 0 {
				t.Fatalf("last rebuild-cache call: exit %d, want 0: %s", lastCode, lastOut)
			}
			if !strings.Contains(lastOut, "rebuild-cache: ok") {
				t.Fatalf("last rebuild-cache call output %q does not say ok", lastOut)
			}

			if got := discListUUIDCount(t, repo); got != 3 {
				t.Fatalf("disc list shows %d disc(s), want 3", got)
			}

			// The next pack must take the next free disc_seq, not
			// reuse one already in the ledger.
			if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
				t.Fatalf("re-commit: exit %d: %s", code, out)
			}
			fourthDir := filepath.Join(t.TempDir(), "disc3")
			if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+packSectors(10_000_000), "--out="+fourthDir); code != 0 {
				t.Fatalf("fourth pack: exit %d: %s", code, out)
			}
			if got := discListUUIDCount(t, repo); got != 4 {
				t.Fatalf("disc list shows %d disc(s) after the fourth pack, want 4", got)
			}
		})
	}
}

// TestRebuildCacheKeepsUnpackedRef commits a ref that is never packed,
// then runs rebuild-cache from an unrelated packed disc. The unpacked
// ref must survive in refs.txt, and a following pack must carry it.
// Before the fix, writeRefs replaced refs.txt with only the names found
// on the provided disc, dropping the unpacked one.
func TestRebuildCacheKeepsUnpackedRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	baseSrc := writeRefsCarryFixture(t, "base")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=BASE", baseSrc); code != 0 {
		t.Fatalf("commit BASE: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=BASE", "--out="+treeDir); code != 0 {
		t.Fatalf("pack BASE: exit %d: %s", code, out)
	}

	xSrc := writeRefsCarryFixture(t, "x")
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=X", xSrc); code != 0 {
		t.Fatalf("commit X: exit %d: %s", code, out)
	}

	if code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
	}

	if _, err := resolveRef(repo, "X"); err != nil {
		t.Fatalf("resolveRef(X) after rebuild-cache: %v, want the unpacked ref to survive", err)
	}

	secondTree := filepath.Join(work, "tree2")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=X", "--out="+secondTree); code != 0 {
		t.Fatalf("pack X: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "log", secondTree)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "X") {
		t.Fatalf("log output does not mention ref X: %s", out)
	}
}

// TestConfigStagingDirSurvivesRepositoryRename reproduces renaming a
// repository directory out of the way before rebuilding a fresh one at
// its old path: "mv repo repo.lost", then "rebuild-cache" into a new
// "repo". A command still pointed at "repo.lost" (--repo=repo.lost)
// must stage into repo.lost/staging, never into the new repo's own
// staging directory, even though repo.lost's config was written
// before the rename.
func TestConfigStagingDirSurvivesRepositoryRename(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	lost := filepath.Join(work, "repo.lost")
	if err := os.Rename(repo, lost); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
	}

	// Now commit against the old, renamed directory: it must stage
	// under repo.lost/staging, the directory that moved with it, not
	// under the freshly rebuilt repo's own staging directory.
	src2 := writeFixtureSource(t)
	if code, out := runCmd(t, "commit", "--repo="+lost, src2); code != 0 {
		t.Fatalf("commit --repo=%s: exit %d: %s", lost, code, out)
	}
	if entries, err := os.ReadDir(filepath.Join(lost, "staging", "objects")); err != nil {
		t.Fatalf("read %s/staging/objects: %v", lost, err)
	} else if len(entries) == 0 {
		t.Fatalf("%s/staging/objects is empty; the commit staged somewhere else", lost)
	}

	rebuiltCount, err := countFiles(filepath.Join(repo, "staging", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	if rebuiltCount != 0 {
		t.Fatalf("the rebuilt repository's own staging/objects has %d file(s); the commit against --repo=%s leaked into it", rebuiltCount, lost)
	}
}

// TestRebuildCacheWarnsSeqContinuesFromNewestFed asserts the stderr
// warning rebuild-cache prints once it exits ok: it names the newest
// disc actually fed, and the run_seq and disc_seq the next pack will
// assign.
func TestRebuildCacheWarnsSeqContinuesFromNewestFed(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=disc-one", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir)
	if code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "disc-one") {
		t.Fatalf("output %q does not name the fed disc's label", out)
	}
	if !strings.Contains(out, "run_seq 2, disc_seq 1") {
		t.Fatalf("output %q does not state the next pack's numbers", out)
	}
	if !strings.Contains(out, "feed every disc") {
		t.Fatalf("output %q does not tell the operator to feed every disc", out)
	}
}

// TestRebuildCacheRefusesAReintroducedLostDisc packs two discs, loses
// the repository together with the second (newer) disc, rebuilds from
// the first disc alone, and packs a third disc: this repeats case A's
// experiment and lands the third disc on the same run_seq and disc_seq
// the lost second disc once had. It then feeds the lost second disc
// back in and asserts rebuild-cache refuses, naming both disc uuids,
// and leaves the ledger untouched, rather than silently letting two
// discs share one sequence number.
func TestRebuildCacheRefusesAReintroducedLostDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	discOne := filepath.Join(work, "disc-one")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=one", "--out="+discOne); code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, out)
	}

	src2 := writeFixtureSource(t)
	if code, out := runCmd(t, "commit", "--repo="+repo, src2); code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	discTwoLost := filepath.Join(work, "disc-two-lost")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=two", "--out="+discTwoLost); code != 0 {
		t.Fatalf("pack 2: exit %d: %s", code, out)
	}

	// Lose the repository and the second disc; only the first survives.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+discOne); code != 0 {
		t.Fatalf("rebuild-cache (disc one only): exit %d: %s", code, out)
	}

	src3 := writeFixtureSource(t)
	if code, out := runCmd(t, "commit", "--repo="+repo, src3); code != 0 {
		t.Fatalf("commit 3: exit %d: %s", code, out)
	}
	discThree := filepath.Join(work, "disc-three")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=three", "--out="+discThree); code != 0 {
		t.Fatalf("pack 3: exit %d: %s", code, out)
	}

	if got := discListUUIDCount(t, repo); got != 2 {
		t.Fatalf("discs known before the reintroduced disc = %d, want 2", got)
	}

	// The "lost" second disc turns up after all. Feeding it now must be
	// refused: its run_seq and disc_seq are already the third disc's.
	code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+discTwoLost)
	if code != 1 {
		t.Fatalf("rebuild-cache (reintroduced disc two): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "share a sequence number") {
		t.Fatalf("output %q does not refuse over a shared sequence number", out)
	}

	if got := discListUUIDCount(t, repo); got != 2 {
		t.Fatalf("discs known after the refused rebuild = %d, want 2 (unchanged)", got)
	}
}
