package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
)

// discSwapFixture packs writeMultiDiscFixtureSource's tree across two
// small forced capacities, into two disc-root trees, the same way
// multiDiscPlanFixture does, and also returns the committed source
// directory so a restore can be checked byte for byte.
func discSwapFixture(t *testing.T) (repo, snapID, src string, discRoots []string) {
	t.Helper()
	work := t.TempDir()
	repo = filepath.Join(work, "repo")
	src = writeMultiDiscFixtureSource(t)

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
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}
	return repo, snapID, src, discRoots
}

// planOrderDiscSeqs runs "plan" with args and returns the disc_seq
// values it names, in plan order.
func planOrderDiscSeqs(t *testing.T, args ...string) []int {
	t.Helper()
	full := append([]string{"plan"}, args...)
	code, out := runCmd(t, full...)
	if code != 0 {
		t.Fatalf("plan: exit %d: %s", code, out)
	}
	var seqs []int
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "disc ") {
			continue
		}
		var seq int
		if _, err := fmt.Sscanf(line, "disc %d", &seq); err != nil {
			t.Fatalf("parse plan line %q: %v", line, err)
		}
		seqs = append(seqs, seq)
	}
	if len(seqs) == 0 {
		t.Fatalf("no disc line in plan output %q", out)
	}
	return seqs
}

// chunkDiscSeqs opens repo's cache directly and builds the same plan
// "plan" would, restricted to include, returning the disc_seq of every
// disc that plan assigns at least one chunk object to. A disc a plan
// names only for a tree or blob object never needs a physical visit:
// BuildManifest resolves those from the cache, so this is the set of
// discs a disc-swap restore of include would actually prompt for.
func chunkDiscSeqs(t *testing.T, repo, snapID, include string) []int {
	t.Helper()
	c, err := cache.Open(repoCacheDir(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	id, err := object.ParseID(snapID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Build(c, snap, id, []string{include})
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int
	for _, d := range result.Discs {
		for _, o := range d.Objects {
			if o.Kind == format.ObjectKindChunk {
				seqs = append(seqs, int(d.DiscSeq))
				break
			}
		}
	}
	return seqs
}

// mountDisc replaces mountDir with a symlink to discRoot, standing in
// for an operator swapping the disc a real drive has mounted there.
func mountDisc(t *testing.T, mountDir, discRoot string) {
	t.Helper()
	if err := os.RemoveAll(mountDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(discRoot, mountDir); err != nil {
		t.Fatal(err)
	}
}

// scriptedStdin feeds one newline per Scan, running a step first so a
// test can swap the mount directory's contents right where a real
// operator would, between the prompt and pressing Enter. Reading past
// the last step reports EOF, the same as a closed terminal.
type scriptedStdin struct {
	steps []func()
	next  int
}

func (s *scriptedStdin) Read(p []byte) (int, error) {
	if s.next >= len(s.steps) {
		return 0, io.EOF
	}
	s.steps[s.next]()
	s.next++
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = '\n'
	return 1, nil
}

// setRestoreStdin installs r as the disc-swap loop's stdin for the
// duration of the test.
func setRestoreStdin(t *testing.T, r io.Reader) {
	t.Helper()
	restoreStdin = r
	t.Cleanup(func() { restoreStdin = os.Stdin })
}

// TestRestoreDiscSwapTwoDiscChain drives the disc-swap loop through a
// two-disc plan with the first disc already correctly mounted and the
// second requiring a swap, and checks the result matches the source
// tree exactly.
func TestRestoreDiscSwapTwoDiscChain(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := planOrderDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if !strings.Contains(out, ": found") {
		t.Fatalf("restore output %q missing a found line for the already-mounted disc", out)
	}
	if !strings.Contains(out, "expected disc") {
		t.Fatalf("restore output %q missing the mismatch report for the second disc", out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
}

// TestRestoreDiscSwapWrongDiscThenRight checks that an operator who
// inserts the wrong disc sees the mismatch reported and is prompted
// again, and that the restore still completes once the right disc is
// in the drive.
func TestRestoreDiscSwapWrongDiscThenRight(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := planOrderDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	// The disc the plan wants second is in the drive for the first
	// disc: an immediate mismatch.
	mountDisc(t, mountDir, discRoots[seqs[1]])
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() {}, // operator presses Enter without swapping yet
		func() { mountDisc(t, mountDir, discRoots[seqs[0]]) }, // now inserts the right one
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) }, // and the second plan disc
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Count(out, "expected disc") < 1 {
		t.Fatalf("restore output %q missing the mismatch report", out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
}

// TestRestoreDiscSwapResume interrupts a restore before the second disc
// is ever inserted, checks the subtree the first disc alone could
// already finish is on disk, then completes the restore in a second run
// and checks the whole tree matches.
func TestRestoreDiscSwapResume(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := planOrderDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	// A subdirectory whose chunk data lives entirely on the disc the
	// plan visits first: after the interrupted run, it is already
	// restored, even though the whole snapshot is not.
	var doneSub string
	for i := range 6 {
		sub := fmt.Sprintf("sub%d", i)
		include := strings.TrimPrefix(filepath.Join(src, sub), "/")
		if s := chunkDiscSeqs(t, repo, snapID, include); len(s) == 1 && s[0] == seqs[0] {
			doneSub = sub
			break
		}
	}
	if doneSub == "" {
		t.Fatalf("no subdirectory of %s has its chunk data on disc_seq=%d alone", src, seqs[0])
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	outDir := filepath.Join(t.TempDir(), "out")

	// stdin closes with no lines at all, standing in for a session
	// killed while waiting for the operator to insert the second disc.
	setRestoreStdin(t, &scriptedStdin{})
	if code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir); code == 0 {
		t.Fatalf("restore (interrupted): exit 0, want non-zero: %s", out)
	}
	compareTrees(t, filepath.Join(outDir, src, doneSub), filepath.Join(src, doneSub))

	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})
	// No --overwrite: the interrupted run already wrote doneSub's files
	// with the right size and mtime, so this rerun must count them
	// resumed, not skipped, and still exit 0.
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore (resumed): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "resumed:") {
		t.Fatalf("restore (resumed) output %q missing the resumed line", out)
	}
	if strings.Contains(out, "skipped") {
		t.Fatalf("restore (resumed) output %q, want no skipped path", out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)

	if _, err := os.Stat(filepath.Join(repo, "staging", "restore", snapID)); !os.IsNotExist(err) {
		t.Fatalf("staging/restore/%s still exists after a successful restore: %v", snapID, err)
	}
}

// TestRestoreDiscSwapIncludeNarrowsToOneDisc checks that --include
// narrowed to a subtree whose chunk data lives on one disc never
// prompts at all, when that disc is already in the drive.
func TestRestoreDiscSwapIncludeNarrowsToOneDisc(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	include := strings.TrimPrefix(filepath.Join(src, "sub0"), "/")

	seqs := chunkDiscSeqs(t, repo, snapID, include)
	if len(seqs) != 1 {
		t.Fatalf("--include=%s needs chunks from %d disc(s), want 1", include, len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "--include="+include, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Contains(out, "insert disc") {
		t.Fatalf("restore output %q prompted, want no prompt", out)
	}
	compareTrees(t, filepath.Join(outDir, src, "sub0"), filepath.Join(src, "sub0"))
}

// TestRestoreDiscSwapNoEject checks that --no-eject skips the eject
// step: with it, a mount directory that is not a real mount point never
// runs umount or eject, so no warning about either appears.
func TestRestoreDiscSwapNoEject(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := planOrderDiscSeqs(t, "--repo="+repo, snapID)

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "--no-eject", snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Contains(out, "umount") || strings.Contains(out, "eject") {
		t.Fatalf("restore --no-eject output %q mentions eject", out)
	}
	if strings.Contains(out, "expected disc") {
		t.Fatalf("restore --no-eject output %q reported a mismatch for the disc the loop just finished, want none before the first prompt", out)
	}
	if !strings.Contains(out, "insert disc") {
		t.Fatalf("restore --no-eject output %q missing the prompt to swap discs", out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
}

// TestRestoreDiscSwapNoEjectStillReportsAGenuineMismatch checks that
// --no-eject only suppresses the mismatch line for the disc the loop
// just finished: once the operator has been prompted once, and a
// second, different disc is inserted, that is reported the normal way.
func TestRestoreDiscSwapNoEjectStillReportsAGenuineMismatch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, discRoots := discSwapFixture(t)
	seqs := planOrderDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	// A disc from an unrelated repository: neither disc the plan wants.
	bogusWork := t.TempDir()
	bogusRepo := filepath.Join(bogusWork, "repo")
	if code, out := runCmd(t, "init", "--repo="+bogusRepo); code != 0 {
		t.Fatalf("init bogus repo: exit %d: %s", code, out)
	}
	bogusSrc := writeFixtureSource(t)
	if code, out := runCmd(t, "commit", "--repo="+bogusRepo, bogusSrc); code != 0 {
		t.Fatalf("commit bogus repo: exit %d: %s", code, out)
	}
	bogusDisc := filepath.Join(bogusWork, "disc")
	if code, out := runCmd(t, "pack", "--repo="+bogusRepo, "--capacity=64MiB", "--out="+bogusDisc); code != 0 {
		t.Fatalf("pack bogus repo: exit %d: %s", code, out)
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, bogusDisc) },          // wrong disc, not the previous one
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) }, // now the right one
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "--no-eject", snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Count(out, "expected disc") != 1 {
		t.Fatalf("restore --no-eject output %q, want exactly one mismatch report (for the bogus disc, not the disc the loop just finished): %d", out, strings.Count(out, "expected disc"))
	}
}

// TestRestoreMountUnknownRefNamesProvidedDiscs checks that restore
// --mount with a ref name the cache does not know reports the ref as
// not on the provided disc(s), not as a name unknown outright: --mount
// implies discs are being fed in, so a later one may still carry it.
func TestRestoreMountUnknownRefNamesProvidedDiscs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, _, _, _ := discSwapFixture(t)

	mountDir := filepath.Join(t.TempDir(), "mount")
	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "no-such-ref", outDir)
	if code != 2 {
		t.Fatalf("restore --mount unknown ref: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "not on the provided disc(s)") {
		t.Fatalf("restore --mount unknown ref output = %q, want the provided-disc(s) wording", out)
	}
	if strings.Contains(out, "neither a snapshot id nor a known ref name") {
		t.Fatalf("restore --mount unknown ref output = %q, want the disc-oriented wording, not the cache one", out)
	}
}

// TestRestoreSnapshotIDPrefixNamesItself checks that a SNAPSHOT
// argument that is 8 or more hex characters, and matches no ref, is
// reported as a likely truncated snapshot id instead of being treated
// as an ordinary unknown ref name.
func TestRestoreSnapshotIDPrefixNamesItself(t *testing.T) {
	repo, _, _, discRoots := discSwapFixture(t)

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, discRoots[0], "1220a053", outDir)
	if code != 2 {
		t.Fatalf("restore with a snapshot id prefix: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "looks like a snapshot id prefix") {
		t.Fatalf("restore with a snapshot id prefix output %q missing the prefix hint", out)
	}
	if strings.Contains(out, "is not on the provided disc(s)") {
		t.Fatalf("restore with a snapshot id prefix output %q, want the prefix wording, not the ref-not-found one", out)
	}
}

// TestPlanDiscListMatchesTheDiscsRestoreReads checks, for a range of
// --include scopes, that "plan"'s printed disc list is exactly the
// discs that hold a needed chunk: the same discs a disc-swap restore
// of that scope actually reads. A disc plan names only because it
// holds a needed tree or blob object, never read from a disc since
// BuildManifest resolves those from the cache, would otherwise make
// plan list a disc restore never asks for.
func TestPlanDiscListMatchesTheDiscsRestoreReads(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, _ := discSwapFixture(t)

	for i := range 6 {
		sub := fmt.Sprintf("sub%d", i)
		include := strings.TrimPrefix(filepath.Join(src, sub), "/")
		planned := planOrderDiscSeqs(t, "--repo="+repo, "--include="+include, snapID)
		want := chunkDiscSeqs(t, repo, snapID, include)
		if !slices.Equal(planned, want) {
			t.Fatalf("--include=%s: plan named disc_seq %v, want exactly the chunk-holding discs %v", include, planned, want)
		}
	}
}
