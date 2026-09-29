package main

import (
	crand "crypto/rand"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/repolock"
)

// TestPackPopulatesCatalog runs init, commit and pack, then checks pack
// left a catalog behind that ls and plan could use with no disc
// present: the run's catalog, the snapshot object, at least one tree,
// and a completeness record.
func TestPackPopulatesCatalog(t *testing.T) {
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

	if _, err := c.Refs(); err != nil {
		t.Fatalf("Refs: %v", err)
	}
	discs, err := c.Discs()
	if err != nil {
		t.Fatalf("Discs: %v", err)
	}
	if len(discs.Rows) != 1 {
		t.Fatalf("catalog DISCS row count = %d, want 1", len(discs.Rows))
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

var dryRunTotalRe = regexp.MustCompile(`total: (\d+) disc\(s\), (\d+) object\(s\) on the discs, (\d+) bytes`)

// TestPackDryRunWritesNothing checks that --dry-run leaves the repository
// exactly as a plain "commit" left it: no packed tree, no state record,
// no catalog entry, no ledger row, and the staging total unchanged.
func TestPackDryRunWritesNothing(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	beforeCode, beforeOut := runCmd(t, "--repo="+repo, "status")
	if beforeCode != 0 {
		t.Fatalf("status: exit %d: %s", beforeCode, beforeOut)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--dry-run")
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
	if !strings.Contains(out, "next: run noahsark pack 1 time(s)") {
		t.Fatalf("pack --dry-run output %q should end with the one action for the operator", out)
	}

	// No ledger file, no run tree, and the same staged total.
	layout := testLayout(t, repo)
	if _, err := os.Stat(layout.discsLedgerFile()); !os.IsNotExist(err) {
		t.Fatalf("pack --dry-run wrote a disc ledger: %v", err)
	}
	entries, err := os.ReadDir(layout.plansDir())
	if err == nil && len(entries) != 0 {
		t.Fatalf("pack --dry-run wrote a plan tree under staging/plans: %v", entries)
	}

	afterCode, afterOut := runCmd(t, "--repo="+repo, "status")
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=6MiB", "--dry-run")
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
		code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=6MiB", "--out="+treeDir)
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	lk, code, ok := lockRepo("test", repo, os.Stderr)
	if !ok {
		t.Fatalf("could not take the repository lock to set up the test: exit %d", code)
	}
	defer releaseLock(lk)

	dryCode, dryOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--dry-run")
	if dryCode != 0 {
		t.Fatalf("pack --dry-run under a held lock: exit %d: %s", dryCode, dryOut)
	}

	// A real pack, in contrast, must fail fast while the lock is held.
	realCode, realOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if realCode == 0 {
		t.Fatalf("real pack under a held lock unexpectedly succeeded: %s", realOut)
	}
}

// TestPackWithNothingStagedSucceeds packs a repository in full, then
// packs the same ref again with nothing left staged. The second pack
// must write no run, say why, and exit 0: nothing to do is success.
func TestPackWithNothingStagedSucceeds(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	firstTree := filepath.Join(work, "tree1")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+firstTree); code != 0 {
		t.Fatalf("first pack: exit %d: %s", code, out)
	}

	secondTree := filepath.Join(work, "tree2")
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+secondTree)
	if code != 0 {
		t.Fatalf("second pack: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "nothing to pack") {
		t.Fatalf("second pack: output %q missing \"nothing to pack\"", out)
	}
	if entries, err := os.ReadDir(secondTree); err == nil && len(entries) != 0 {
		t.Fatalf("second pack: %s is not empty, a run was written despite the refusal", secondTree)
	}
}

// TestPackAfterGCSaysNothingToPackNotNeverCommitted packs, burns and
// verifies a disc, then runs gc with retention forced to zero so every
// chunk file of staging is deleted. A pack run
// after that still has a ref naming the old snapshot, so it must say
// "nothing to pack", the same as an ordinary already-packed
// repository, not "no snapshot has been committed".
func TestPackAfterGCSaysNothingToPackNotNeverCommitted(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	packAndVerifyDisc(t, work, repo, src)

	setFakeStdin(t, strings.NewReader("y\n"))
	if code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=0d"); code != 0 {
		t.Fatalf("gc: exit %d, want 0: %s", code, out)
	}
	if files := listFilesUnder(t, testLayout(t, repo).chunksDir()); len(files) != 0 {
		t.Fatalf("staging still holds %d chunk files after gc; test fixture did not empty it", len(files))
	}

	secondTree := filepath.Join(work, "tree2")
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+secondTree)
	if code != 0 {
		t.Fatalf("pack after gc: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "nothing to pack") {
		t.Fatalf("pack after gc: output %q missing \"nothing to pack\"", out)
	}
	if strings.Contains(out, "no snapshot has been committed") {
		t.Fatalf("pack after gc: output %q wrongly claims no snapshot was ever committed", out)
	}
}

// TestPackRefusesNonEmptyOutput packs once into treeDir, then packs new
// staged content into the same --out. The second pack must refuse
// instead of adding another run alongside the first, or rewriting
// DISC.bin and README.txt.
func TestPackRefusesNonEmptyOutput(t *testing.T) {
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
		t.Fatalf("first pack: exit %d: %s", code, out)
	}

	// Stage something new to pack, so the refusal below is really about
	// --out and not about there being nothing left to place.
	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}

	before, err := os.ReadFile(filepath.Join(treeDir, "NOAHSARK", "DISC.bin"))
	if err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir)
	if code != 2 {
		t.Fatalf("second pack into the same --out: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, treeDir) {
		t.Fatalf("second pack: output %q does not name the offending directory", out)
	}

	after, err := os.ReadFile(filepath.Join(treeDir, "NOAHSARK", "DISC.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("DISC.bin changed after the refused pack")
	}
	runsDir := filepath.Join(treeDir, "NOAHSARK", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("runs directory has %d entries after the refused pack, want 1", len(entries))
	}
}

// TestPackDefaultOutputPathsDoNotCollide runs two packs with no --out on
// the same repository. Each pack's own disc uuid must make the default
// path unique, so the second pack never lands in the first pack's tree.
func TestPackDefaultOutputPathsDoNotCollide(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out1 := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("first pack: exit %d: %s", code, out1)
	}
	firstPath := packedIntoPath(t, out1)

	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	code, out2 := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("second pack: exit %d: %s", code, out2)
	}
	secondPath := packedIntoPath(t, out2)

	if firstPath == secondPath {
		t.Fatalf("both packs used the same default --out: %s", firstPath)
	}
	if !strings.HasPrefix(firstPath, testLayout(t, repo).plansDir()) {
		t.Fatalf("default --out %q is not under staging/plans", firstPath)
	}
}

// TestPackDefaultOutputFollowsStagingDir checks that pack's default
// --out is built from the staging.dir config key, not a hardcoded
// "<repo>/staging" path, so a repository whose staging store was moved
// still packs into it.
func TestPackDefaultOutputFollowsStagingDir(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	stagingDir := filepath.Join(work, "elsewhere-staging")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	oldStaging := testLayout(t, repo).stagingDir()
	cfgPath := configPath(repo)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.ReplaceAll(string(data), "  dir: staging\n", "  dir: "+stagingDir+"\n")
	if edited == string(data) {
		t.Fatalf("config %q has no staging.dir key to replace: %q", cfgPath, data)
	}
	if err := os.WriteFile(cfgPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldStaging, stagingDir); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	path := packedIntoPath(t, out)
	if !strings.HasPrefix(path, testLayout(t, repo).plansDir()) || testLayout(t, repo).stagingDir() != stagingDir {
		t.Fatalf("default --out %q is not under the moved staging.dir %q", path, stagingDir)
	}
}

// packedIntoPath picks the directory out of pack's "tree: PATH" line.
func packedIntoPath(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if after, found := strings.CutPrefix(line, "tree: "); found {
			return after
		}
	}
	t.Fatalf("no \"tree: PATH\" line in pack output: %q", output)
	return ""
}

// TestPackCapacityTooSmallMessage packs with a capacity too small to
// hold even the run's own fixed files. The message must name the given
// value, the parsed byte and sector counts, and the minimum sector and
// byte counts this run actually needs, rather than the internal "does
// not fit after all" wording or a generic list of presets and suffixes.
func TestPackCapacityTooSmallMessage(t *testing.T) {
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
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=50KiB", "--out="+treeDir)
	if code != 2 {
		t.Fatalf("pack: exit %d, want 2: %s", code, out)
	}
	if strings.Contains(out, "internal error") {
		t.Fatalf("pack: output %q leaked the internal-error wording", out)
	}
	for _, want := range []string{"51200", "holds not one object", "the smallest staged object is", "or more"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pack: output %q missing %q", out, want)
		}
	}
}

// TestPackNextStepsBlock checks that a successful pack prints the
// image-build, burn and verify commands, and that the default burn line
// carries no -dvd-compat and no spare:none, since the disc stays open
// unless --close is given.
func TestPackNextStepsBlock(t *testing.T) {
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
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir)
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "next steps:") {
		t.Fatalf("pack output %q missing the next-steps block", out)
	}
	if !strings.Contains(out, "sudo noahsark image build") || !strings.Contains(out, treeDir) {
		t.Fatalf("pack output %q missing the image build command", out)
	}
	if !strings.Contains(out, "growisofs") {
		t.Fatalf("pack output %q missing the growisofs burn line", out)
	}
	if strings.Contains(out, "-dvd-compat") {
		t.Fatalf("pack output %q carries -dvd-compat without --close", out)
	}
	if strings.Contains(out, "spare:none") {
		t.Fatalf("pack output %q carries spare:none without --close", out)
	}
	if !strings.Contains(out, "spare:min") {
		t.Fatalf("pack output %q missing the default spare:min", out)
	}
	if !strings.Contains(out, " verify <MOUNT>") {
		t.Fatalf("pack output %q missing the verify command", out)
	}
}

// TestPackCloseFlagSealsBurnLine checks that --close switches the
// printed burn line to -dvd-compat and spare:none, the closing variant.
func TestPackCloseFlagSealsBurnLine(t *testing.T) {
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
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir, "--close")
	if code != 0 {
		t.Fatalf("pack --close: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "-dvd-compat") {
		t.Fatalf("pack --close output %q missing -dvd-compat", out)
	}
	if !strings.Contains(out, "spare:none") {
		t.Fatalf("pack --close output %q missing spare:none", out)
	}
}

// TestPackOnANewRepositorySucceeds checks that pack before the first
// commit writes no run, says why, and exits 0.
func TestPackOnANewRepositorySucceeds(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "nothing to pack") || !strings.Contains(out, "no snapshot has been committed") {
		t.Fatalf("pack output %q, want the nothing-to-pack reason", out)
	}
}

// TestPackCarriesEveryPendingRef commits three refs, then packs once.
// The single run must carry all three: pack copies every local ref
// record with run_seq 0 into the run it packs; it never narrows which
// refs a run carries.
func TestPackCarriesEveryPendingRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	srcByRef := make(map[string]string)
	for _, name := range []string{"A", "B", "C"} {
		src := writeRefsCarryFixture(t, name)
		srcByRef[name] = src
		if code, out := runCmd(t, "--repo="+repo, "commit", "--ref="+name, src); code != 0 {
			t.Fatalf("commit %s: exit %d: %s", name, code, out)
		}
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "log", treeDir)
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, out)
	}
	for _, name := range []string{"A", "B", "C"} {
		if !strings.Contains(out, name) {
			t.Fatalf("log output does not mention ref %s: %s", name, out)
		}
	}

	outDir := filepath.Join(work, "out-b")
	if code, out := runCmd(t, "restore", treeDir, "B", outDir); code != 0 {
		t.Fatalf("restore B: exit %d: %s", code, out)
	}
	restored := filepath.Join(outDir, srcByRef["B"], "a.txt")
	if _, err := os.Stat(restored); err != nil {
		t.Fatalf("restored file for ref B missing: %v", err)
	}
}

// TestPackObjectCountMatchesBurnedAndVerify packs two discs, the second
// carrying run1's snapshot object forward for disc-b's own
// self-description. pack's own object count for disc-b must equal the
// count disc burned marks and the count verify reports for that same
// disc: the carried snapshot object already belongs to disc-a, so it is
// not disc-b's own object, in any of the three commands.
func TestPackObjectCountMatchesBurnedAndVerify(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	src1 := writeRefsCarryFixture(t, "run1")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=run1", src1); code != 0 {
		t.Fatalf("commit run1: exit %d: %s", code, out)
	}
	discA := filepath.Join(work, "disc-a")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+discA); code != 0 {
		t.Fatalf("pack run1: exit %d: %s", code, out)
	}

	src2 := writeRefsCarryFixture(t, "run2")
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=run2", src2); code != 0 {
		t.Fatalf("commit run2: exit %d: %s", code, out)
	}
	discB := filepath.Join(work, "disc-b")
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+discB)
	if code != 0 {
		t.Fatalf("pack run2: exit %d: %s", code, out)
	}
	m := packedDiscRe.FindStringSubmatch(strings.SplitN(out, "\n", 2)[0])
	if m == nil {
		t.Fatalf("pack run2: first line of %q is not a packed-disc line", out)
	}
	packedCount, err := strconv.Atoi(m[3])
	if err != nil {
		t.Fatalf("pack run2: object count %q does not parse: %v", m[3], err)
	}

	// disc-b is the second, and only the second, disc this repository has
	// ever packed, so its disc_seq is 1.
	code, out = runCmd(t, "--repo="+repo, "disc", "burned", "1")
	if code != 0 {
		t.Fatalf("disc burned disc-b: exit %d: %s", code, out)
	}
	if !strings.Contains(out, strconv.Itoa(packedCount)+" object(s) marked") {
		t.Fatalf("disc burned output %q does not mark the %d object(s) pack reported", out, packedCount)
	}

	code, out = runCmd(t, "--repo="+repo, "verify", discB)
	if code != 0 {
		t.Fatalf("verify disc-b: exit %d: %s", code, out)
	}
	if !strings.Contains(out, strconv.Itoa(packedCount)+" object(s) verified") {
		t.Fatalf("verify output %q does not verify the %d object(s) pack reported", out, packedCount)
	}
}

// TestPackWithoutCapacityRefused asserts that pack refuses to run when
// --capacity is not given: the config carries no capacity default, so
// every pack must give its own.
func TestPackWithoutCapacityRefused(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--out="+filepath.Join(work, "tree"))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	if !strings.Contains(out, "pack needs --capacity") {
		t.Fatalf("output = %q, want the pack needs --capacity line", out)
	}
}

// TestPackKeepsCrossDiscDedupAfterBurnAndVerify commits and packs one
// disc, burns and verifies it so its objects reach CLEAN, then commits a
// one-line change and packs again. The second pack must hold only the
// new objects: a CLEAN object must never be copied again or rebound to
// the new disc, and the first disc's object count in "status" must not
// change.
func TestPackKeepsCrossDiscDedupAfterBurnAndVerify(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	packAndVerifyDisc(t, work, repo, src)

	discs := statusDiscs(t, repo)
	if len(discs) != 1 {
		t.Fatalf("status names %d disc(s) after the first pack, want 1", len(discs))
	}
	firstObjects := discs[0].OnDiscObjects

	// Commit a one-line change: most objects (the unchanged file, every
	// tree above it) are unchanged content, already CLEAN.
	if err := os.WriteFile(filepath.Join(src, "new.txt"), []byte("one new line"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("second pack: exit %d: %s", code, packOut)
	}

	discs = statusDiscs(t, repo)
	if len(discs) != 2 {
		t.Fatalf("status names %d disc(s) after the second pack, want 2", len(discs))
	}
	if discs[0].OnDiscObjects != firstObjects {
		t.Fatalf("first disc objects = %d after the second pack, want unchanged %d: a CLEAN object was rebound", discs[0].OnDiscObjects, firstObjects)
	}
	if discs[1].OnDiscObjects == 0 {
		t.Fatal("second disc objects = 0, want nonzero for the new file's objects")
	}
}

// TestPackWithNoRefCarriesEveryPendingDateRef commits twice, each with
// its own --ref=DATE naming a real label. pack takes every pending ref;
// it must carry both.
func TestPackWithNoRefCarriesEveryPendingDateRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-21", src); code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-22", src); code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack with no --ref: exit %d: %s", code, out)
	}
	if strings.Contains(out, "not found") {
		t.Fatalf("pack output %q, must not look for a ref that was never created", out)
	}
}

// dryRunDiscRe matches one predicted disc line of pack --dry-run.
var dryRunDiscRe = regexp.MustCompile(`^disc (\d+) "([^"]*)": (\d+) object\(s\) on the disc, (\d+) bytes$`)

// packedDiscRe matches the first line of a real pack.
var packedDiscRe = regexp.MustCompile(`^packed disc (\d+) "([^"]*)": (\d+) object\(s\) on the disc, (\d+) bytes$`)

// TestPackDefaultLabelUsesTheNewestPendingRef checks that a pack with
// two pending refs labels the disc with the newest one, and that a
// later disc, which carries no pending ref at all, still carries a ref
// name instead of a bare disc number.
func TestPackDefaultLabelUsesTheNewestPendingRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	// Two commits in the same second: the tie must go to the later
	// date, not to the name that sorts first.
	writeSized(t, filepath.Join(src, "one.bin"), 400_000, 1)
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-14"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	writeSized(t, filepath.Join(src, "two.bin"), 400_000, 2)
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-21"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=6MB", "--out="+filepath.Join(work, "d0"))
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if !strings.Contains(out, `packed disc 0 "2026-09-21 disc 0"`) {
		t.Fatalf("pack output %q, want the newest pending ref in the label", out)
	}

	// A second disc: both refs are carried already, so nothing is
	// pending. The label still names the newest ref of the repository.
	writeSized(t, filepath.Join(src, "three.bin"), 400_000, 3)
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-21"); code != 0 {
		t.Fatalf("commit again: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=6MB", "--out="+filepath.Join(work, "d1")); code != 0 {
		t.Fatalf("pack again: exit %d: %s", code, out)
	} else if !strings.Contains(out, `packed disc 1 "2026-09-21 disc 1"`) {
		t.Fatalf("pack output %q, want a later disc to keep a ref name in its label", out)
	}

	if code, out := runCmd(t, "pack", "-h"); code != 0 && !strings.Contains(out, "newest ref name and the disc number") {
		t.Fatalf("pack -h output %q, want the help to name the same rule", out)
	}
}

// TestPackDryRunPredictsTheRealPacks checks that pack --dry-run and a
// loop of real packs agree exactly: the same disc numbers, labels,
// object counts and byte counts, at a capacity that needs several
// discs.
func TestPackDryRunPredictsTheRealPacks(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		writeSized(t, filepath.Join(src, fmt.Sprintf("f%d.bin", i)), 900_000, byte(i+1))
	}
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=r1"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=7MB", "--dry-run")
	if code != 0 {
		t.Fatalf("pack --dry-run: exit %d: %s", code, out)
	}
	var predicted []string
	for line := range strings.SplitSeq(out, "\n") {
		if dryRunDiscRe.MatchString(line) {
			predicted = append(predicted, line)
		}
	}
	if len(predicted) < 2 {
		t.Fatalf("pack --dry-run predicted %d disc(s), want at least 2: %s", len(predicted), out)
	}

	for i := range predicted {
		code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=7MB", "--out="+filepath.Join(work, fmt.Sprintf("tree%d", i)))
		if code != 0 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		m := packedDiscRe.FindStringSubmatch(strings.SplitN(out, "\n", 2)[0])
		if m == nil {
			t.Fatalf("pack %d: first line of %q is not a packed-disc line", i, out)
		}
		got := fmt.Sprintf("disc %s %q: %s object(s) on the disc, %s bytes", m[1], m[2], m[3], m[4])
		if got != predicted[i] {
			t.Fatalf("real pack wrote %q, pack --dry-run predicted %q", got, predicted[i])
		}
	}
	code, out = runCmd(t, "--repo="+repo, "pack", "--capacity=7MB", "--out="+filepath.Join(work, "extra"))
	if code != 0 {
		t.Fatalf("pack after the predicted discs: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "nothing to pack") {
		t.Fatalf("pack after the predicted discs: output %q, want the nothing-to-pack line", out)
	}
}

// writeSized writes a file of n bytes whose content depends on seed, so
// two files of the same size never dedup against each other.
func writeSized(t *testing.T, path string, n int, seed byte) {
	t.Helper()
	buf := make([]byte, n)
	x := uint32(seed)*2654435761 + 1
	for i := range buf {
		x = x*1664525 + 1013904223
		buf[i] = byte(x >> 24)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCapacityRefusesABareNumber checks that a capacity without a unit
// is a usage error, and that the message lists the presets and shows a
// size example. A bare number used to mean sectors, a factor of 2048
// away from the byte count an operator reads it as.
func TestCapacityRefusesABareNumber(t *testing.T) {
	repo, _ := initAndCommit(t)

	for _, arg := range []string{"--capacity=7500000"} {
		args := []string{"--repo=" + repo, "pack", arg}
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Fatalf("pack %s: exit %d, want 2: %s", arg, code, out)
		}
		for _, want := range []string{"has no unit", "bd25", "dvd+r", "25GB"} {
			if !strings.Contains(out, want) {
				t.Fatalf("pack %s: output %q missing %q", arg, out, want)
			}
		}
	}
}

// TestPackPartialPackLeavesTheRestStaged checks the promise the guide
// makes: a capacity smaller than the staged data packs what fits and
// reports what is left, rather than refusing the whole pack.
func TestPackPartialPackLeavesTheRestStaged(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.bin", "two.bin", "three.bin"} {
		b := make([]byte, 2<<20)
		if _, err := crand.Read(b); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=10MB", "--out="+filepath.Join(t.TempDir(), "tree"))
	if code != 0 {
		t.Fatalf("partial pack: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "packed disc 0 ") {
		t.Fatalf("partial pack: output %q has no packed-disc line", out)
	}
	if strings.Contains(out, "remaining staged: 0 objects") {
		t.Fatalf("partial pack: output %q packed everything; the fixture must not fit one disc", out)
	}
}

// TestPackTooSmallNamesTheSmallestObject checks that a capacity holding
// nothing at all names the smallest staged object and its size, not the
// size of all the staged data.
func TestPackTooSmallNamesTheSmallestObject(t *testing.T) {
	repo, _ := initAndCommit(t)
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=50KiB", "--out="+filepath.Join(t.TempDir(), "tree"))
	if code != 2 {
		t.Fatalf("pack: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "the smallest staged object is") {
		t.Fatalf("pack: output %q does not name the smallest object", out)
	}
	if !strings.Contains(out, "or more") {
		t.Fatalf("pack: output %q does not say which capacity would work", out)
	}
}

// TestPackNamesADamagedStagedObject checks the one damaged-staged-object
// text: it names the object id, says the staged copy is damaged, and
// gives the cure. A raw decoder message such as "buffer too short" tells
// the operator nothing to do.
func TestPackNamesADamagedStagedObject(t *testing.T) {
	repo, _ := initAndCommit(t)
	damaged := truncateOneStagedTree(t, repo)
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+filepath.Join(t.TempDir(), "tree"))
	if code == 0 {
		t.Fatalf("pack over a damaged staged object: exit 0, want a failure: %s", out)
	}
	if strings.Contains(out, "buffer too short") {
		t.Fatalf("pack output %q leaks a decoder message", out)
	}
	for _, want := range []string{damaged, "is damaged", "commit again"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pack output %q missing %q", out, want)
		}
	}
}

// truncateOneStagedTree cuts the root tree object file of the one
// snapshot of the catalog of repo to a few bytes, and returns its id
// text.
func truncateOneStagedTree(t *testing.T, repo string) string {
	t.Helper()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := c.ListSnapshots()
	if err != nil || len(ids) != 1 {
		t.Fatalf("catalog snapshots = %v, %v; want one", ids, err)
	}
	snap, err := c.ReadSnapshot(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	root := object.ID(snap.RootTree)
	if err := os.Truncate(c.MetaPath(format.ObjectKindTree, root), 4); err != nil {
		t.Fatal(err)
	}
	return root.TextForm()
}

// TestPackDefaultLabelNamesTheRefAndTheDisc checks the default label: a
// pack with no --label takes the newest ref name and the disc number.
func TestPackDefaultLabelNamesTheRefAndTheDisc(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-21", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if !strings.Contains(out, `packed disc 0 "2026-09-21 disc 0"`) {
		t.Fatalf("pack output %q does not carry the default label", out)
	}
}

// TestPackFailsFastWhenRepoLockHeld checks the same rule for pack: two
// state-writing commands must never replay the state log into their
// own stale snapshot at the same time.
func TestPackFailsFastWhenRepoLockHeld(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	held, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 1 {
		t.Fatalf("pack while locked: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("pack while locked output %q, want it to name the repository lock", out)
	}
}

// TestPackUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// pack: each case exits 2, never 0 or 1.
func TestPackUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"missing --capacity", []string{"--repo=" + repo, "pack"}},
		{"unknown flag", []string{"--repo=" + repo, "pack", "--capacity=64MiB", "--no-such-flag"}},
		{"capacity without a unit", []string{"--repo=" + repo, "pack", "--capacity=7500000"}},
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
