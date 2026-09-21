package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
)

// discSwapFixture packs writeMultiDiscFixtureSource's tree across two
// small forced capacities, into two disc-root trees, and also returns
// the committed source directory so a restore can be checked byte for
// byte.
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

// restoreDryRunDiscSeqs runs "restore --dry-run" with flagsAndSnapshot
// (every element but the last is a flag, the last is SNAPSHOT) and
// returns the disc_seq values it names, in plan order. --mount and
// OUT-DIR are filled with throwaway paths: --dry-run never reads or
// writes either.
func restoreDryRunDiscSeqs(t *testing.T, flagsAndSnapshot ...string) []int {
	t.Helper()
	if len(flagsAndSnapshot) == 0 {
		t.Fatal("restoreDryRunDiscSeqs: no SNAPSHOT given")
	}
	flags := flagsAndSnapshot[:len(flagsAndSnapshot)-1]
	snapID := flagsAndSnapshot[len(flagsAndSnapshot)-1]

	full := append([]string{"restore"}, flags...)
	full = append(full, "--mount="+t.TempDir(), "--dry-run", snapID, filepath.Join(t.TempDir(), "out"))
	code, out := runCmd(t, full...)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	var seqs []int
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "disc ") {
			continue
		}
		var seq int
		if _, err := fmt.Sscanf(line, "disc %d", &seq); err != nil {
			t.Fatalf("parse restore --dry-run line %q: %v", line, err)
		}
		seqs = append(seqs, seq)
	}
	if len(seqs) == 0 {
		t.Fatalf("no disc line in restore --dry-run output %q", out)
	}
	return seqs
}

// chunkDiscSeqs opens repo's cache directly and builds the same plan
// restore --dry-run would, restricted to include, returning the
// disc_seq of every disc that plan assigns at least one chunk object
// to. A disc the plan names only for a tree or blob object never needs
// a physical visit: the assembler resolves those from the cache, so
// this is the set of discs a disc-swap restore of include would
// actually prompt for.
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
	seqs := restoreDryRunDiscSeqs(t, "--repo="+repo, snapID)
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
	if strings.Contains(out, "expected disc") {
		t.Fatalf("restore output %q complained about the disc it had just finished, want no complaint before the first prompt", out)
	}
	if !strings.Contains(out, "insert disc") {
		t.Fatalf("restore output %q missing the prompt for the second disc", out)
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
	seqs := restoreDryRunDiscSeqs(t, "--repo="+repo, snapID)
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
	seqs := restoreDryRunDiscSeqs(t, "--repo="+repo, snapID)
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

	if _, err := os.Stat(filepath.Join(repo, "staging", "restore")); !os.IsNotExist(err) {
		t.Fatalf("restore wrote %s in the repository; it writes only below the output directory: %v", filepath.Join(repo, "staging", "restore"), err)
	}
	if left := partFilesUnder(t, outDir); len(left) > 0 {
		t.Fatalf("part file(s) left after a successful restore: %v", left)
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

// TestRestoreDiscSwapNeverEjects checks that restore never unmounts and
// never ejects: it names the disc it wants and waits. It also checks
// that the disc the loop just finished, still in the drive, draws no
// complaint before the first prompt.
func TestRestoreDiscSwapNeverEjects(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, "--repo="+repo, snapID)

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
	if strings.Contains(out, "umount") || strings.Contains(out, "eject") || strings.Contains(out, "sudo") {
		t.Fatalf("restore output %q mentions umount, eject or sudo", out)
	}
	if strings.Contains(out, "expected disc") {
		t.Fatalf("restore output %q reported a mismatch for the disc the loop just finished, want none before the first prompt", out)
	}
	if !strings.Contains(out, "insert disc") {
		t.Fatalf("restore output %q missing the prompt to swap discs", out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
}

// TestRestoreDiscSwapDiscFromEarlierRunNotNeeded checks that a disc this
// repository already knows, left in the drive from an earlier run, but
// not part of the current restore's plan, draws a calm "not needed"
// line rather than an "expected ... found ..." mismatch, and never that
// line on the first look.
func TestRestoreDiscSwapDiscFromEarlierRunNotNeeded(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
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
	firstSnap := snapshotIDFromCommit(t, out)
	firstDisc := filepath.Join(work, "disc0")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+firstDisc); code != 0 {
		t.Fatalf("pack 0: exit %d: %s", code, out)
	}

	// A second snapshot, packed to its own disc: known to this
	// repository's cache, but not needed to restore the first snapshot.
	// Two packs in one second tie on created_sec; the cache then takes
	// the disc whose DISCS table has more rows, which is this one.
	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	secondDisc := filepath.Join(work, "disc1")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+secondDisc); code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, out)
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, secondDisc) // in the drive, not needed for firstSnap
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, firstDisc) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out = runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, firstSnap, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Contains(out, "expected disc") {
		t.Fatalf("restore output %q reported an expected/found mismatch for a disc that is simply not needed", out)
	}
	if !strings.Contains(out, "is not needed") {
		t.Fatalf("restore output %q, want a calm \"is not needed\" line", out)
	}
}

// TestRestoreDiscSwapStillReportsAGenuineMismatch checks that only the
// disc the loop just finished escapes the mismatch line: a disc of
// another repository is reported the normal way, and the report names
// the disc by its number, its label and its uuid.
func TestRestoreDiscSwapStillReportsAGenuineMismatch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, "--repo="+repo, snapID)
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
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Count(out, "expected disc") != 1 {
		t.Fatalf("restore output %q, want exactly one mismatch report (for the bogus disc, not the disc the loop just finished): %d", out, strings.Count(out, "expected disc"))
	}
	if !mismatchNamesRe.MatchString(out) {
		t.Fatalf("restore output %q, want the mismatch to name both discs as disc N \"LABEL\" (uuid)", out)
	}
	if strings.Contains(out, `""`) {
		t.Fatalf("restore output %q names a disc with an empty label", out)
	}
}

// mismatchNamesRe matches the wrong-disc line, which must name the disc
// it wants and the disc it found in the same complete form.
var mismatchNamesRe = regexp.MustCompile(`expected disc \d+ "[^"]+" \([0-9a-f-]+\), found disc \d+ "[^"]+" \([0-9a-f-]+\)`)

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

// TestDryRunDiscListMatchesTheDiscsRestoreReads checks, for a range of
// --include scopes, that "restore --dry-run"'s printed disc list is
// exactly the discs that hold a needed chunk: the same discs a
// disc-swap restore of that scope actually reads. A disc the plan
// names only because it holds a needed tree or blob object, never read
// from a disc since the assembler resolves those from the cache, would
// otherwise make the dry-run list a disc restore never asks for.
func TestDryRunDiscListMatchesTheDiscsRestoreReads(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, _ := discSwapFixture(t)

	for i := range 6 {
		sub := fmt.Sprintf("sub%d", i)
		include := strings.TrimPrefix(filepath.Join(src, sub), "/")
		planned := restoreDryRunDiscSeqs(t, "--repo="+repo, "--include="+include, snapID)
		want := chunkDiscSeqs(t, repo, snapID, include)
		if !slices.Equal(planned, want) {
			t.Fatalf("--include=%s: plan named disc_seq %v, want exactly the chunk-holding discs %v", include, planned, want)
		}
	}
}

// TestRestoreDiscSwapReadsTheDiscInTheDriveFirst puts the disc the list
// names last into the drive before the restore starts. The restore must
// read it with no prompt and no complaint, and then ask for the other
// one.
func TestRestoreDiscSwapReadsTheDiscInTheDriveFirst(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[1]])
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[0]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Contains(out, "expected disc") {
		t.Fatalf("restore output %q complained about a disc it needed and could read", out)
	}
	if n := strings.Count(out, "insert disc"); n != 1 {
		t.Fatalf("restore prompted %d time(s), want 1: %s", n, out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
}

// TestRestoreDiscSwapRerunListsOnlyTheDiscsStillNeeded interrupts a
// restore after the first disc, then checks that both --dry-run and the
// rerun list only the disc that is still needed.
func TestRestoreDiscSwapRerunListsOnlyTheDiscsStillNeeded(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	outDir := filepath.Join(t.TempDir(), "out")

	setRestoreStdin(t, &scriptedStdin{})
	if code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir); code == 0 {
		t.Fatalf("restore (interrupted): exit 0, want non-zero: %s", out)
	}

	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "--dry-run", snapID, outDir)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "totals: 1 discs") {
		t.Fatalf("restore --dry-run output %q, want only the disc that is still needed", out)
	}
	if listsDisc(out, seqs[0]) {
		t.Fatalf("restore --dry-run output %q still lists the disc it already read", out)
	}

	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})
	code, out = runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore (resumed): exit %d: %s", code, out)
	}
	if listsDisc(out, seqs[0]) {
		t.Fatalf("the rerun output %q still lists the disc it already read", out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
}

// listsDisc reports whether out's printed disc list names disc seq. It
// reads only the list lines, so a wrong-disc report elsewhere in the
// output never counts as a listing.
func listsDisc(out string, seq int) bool {
	prefix := fmt.Sprintf("disc %d ", seq)
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, prefix) && strings.Contains(line, " objects, ") {
			return true
		}
	}
	return false
}
