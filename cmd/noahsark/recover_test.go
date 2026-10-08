package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// recoverDisc registers root as a read-only mount in the fake mount
// table, and runs recover of root into repo with the source src.
func recoverDisc(t *testing.T, repo, src, root string) (int, string) {
	t.Helper()
	addFakeMount(t, root, true)
	return runCmd(t, "--repo="+repo, "recover", "--source="+src, "--disc="+root)
}

// TestRebuildCatalogRestoresCatalogContent deletes the whole catalog pack
// left behind, along with the repository, and checks recover
// from the packed tree alone puts back an equally complete catalog.
func TestRebuildCatalogRestoresCatalogContent(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", "--ref=BASE", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapIDText := snapshotIDFromCommit(t, out)
	snapID, err := object.ParseID(snapIDText)
	if err != nil {
		t.Fatal(err)
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	beforeTrees, err := filepath.Glob(filepath.Join(repoCatalogDir(t, repo), "trees", "*", "*"))
	if err != nil {
		t.Fatalf("read trees before: %v", err)
	}

	// The catalog lives inside the repository directory, so removing the
	// repository removes the catalog with it: this is the rebuild case
	// recover must handle, the catalog lost along with everything else.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	if code, out := recoverDisc(t, repo, src, treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatalf("catalog.Open: %v", err)
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

	afterTrees, err := filepath.Glob(filepath.Join(repoCatalogDir(t, repo), "trees", "*", "*"))
	if err != nil {
		t.Fatalf("read trees after: %v", err)
	}
	if len(afterTrees) != len(beforeTrees) {
		t.Fatalf("tree count after rebuild = %d, want %d", len(afterTrees), len(beforeTrees))
	}
}

// TestRecoverFromDiscRestoresState packs one disc, deletes the
// whole repository directory, then rebuilds it from that disc alone: the
// state log's packed count must match the disc's own INDEX object
// count, and the ref must resolve again.
// TestRecoverNamesMissingOption checks that recover without --source or
// --disc names the missing option before the usage line.
func TestRecoverNamesMissingOption(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"recover", "--disc=/mnt/ark"}, "noahsark: recover needs --source=PATH\n"},
		{[]string{"recover", "--source=/srv/data"}, "noahsark: recover needs --disc=DIR\n"},
		{[]string{"recover"}, "noahsark: recover needs --source=PATH\n"},
	} {
		code, out := runCmd(t, c.args...)
		if code != 2 || !strings.Contains(out, c.want) || !strings.Contains(out, "usage: noahsark recover --source=PATH --disc=DIR") {
			t.Errorf("%v: exit %d, output %q, want 2, %q and the usage line", c.args, code, out, c.want)
		}
	}
}

// TestVerifyAsksForTheMount checks that verify of a directory with no
// DISC.bin asks whether the disc is mounted.
func TestVerifyAsksForTheMount(t *testing.T) {
	dir := t.TempDir()
	code, out := runCmd(t, "verify", dir)
	want := "cannot read the disc: no DISC.bin under " + dir + " or " + filepath.Join(dir, "NOAHSARK") + "; is the disc mounted at " + dir + "?"
	if code != 1 || !strings.Contains(out, want) {
		t.Fatalf("exit %d, output %q, want 1 and %q", code, out, want)
	}
}

func TestRecoverFromDiscRestoresState(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", "--ref=BASE", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
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

	code, out = recoverDisc(t, repo, src, treeDir)
	if code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	if got := countByState(t, repo, stage.OnDisc); got != wantOnDisc {
		t.Fatalf("on-disc-only count = %d, want %d (disc INDEX object count)", got, wantOnDisc)
	}

	if id, err := resolveRef(testLayout(t, repo).refsFile(), "BASE"); err != nil || id.TextForm() != snapID {
		t.Fatalf("resolveRef(BASE) = %v, %v, want %s", id, err, snapID)
	}
}

// TestRecoverIsIdempotent runs recover twice from the same disc. Both
// calls exit 0. The second call knows the disc, and writes no item
// record.
func TestRecoverIsIdempotent(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}

	if code, out := recoverDisc(t, repo, src, treeDir); code != 0 {
		t.Fatalf("recover #1: exit %d: %s", code, out)
	}
	count1 := countByState(t, repo, stage.OnDisc)

	code, out := recoverDisc(t, repo, src, treeDir)
	if code != 0 {
		t.Fatalf("recover #2: exit %d: %s", code, out)
	}
	wantLines(t, out, "recover: ok; disc 0 \"", "\" already known\n", nextStatusLine)
	if count2 := countByState(t, repo, stage.OnDisc); count1 != count2 {
		t.Fatalf("on-disc count changed across a repeat rebuild: %d then %d", count1, count2)
	}
}

// TestRecoverOfVerifiedDiscWritesNoEvent recovers a verified disc of
// the repository: recover says that it knows the disc, and writes no
// event and no item record.
func TestRecoverOfVerifiedDiscWritesNoEvent(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	treeDir := filepath.Join(work, "disc")
	copyTree(t, packedTreeDir(t, repo, packOut), treeDir)
	discUUID := packedDiscUUID(t, packOut)

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "verify", treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	before := discState(t, repo, discUUID)

	code, out := recoverDisc(t, repo, src, treeDir)
	if code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}
	wantLines(t, out, "recover: ok; disc 0 \"", "\" already known\n", nextStatusLine)
	if after := discState(t, repo, discUUID); after != before {
		t.Fatalf("disc record %+v after recover, want %+v", after, before)
	}
	if n := countByState(t, repo, stage.OnDisc); n != 0 {
		t.Fatalf("%d on-disc item(s), want 0", n)
	}
}

// TestRecoverPartialNamesMissingDisc packs a sequence across three
// small discs, deletes the repository, and rebuilds from only the last
// disc: the rebuild must exit 1 and name the earlier discs' uuids.
func TestRecoverPartialNamesMissingDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	var discRoots []string
	capacities := []string{packSectors(6_000_000), packSectors(6_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, "disc"+string(rune('0'+i)))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--out="+treeDir); code == 2 {
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

	code, out := recoverDisc(t, repo, src, discRoots[len(discRoots)-1])
	if code != 1 {
		t.Fatalf("recover: exit %d, want 1: %s", code, out)
	}
	wantLines(t, out, "recover: disc 0 \"", "\" ("+uuid1+") named by another disc, not yet given\n", nextStatusLine)
	if strings.Contains(out, "config:") {
		t.Fatalf("output %q names config keys to complete; recover prints no such hint", out)
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	// The first two discs pack into remainingDir, and the newest packs
	// elsewhere, so passing those two disc roots later feeds exactly
	// them without also re-feeding the newest.
	remainingDir := filepath.Join(work, "remaining")
	var discRoots []string
	capacities := []string{packSectors(6_000_000), packSectors(6_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		dir := remainingDir
		if i == len(capacities)-1 {
			dir = filepath.Join(work, "newest")
		}
		treeDir := filepath.Join(dir, "fed-disc"+string(rune('0'+i)))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--out="+treeDir); code == 2 {
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
	code, out := recoverDisc(t, repo, src, newest)
	if code != 1 {
		t.Fatalf("recover (newest only): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, " named by another disc, not yet given\n") {
		t.Fatalf("output %q does not name a disc that was not given yet", out)
	}
	if strings.Contains(out, "recover: ok") {
		t.Fatalf("output %q says ok before every disc was fed", out)
	}

	// A disc the ledger names but recover never read holds no known
	// object. status must call it "missing", never "packed", and must
	// name recover as the next step.
	code, out = runCmd(t, "--repo="+repo, "status")
	if code != 0 {
		t.Fatalf("status (partial): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "  missing  ") {
		t.Fatalf("status output %q does not call the unfed disc missing", out)
	}
	if strings.Contains(out, "  packed  ") {
		t.Fatalf("status output %q calls an unfed disc packed", out)
	}
	if !strings.Contains(out, "next: load disc ") || !strings.Contains(out, "noahsark recover --source=") {
		t.Fatalf("status output %q does not send the operator to recover", out)
	}

	// Feed the remaining two discs, one call each: after the second call
	// every disc named in DISCS has itself been fed, and the rebuild must
	// say ok.
	if code, out := recoverDisc(t, repo, src, discRoots[0]); code != 1 {
		t.Fatalf("recover (disc 0): exit %d, want 1: %s", code, out)
	}
	code, out = recoverDisc(t, repo, src, discRoots[1])
	if code != 0 {
		t.Fatalf("recover (remaining two): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "recover: ok") {
		t.Fatalf("output %q does not say ok once every disc is fed", out)
	}
}

// TestRecoverNoUsableDisc checks that a counted mount that holds no disc
// exits 1 and creates no repository.
func TestRecoverNoUsableDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	empty := filepath.Join(work, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := recoverDisc(t, repo, work, empty)
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "cannot read the disc") || strings.Contains(out, nextStatusLine) {
		t.Fatalf("output %q, want the read failure and no next line", out)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("the repository directory exists after a refused recover: %v", err)
	}
}

// newObjectsFromCommit parses a commit's "new items: N, existing
// items: M" line and returns N.
func newObjectsFromCommit(t *testing.T, output string) int {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		var newObjects, existingObjects int
		if _, err := fmt.Sscanf(line, "new items: %d, existing items: %d", &newObjects, &existingObjects); err == nil {
			return newObjects
		}
	}
	t.Fatalf("no \"new items\" line in commit output: %q", output)
	return -1
}

// TestCommitAfterRebuildCatalogReportsNoNewObjects packs a commit, rebuilds
// the repository from that disc alone, then commits the same source
// again: every object the disc already carries must count as existing,
// not new, even though recover never restored the staging bytes
// for them.
func TestCommitAfterRebuildCatalogReportsNoNewObjects(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	// Every commit stamps a fresh snapshot object with the current
	// time and this build chains no parent, so two commits of unchanged
	// content only produce byte-identical objects, snapshot included,
	// when both run under the same fixed clock.
	fixed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	setFakeNow(t, func() time.Time { return fixed })

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := recoverDisc(t, repo, src, treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	if got := newObjectsFromCommit(t, out); got != 0 {
		t.Fatalf("new objects = %d, want 0: %s", got, out)
	}
}

// discListUUIDCount returns how many discs the ledger reports.
func discListUUIDCount(t *testing.T, repo string) int {
	t.Helper()
	return len(statusDiscs(t, repo))
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	var discRoots []string
	capacities := []string{packSectors(6_000_000), packSectors(6_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--out="+treeDir); code != 0 && i != len(capacities)-1 {
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
				lastCode, lastOut = recoverDisc(t, repo, src, discRoots[i])
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
			if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
				t.Fatalf("re-commit: exit %d: %s", code, out)
			}
			fourthDir := filepath.Join(t.TempDir(), "disc3")
			if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+packSectors(10_000_000), "--out="+fourthDir); code != 0 {
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	baseSrc := writeRefsCarryFixture(t, "base")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=BASE", baseSrc); code != 0 {
		t.Fatalf("commit BASE: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack BASE: exit %d: %s", code, out)
	}

	xSrc := writeRefsCarryFixture(t, "x")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=X", xSrc); code != 0 {
		t.Fatalf("commit X: exit %d: %s", code, out)
	}

	// The pack --out tree of a disc of this repository is not a counted
	// mount. A copy of it stands for the disc.
	disc := filepath.Join(work, "disc")
	copyTree(t, treeDir, disc)
	if code, out := recoverDisc(t, repo, baseSrc, disc); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	if _, err := resolveRef(testLayout(t, repo).refsFile(), "X"); err != nil {
		t.Fatalf("resolveRef(X) after recover: %v, want the unpacked ref to survive", err)
	}

	secondTree := filepath.Join(work, "tree2")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+secondTree); code != 0 {
		t.Fatalf("pack X: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "log")
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "X") {
		t.Fatalf("log output does not mention ref X: %s", out)
	}
}

// TestRecoverKeepsANewerCommittedRef packs two discs that carry the ref
// R, rebuilds the repository from the first disc, and commits again: R
// moves to a new snapshot. Then recover reads the second disc, which the
// repository does not know. Its record of R is older than the commit,
// thus R still names the new snapshot.
func TestRecoverKeepsANewerCommittedRef(t *testing.T) {
	clock := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	setFakeNow(t, func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	})
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	firstSrc := writeRefsCarryFixture(t, "first")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=R", firstSrc); code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	disc0 := filepath.Join(work, "disc0")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+disc0); code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, out)
	}
	secondSrc := writeRefsCarryFixture(t, "second")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=R", secondSrc); code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	disc1 := filepath.Join(work, "disc1")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+disc1); code != 0 {
		t.Fatalf("pack 2: exit %d: %s", code, out)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := recoverDisc(t, repo, firstSrc, disc0); code != 0 {
		t.Fatalf("recover disc 0: exit %d: %s", code, out)
	}
	thirdSrc := writeRefsCarryFixture(t, "third")
	code, out := runCmd(t, "--repo="+repo, "commit", "--ref=R", thirdSrc)
	if code != 0 {
		t.Fatalf("commit 3: exit %d: %s", code, out)
	}
	want := snapshotIDFromCommit(t, out)

	if code, out := recoverDisc(t, repo, firstSrc, disc1); code != 0 {
		t.Fatalf("recover disc 1: exit %d: %s", code, out)
	}
	if id, err := resolveRef(testLayout(t, repo).refsFile(), "R"); err != nil || id.TextForm() != want {
		t.Fatalf("resolveRef(R) = %v, %v after recover, want the committed snapshot %s", id, err, want)
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	lost := filepath.Join(work, "repo.lost")
	if err := os.Rename(repo, lost); err != nil {
		t.Fatal(err)
	}

	if code, out := recoverDisc(t, repo, src, treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	// Now commit against the old, renamed directory: it must stage
	// under repo.lost/staging, the directory that moved with it, not
	// under the freshly rebuilt repo's own staging directory.
	src2 := writeFixtureSource(t)
	if code, out := runCmd(t, "--repo="+lost, "commit", src2); code != 0 {
		t.Fatalf("--repo=%s commit: exit %d: %s", lost, code, out)
	}
	if files := listFilesUnder(t, testLayout(t, lost).chunksDir()); len(files) == 0 {
		t.Fatalf("%s holds no chunk file; the commit staged somewhere else", lost)
	}

	if files := listFilesUnder(t, testLayout(t, repo).chunksDir()); len(files) != 0 {
		t.Fatalf("the rebuilt repository's own staging holds %d chunk file(s); the commit against --repo=%s leaked into it", len(files), lost)
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", "--ref=one", src1)
	if code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	snap1 := snapshotIDFromCommit(t, out)
	discOne := filepath.Join(work, "discs", "disc-one")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+discOne); code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, out)
	}

	src2 := writeFixtureSource(t)
	if err := os.WriteFile(filepath.Join(src2, "two.txt"), []byte("content of the second source"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "commit", "--ref=two", src2)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	snap2 := snapshotIDFromCommit(t, out)
	discTwoLost := filepath.Join(work, "discs", "disc-two-lost")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+discTwoLost); code != 0 {
		t.Fatalf("pack 2: exit %d: %s", code, out)
	}

	// Lose the repository and the second disc; only the first survives.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := recoverDisc(t, repo, src1, discOne); code != 0 {
		t.Fatalf("recover (disc one only): exit %d: %s", code, out)
	}

	src3 := writeFixtureSource(t)
	if err := os.WriteFile(filepath.Join(src3, "three.txt"), []byte("content of the third source"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "commit", "--ref=three", src3)
	if code != 0 {
		t.Fatalf("commit 3: exit %d: %s", code, out)
	}
	snap3 := snapshotIDFromCommit(t, out)
	discThree := filepath.Join(work, "discs", "disc-three")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+discThree); code != 0 {
		t.Fatalf("pack 3: exit %d: %s", code, out)
	}

	// The "lost" second disc turns up after all. Its numbers are the
	// third disc's numbers; the feed must still be accepted.
	code, out = recoverDisc(t, repo, src1, discTwoLost)
	if code != 0 {
		t.Fatalf("recover (reintroduced disc two): exit %d, want 0: %s", code, out)
	}
	discs := discListRows(t, repo)
	if len(discs) != 3 {
		t.Fatalf("status reports %d disc(s), want 3: %v", len(discs), discs)
	}

	shared := discs[byLabel(t, discs, "two disc 1")].Seq
	if other := discs[byLabel(t, discs, "three disc 1")].Seq; other != shared {
		t.Fatalf("disc two seq %d and disc three seq %d differ; the test needs the shared number", shared, other)
	}

	code, out = runCmd(t, "--repo="+repo, "disc", "burned", fmt.Sprint(shared))
	if code != 2 {
		t.Fatalf("disc burned %d: exit %d, want 2: %s", shared, code, out)
	}
	if !strings.Contains(out, "matches more than one disc") {
		t.Fatalf("disc burned %d output %q does not refuse the shared number as ambiguous", shared, out)
	}

	// Discs one and two came back from their own discs: they are on disc
	// only, so disc burned refuses them. Disc three was packed here, and
	// disc burned records its burn.
	// The pack --out tree of disc three is not a counted mount. A copy
	// of it stands for the disc.
	discThreeCopy := filepath.Join(work, "discs", "disc-three-copy")
	copyTree(t, discThree, discThreeCopy)
	roots := map[string]string{"one disc 0": discOne, "two disc 1": discTwoLost, "three disc 1": discThreeCopy}
	for _, root := range roots {
		addFakeMount(t, root, true)
	}
	for _, d := range discs {
		code, out := runCmd(t, "--repo="+repo, "disc", "burned", d.UUID[:8])
		if d.Label == "three disc 1" {
			if code != 0 {
				t.Fatalf("disc burned %s: exit %d: %s", d.UUID[:8], code, out)
			}
			continue
		}
		if code != 1 || !strings.Contains(out, "is already verified") {
			t.Fatalf("disc burned %s (%s): exit %d, want 1 and the already-verified refusal: %s", d.UUID[:8], d.Label, code, out)
		}
		if d.Info.State != stage.DiscOnDiscOnly || d.Items == 0 {
			t.Fatalf("disc %s (%s): state %s with %d item(s), want on disc only with items", d.UUID, d.Label, d.Info.State, d.Items)
		}
	}

	// A verify of disc three moves disc three, and only disc three.
	verified := discs[byLabel(t, discs, "three disc 1")]
	if code, out := runCmd(t, "--repo="+repo, "verify", roots["three disc 1"]); code != 0 {
		t.Fatalf("verify disc three: exit %d: %s", code, out)
	}
	for _, d := range discListRows(t, repo) {
		want := stage.DiscOnDiscOnly
		if d.UUID == verified.UUID {
			want = stage.DiscVerified
		}
		if d.Info.State != want {
			t.Fatalf("disc %s (%s) is %s after the verify of disc three, want %s", d.UUID, d.Label, d.Info.State, want)
		}
	}
	for _, d := range discs {
		if d.UUID == verified.UUID {
			continue
		}
		if code, out := runCmd(t, "--repo="+repo, "verify", roots[d.Label]); code != 0 {
			t.Fatalf("verify disc %s: exit %d: %s", d.Label, code, out)
		}
	}

	for i, pair := range []struct {
		snap string
		src  string
	}{{snap1, src1}, {snap2, src2}, {snap3, src3}} {
		outDir := filepath.Join(work, fmt.Sprintf("restored-%d", i))
		code, out := restoreFromDiscs(t, repo, pair.snap, outDir, discOne, discTwoLost, discThree)
		if code != 0 {
			t.Fatalf("restore %s: exit %d: %s", pair.snap, code, out)
		}
		compareTrees(t, outDir, pair.src)
	}
}

// discListRows returns repo's disc summaries, the same rows "status"
// prints from.
func discListRows(t *testing.T, repo string) []discSummary {
	t.Helper()
	return statusDiscs(t, repo)
}

// byLabel returns the index of the one row with this label.
func byLabel(t *testing.T, rows []discSummary, label string) int {
	t.Helper()
	for i, r := range rows {
		if r.Label == label {
			return i
		}
	}
	t.Fatalf("no disc labelled %q in %v", label, rows)
	return 0
}

// TestRecoverRepeatTwoDiscFeedIsAccepted recovers two discs of the
// repository, one call each, two times. Each call exits 0 and says that
// it knows the disc.
func TestRecoverRepeatTwoDiscFeedIsAccepted(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=BASE", src); code != 0 {
		t.Fatalf("commit BASE: exit %d: %s", code, out)
	}
	discsDir := filepath.Join(work, "discs")
	tree1 := filepath.Join(discsDir, "tree1")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+tree1); code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, out)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := recoverDisc(t, repo, src, tree1); code != 0 {
		t.Fatalf("recover (disc 1 alone): exit %d: %s", code, out)
	}

	if err := os.WriteFile(filepath.Join(src, "c.txt"), []byte("content of c, added for NEXT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=NEXT", src); code != 0 {
		t.Fatalf("commit NEXT: exit %d: %s", code, out)
	}
	tree2 := filepath.Join(discsDir, "tree2")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+tree2); code != 0 {
		t.Fatalf("pack 2: exit %d: %s", code, out)
	}

	// The pack --out tree of disc 2 is not a counted mount. A copy of it
	// stands for the disc.
	disc2 := filepath.Join(discsDir, "disc2")
	copyTree(t, tree2, disc2)
	for round := range 2 {
		for _, tree := range []string{tree1, disc2} {
			code, out := recoverDisc(t, repo, src, tree)
			if code != 0 {
				t.Fatalf("recover %s, round %d: exit %d: %s", tree, round, code, out)
			}
			if !strings.Contains(out, "recover: ok; disc ") || !strings.Contains(out, " already known\n") {
				t.Fatalf("recover %s, round %d: output %q does not say that it knows the disc", tree, round, out)
			}
		}
	}
}

// TestRebuildCatalogFailsFastWhenRepoLockHeld checks that recover
// takes the repository's exclusive lock: recover writes the state
// log and the disc and ref ledgers, so it must not run alongside
// another state-writing command, or another recover.
func TestRebuildCatalogFailsFastWhenRepoLockHeld(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	held, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	disc := filepath.Join(work, "disc")
	copyTree(t, treeDir, disc)
	code, out := recoverDisc(t, repo, src, disc)
	if code != 1 {
		t.Fatalf("recover while locked: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("recover while locked output %q, want it to name the repository lock", out)
	}
}

// TestRecoverUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// recover: each case exits 2, never 0 or 1.
func TestRecoverUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	disc := t.TempDir()
	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--repo=" + repo, "recover", "--no-such-flag"}},
		{"no --disc", []string{"--repo=" + repo, "recover", "--source=" + disc}},
		{"no --source", []string{"--repo=" + repo, "recover", "--disc=" + disc}},
		{"empty --source", []string{"--repo=" + repo, "recover", "--source=", "--disc=" + disc}},
		{"--disc two times", []string{"--repo=" + repo, "recover", "--source=" + disc, "--disc=" + disc, "--disc=" + disc}},
		{"a positional argument", []string{"--repo=" + repo, "recover", "--source=" + disc, "--disc=" + disc, disc}},
		{"the old form", []string{"--repo=" + repo, "recover", disc}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runCmd(t, c.args...)
			if code != 2 {
				t.Fatalf("args %v: exit %d, want 2: %s", c.args, code, out)
			}
		})
	}
}
