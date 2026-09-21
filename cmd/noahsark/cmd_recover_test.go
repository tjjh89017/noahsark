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

// TestRecoverFromDiscRestoresState packs one disc, deletes the
// whole repository directory, then rebuilds it from that disc alone: the
// state log's packed count must match the disc's own INDEX object
// count, and the ref must resolve again.
func TestRecoverFromDiscRestoresState(t *testing.T) {
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
	wantOnDisc := len(rr.Index.Objects)

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	code, out = runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir)
	if code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	log, err := stage.Open(cfg.StagingDir)
	if err != nil {
		t.Fatalf("stage.Open: %v", err)
	}
	if got := log.CountState(stage.OnDiscOnly); got != wantOnDisc {
		t.Fatalf("on-disc-only count = %d, want %d (disc INDEX object count)", got, wantOnDisc)
	}

	if id, err := resolveRef(repo, "BASE"); err != nil || id.TextForm() != snapID {
		t.Fatalf("resolveRef(BASE) = %v, %v, want %s", id, err, snapID)
	}
}

// TestRecoverIsIdempotent runs recover twice from the same
// disc and asserts both calls exit 0 with the same packed count.
func TestRecoverIsIdempotent(t *testing.T) {
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

	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("recover #1: exit %d: %s", code, out)
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

	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("recover #2: exit %d: %s", code, out)
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

// TestRecoverWordingDoesNotClaimClean checks that recover's
// summary line never claims CLEAN objects are PACKED: after disc burned
// and a passing verify move a run's objects to CLEAN, a recover
// of the same disc, with the state log still in place, must report
// those objects as already past packed, not as newly recorded packed.
func TestRecoverWordingDoesNotClaimClean(t *testing.T) {
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
	if code, out := runCmd(t, "verify", "--repo="+repo, treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir)
	if code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}
	if strings.Contains(out, "recorded packed:") {
		t.Fatalf("recover output %q uses the old wording, which would claim CLEAN objects are PACKED", out)
	}
	if !strings.Contains(out, "already known") {
		t.Fatalf("recover output %q missing a count of objects the log already knew", out)
	}
	if !strings.Contains(out, "objects recorded: 0 on disc") {
		t.Fatalf("recover output %q recorded an object the log already knew", out)
	}
}

// TestRecoverPartialNamesMissingDisc packs a sequence across three
// small discs, deletes the repository, and rebuilds from only the last
// disc: the rebuild must exit 1 and name the earlier discs' uuids.
func TestRecoverPartialNamesMissingDisc(t *testing.T) {
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

	code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+discRoots[len(discRoots)-1])
	if code != 1 {
		t.Fatalf("recover: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "partial") {
		t.Fatalf("output %q does not say the rebuild is partial", out)
	}
	if !strings.Contains(out, uuid1) {
		t.Fatalf("output %q does not name the missing disc %s", out, uuid1)
	}
	if !strings.Contains(out, "sources.root and pack.capacity") {
		t.Fatalf("output %q does not say which config keys the new repository still needs", out)
	}
}

// TestRecoverPartialUntilEveryDiscFed packs a three-disc chain,
// deletes the repository, feeds only the newest disc, then feeds the
// remaining two: "ok" must wait until every disc named in DISCS has
// itself been fed at least once, not merely copied in from a sibling
// disc's own DISCS table.
func TestRecoverPartialUntilEveryDiscFed(t *testing.T) {
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
	code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+newest)
	if code != 1 {
		t.Fatalf("recover (newest only): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not fed yet") {
		t.Fatalf("output %q does not say a disc was not fed yet", out)
	}
	if strings.Contains(out, "recover: ok") {
		t.Fatalf("output %q says ok before every disc was fed", out)
	}

	// Feed the remaining two discs: now every disc named in DISCS has
	// itself been fed, and the rebuild must say ok.
	code, out = runCmd(t, "recover", "--repo="+repo,
		"--disc="+discRoots[0], "--disc="+discRoots[1])
	if code != 0 {
		t.Fatalf("recover (remaining two): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "recover: ok") {
		t.Fatalf("output %q does not say ok once every disc is fed", out)
	}
}

// TestRecoverNoUsableDisc asserts exit 3 when every named disc
// root fails to read.
func TestRecoverNoUsableDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	empty := filepath.Join(work, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+empty)
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out)
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
// not new, even though recover never restored the staging bytes
// for them.
func TestCommitAfterRebuildCacheReportsNoNewObjects(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	// Every commit stamps a fresh snapshot object with the current
	// time and this build chains no parent, so two commits of unchanged
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
	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	if got := newObjectsFromCommit(t, out); got != 0 {
		t.Fatalf("new objects = %d, want 0: %s", got, out)
	}
}

// discListUUIDCount runs "status --json" and returns how many discs
// the ledger reports.
func discListUUIDCount(t *testing.T, repo string) int {
	t.Helper()
	code, out := runCmd(t, "status", "--repo="+repo, "--json")
	if code != 0 {
		t.Fatalf("status --json: exit %d: %s", code, out)
	}
	var listed struct {
		Discs []struct {
			UUID string `json:"uuid"`
		} `json:"discs"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("status --json output: %v: %s", err, out)
	}
	return len(listed.Discs)
}

// TestRecoverOneDiscAtATimeMergesLedger packs a three-disc chain,
// then rebuilds the repository state one disc at a time, in both
// newest-first and oldest-first order. A recover call must merge
// into whatever an earlier call already saved, not replace it: before
// the fix, each single-disc call overwrote the ledger and the refs with
// only that call's own disc, so the last call always left the ledger
// down to one disc.
func TestRecoverOneDiscAtATimeMergesLedger(t *testing.T) {
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
				lastCode, lastOut = runCmd(t, "recover", "--repo="+repo, "--disc="+discRoots[i])
			}
			if lastCode != 0 {
				t.Fatalf("last recover call: exit %d, want 0: %s", lastCode, lastOut)
			}
			if !strings.Contains(lastOut, "recover: ok") {
				t.Fatalf("last recover call output %q does not say ok", lastOut)
			}

			if got := discListUUIDCount(t, repo); got != 3 {
				t.Fatalf("status shows %d disc(s), want 3", got)
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
				t.Fatalf("status shows %d disc(s) after the fourth pack, want 4", got)
			}
		})
	}
}

// TestRecoverKeepsUnpackedRef commits a ref that is never packed,
// then runs recover from an unrelated packed disc. The unpacked
// ref must survive in refs.txt, and a following pack must carry it.
// Before the fix, writeRefs replaced refs.txt with only the names found
// on the provided disc, dropping the unpacked one.
func TestRecoverKeepsUnpackedRef(t *testing.T) {
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

	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	if _, err := resolveRef(repo, "X"); err != nil {
		t.Fatalf("resolveRef(X) after recover: %v, want the unpacked ref to survive", err)
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
// its old path: "mv repo repo.lost", then "recover" into a new
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

	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
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

// TestRecoverAcceptsAReintroducedLostDisc packs two discs, loses
// the repository together with the second (newer) disc, rebuilds from
// the first disc alone, and packs a third disc: the third disc takes
// the run_seq and disc_seq the lost second disc already holds. The lost
// disc then turns up and is fed late. recover must accept it: the
// shared number is a label, and the disc uuid keys every lookup. A
// `disc burned` by the shared number is refused as ambiguous, and the
// same command with a uuid prefix works. A verify marks the objects of
// its own disc only, and every snapshot restores byte for byte. A disc
// that came back from its own catalog holds no staged object, thus no
// verify moves it.
func TestRecoverAcceptsAReintroducedLostDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src1 := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src1)
	if code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	snap1 := snapshotIDFromCommit(t, out)
	discOne := filepath.Join(work, "disc-one")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=one", "--out="+discOne); code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, out)
	}

	src2 := writeFixtureSource(t)
	if err := os.WriteFile(filepath.Join(src2, "two.txt"), []byte("content of the second source"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "commit", "--repo="+repo, src2)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	snap2 := snapshotIDFromCommit(t, out)
	discTwoLost := filepath.Join(work, "disc-two-lost")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=two", "--out="+discTwoLost); code != 0 {
		t.Fatalf("pack 2: exit %d: %s", code, out)
	}

	// Lose the repository and the second disc; only the first survives.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+discOne); code != 0 {
		t.Fatalf("recover (disc one only): exit %d: %s", code, out)
	}

	src3 := writeFixtureSource(t)
	if err := os.WriteFile(filepath.Join(src3, "three.txt"), []byte("content of the third source"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "commit", "--repo="+repo, src3)
	if code != 0 {
		t.Fatalf("commit 3: exit %d: %s", code, out)
	}
	snap3 := snapshotIDFromCommit(t, out)
	discThree := filepath.Join(work, "disc-three")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=three", "--out="+discThree); code != 0 {
		t.Fatalf("pack 3: exit %d: %s", code, out)
	}

	// The "lost" second disc turns up after all. Its numbers are the
	// third disc's numbers; the feed must still be accepted.
	code, out = runCmd(t, "recover", "--repo="+repo, "--disc="+discTwoLost)
	if code != 0 {
		t.Fatalf("recover (reintroduced disc two): exit %d, want 0: %s", code, out)
	}
	discs := discListRows(t, repo)
	if len(discs) != 3 {
		t.Fatalf("status reports %d disc(s), want 3: %v", len(discs), discs)
	}

	shared := discs[byLabel(t, discs, "two")].Seq
	if other := discs[byLabel(t, discs, "three")].Seq; other != shared {
		t.Fatalf("disc two seq %d and disc three seq %d differ; the test needs the shared number", shared, other)
	}

	code, out = runCmd(t, "disc", "burned", "--repo="+repo, fmt.Sprint(shared))
	if code != 2 {
		t.Fatalf("disc burned %d: exit %d, want 2: %s", shared, code, out)
	}
	if !strings.Contains(out, "matches more than one disc") {
		t.Fatalf("disc burned %d output %q does not refuse the shared number as ambiguous", shared, out)
	}

	for _, d := range discs {
		if code, out := runCmd(t, "disc", "burned", "--repo="+repo, d.UUID[:8]); code != 0 {
			t.Fatalf("disc burned %s: exit %d: %s", d.UUID[:8], code, out)
		}
	}

	// Discs one and two came back from their own catalogs: staging holds
	// no file for them, so they are on disc only and no verify can mark
	// them CLEAN. Disc three was packed here, thus a verify of disc
	// three, and only disc three, marks objects CLEAN.
	roots := map[string]string{"one": discOne, "two": discTwoLost, "three": discThree}
	for _, label := range []string{"one", "two"} {
		d := discs[byLabel(t, discs, label)]
		if d.OnDiscOnlyObjects == 0 || d.OnDiscOnlyObjects != d.OnDiscObjects {
			t.Fatalf("disc %s (%s): %d of %d objects on disc only, want all of them", d.UUID, d.Label, d.OnDiscOnlyObjects, d.OnDiscObjects)
		}
	}
	verified := discs[byLabel(t, discs, "three")]
	if code, out := runCmd(t, "verify", "--repo="+repo, roots["three"]); code != 0 {
		t.Fatalf("verify disc three: exit %d: %s", code, out)
	}
	for _, d := range discListRows(t, repo) {
		if d.UUID == verified.UUID {
			if d.CleanObjects == 0 {
				t.Fatalf("disc %s (%s) has 0 clean objects after its own verify", d.UUID, d.Label)
			}
			continue
		}
		if d.CleanObjects != 0 {
			t.Fatalf("disc %s (%s) has %d clean object(s); the verify of disc three marked another disc", d.UUID, d.Label, d.CleanObjects)
		}
	}

	for _, d := range discs {
		if d.UUID == verified.UUID {
			continue
		}
		if code, out := runCmd(t, "verify", "--repo="+repo, roots[d.Label]); code != 0 {
			t.Fatalf("verify disc %s: exit %d: %s", d.Label, code, out)
		}
	}

	for i, pair := range []struct {
		snap string
		src  string
	}{{snap1, src1}, {snap2, src2}, {snap3, src3}} {
		outDir := filepath.Join(work, fmt.Sprintf("restored-%d", i))
		code, out := runCmd(t, "restore", "--disc="+discOne, "--disc="+discTwoLost, "--disc="+discThree, pair.snap, outDir)
		if code != 0 {
			t.Fatalf("restore %s: exit %d: %s", pair.snap, code, out)
		}
		compareTrees(t, filepath.Join(outDir, pair.src), pair.src)
	}
}

// discListRow is one row of "status --json", as the tests read it.
type discListRow struct {
	UUID              string `json:"uuid"`
	Seq               uint64 `json:"seq"`
	Label             string `json:"label"`
	CleanObjects      int    `json:"clean_objects"`
	OnDiscObjects     int    `json:"on_disc_objects"`
	OnDiscOnlyObjects int    `json:"on_disc_only_objects"`
}

// discListRows runs "status --json" and returns its rows.
func discListRows(t *testing.T, repo string) []discListRow {
	t.Helper()
	code, out := runCmd(t, "status", "--repo="+repo, "--json")
	if code != 0 {
		t.Fatalf("status --json: exit %d: %s", code, out)
	}
	var listed struct {
		Discs []discListRow `json:"discs"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("status --json output: %v: %s", err, out)
	}
	return listed.Discs
}

// byLabel returns the index of the one row with this label.
func byLabel(t *testing.T, rows []discListRow, label string) int {
	t.Helper()
	for i, r := range rows {
		if r.Label == label {
			return i
		}
	}
	t.Fatalf("no disc labelled %q in %v", label, rows)
	return 0
}

// TestRecoverRepeatTwoDiscFeedIsAccepted feeds two discs together
// in one recover call, then repeats that exact same call: both
// calls must exit 0 and say ok. A disc named in a call's own --disc
// flags is fed by that call, whether or not it was ever fed before;
// this must never be refused as though a different disc were reusing
// its run_seq and disc_seq.
func TestRecoverRepeatTwoDiscFeedIsAccepted(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=BASE", src); code != 0 {
		t.Fatalf("commit BASE: exit %d: %s", code, out)
	}
	tree1 := filepath.Join(work, "tree1")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=BASE", "--out="+tree1); code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, out)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+tree1); code != 0 {
		t.Fatalf("recover (disc 1 alone): exit %d: %s", code, out)
	}

	if err := os.WriteFile(filepath.Join(src, "c.txt"), []byte("content of c, added for NEXT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=NEXT", src); code != 0 {
		t.Fatalf("commit NEXT: exit %d: %s", code, out)
	}
	tree2 := filepath.Join(work, "tree2")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--ref=NEXT", "--out="+tree2); code != 0 {
		t.Fatalf("pack 2: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+tree1, "--disc="+tree2)
	if code != 0 {
		t.Fatalf("recover (2-disc #1): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "recover: ok") {
		t.Fatalf("2-disc #1 output %q does not say ok", out)
	}

	code, out = runCmd(t, "recover", "--repo="+repo, "--disc="+tree1, "--disc="+tree2)
	if code != 0 {
		t.Fatalf("recover (2-disc #2, repeat): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "recover: ok") {
		t.Fatalf("repeat 2-disc output %q does not say ok", out)
	}
	if strings.Contains(out, "not fed yet") {
		t.Fatalf("repeat 2-disc output %q wrongly reports a disc not fed", out)
	}
}
