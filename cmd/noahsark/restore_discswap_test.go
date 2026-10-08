package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)

	capacities := []string{packSectors(7_000_000), packSectors(7_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}
	return repo, snapID, src, discRoots
}

// setScriptedTerminal makes s the standard input of every fake env, and
// makes it a terminal, until the test ends.
func setScriptedTerminal(t *testing.T, s *scriptedStdin) {
	t.Helper()
	setFakeStdin(t, s)
	old := fakeStdinTTY
	fakeStdinTTY = true
	t.Cleanup(func() { fakeStdinTTY = old })
}

// restoreDryRunDiscSeqs runs "restore --dry-run" for snapID and paths,
// and returns the disc_seq values of its disc lines, in the order that
// it prints them. --disc and DEST are throwaway paths: --dry-run reads
// no disc and writes nothing.
func restoreDryRunDiscSeqs(t *testing.T, repo, snapID string, paths ...string) []int {
	t.Helper()
	args := []string{"--repo=" + repo, "restore", "--dry-run", "--disc=" + t.TempDir(), snapID}
	args = append(args, paths...)
	args = append(args, filepath.Join(t.TempDir(), "out"))
	code, out := runCmd(t, args...)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	seqs := planLineSeqs(t, out)
	if len(seqs) == 0 {
		t.Fatalf("no disc line in restore --dry-run output %q", out)
	}
	return seqs
}

// planLineSeqs returns the disc_seq of each disc line of a plan.
func planLineSeqs(t *testing.T, out string) []int {
	t.Helper()
	var seqs []int
	for _, m := range planLineRe.FindAllStringSubmatch(out, -1) {
		var seq int
		if _, err := fmt.Sscanf(m[1], "%d", &seq); err != nil {
			t.Fatalf("parse plan line %q: %v", m[0], err)
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

// planLineRe matches one disc line of a plan and captures its disc_seq.
var planLineRe = regexp.MustCompile(`(?m)^disc (\d+) "[^"]*" \([0-9a-f-]{36}\): \d+ items, \d+ bytes( \(lost\))?$`)

// TestRestoreDiscSwapTwoDiscChain drives the disc-swap loop through a
// two-disc plan with the first disc already mounted and the second
// needing a swap, and checks the result matches the source tree.
func TestRestoreDiscSwapTwoDiscChain(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setScriptedTerminal(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if got := foundDiscSeqs(t, out); !slices.Equal(got, seqs) {
		t.Fatalf("found lines for disc_seq %v, want %v: %s", got, seqs, out)
	}
	if n := strings.Count(out, "insert disc"); n != 1 {
		t.Fatalf("restore prompted %d time(s), want 1: %s", n, out)
	}
	if strings.Contains(out, "umount") || strings.Contains(out, "eject") || strings.Contains(out, "sudo") {
		t.Fatalf("restore output %q mentions umount, eject or sudo", out)
	}
	if strings.Contains(out, "next: ") {
		t.Fatalf("restore output %q holds the next line", out)
	}
	compareTrees(t, outDir, src)
}

// TestRestoreDiscSwapWrongDiscThenRight checks that an operator who
// inserts the wrong disc sees the mismatch named, is asked again, and
// that the restore completes once the right disc is in the drive.
func TestRestoreDiscSwapWrongDiscThenRight(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	// A disc of another repository is in the drive: the restore does not
	// know it.
	bogusWork := t.TempDir()
	bogusRepo := filepath.Join(bogusWork, "repo")
	if code, out := runIn(t, bogusRepo, "init"); code != 0 {
		t.Fatalf("init bogus repo: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+bogusRepo, "commit", writeFixtureSource(t)); code != 0 {
		t.Fatalf("commit bogus repo: exit %d: %s", code, out)
	}
	bogusDisc := filepath.Join(bogusWork, "disc")
	if code, out := runCmd(t, "--repo="+bogusRepo, "pack", "--capacity=64MiB", "--out="+bogusDisc); code != 0 {
		t.Fatalf("pack bogus repo: exit %d: %s", code, out)
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, bogusDisc)
	setScriptedTerminal(t, &scriptedStdin{steps: []func(){
		func() {}, // the operator presses Enter and swaps nothing
		func() { mountDisc(t, mountDir, discRoots[seqs[0]]) },
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if n := len(unknownMismatchRe.FindAllString(out, -1)); n != 2 {
		t.Fatalf("restore output %q, want two mismatch lines that name the unknown disc by uuid, got %d", out, n)
	}
	if !knownMismatchRe.MatchString(out) {
		t.Fatalf("restore output %q, want a mismatch line that names the disc just read", out)
	}
	if n := strings.Count(out, "insert disc"); n != 3 {
		t.Fatalf("restore prompted %d time(s), want 3: %s", n, out)
	}
	compareTrees(t, outDir, src)
}

// unknownMismatchRe matches the wrong-disc line for a disc that the
// repository does not know. knownMismatchRe matches the line for a disc
// that it knows.
var (
	unknownMismatchRe = regexp.MustCompile(`expected disc \d+ "[^"]+" \([0-9a-f-]{36}\), found disc [0-9a-f-]{36}\n`)
	knownMismatchRe   = regexp.MustCompile(`expected disc \d+ "[^"]+" \([0-9a-f-]{36}\), found disc \d+ "[^"]+" \([0-9a-f-]{36}\)\n`)
)

// TestRestoreDiscSwapResume stops a restore at the prompt for the second
// disc, checks the subtree the first disc alone could finish, then
// completes the restore in a second run.
func TestRestoreDiscSwapResume(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	// A subdirectory whose chunk data lives entirely on the disc the
	// plan reads first.
	var doneSub string
	for i := range 6 {
		sub := fmt.Sprintf("sub%d", i)
		if s := chunkDiscSeqs(t, repo, snapID, sub); len(s) == 1 && s[0] == seqs[0] {
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
	before := treeDigest(t, repo)

	// The input ends at the prompt: the session is killed while it waits.
	setScriptedTerminal(t, &scriptedStdin{})
	code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir)
	if code != 1 {
		t.Fatalf("restore (interrupted): exit %d, want 1: %s", code, out)
	}
	wantInsertAgain(t, out, mountDir)
	compareTrees(t, filepath.Join(outDir, doneSub), filepath.Join(src, doneSub))

	setScriptedTerminal(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})
	code, out = runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore (resumed): exit %d, want 0: %s", code, out)
	}
	if !regexp.MustCompile(`(?m)^skipped: \d+ file\(s\) already restored$`).MatchString(out) {
		t.Fatalf("restore (resumed) output %q missing the skipped line", out)
	}
	if strings.Contains(out, "warning:") {
		t.Fatalf("restore (resumed) output %q, want no problem line", out)
	}
	compareTrees(t, outDir, src)
	if left := partFilesUnder(t, outDir); len(left) > 0 {
		t.Fatalf("part file(s) left after a successful restore: %v", left)
	}
	if after := treeDigest(t, repo); after != before {
		t.Fatalf("restore changed the repository:\nbefore %s\nafter  %s", before, after)
	}
}

// wantInsertAgain checks the line of a restore that stops for a disc.
func wantInsertAgain(t *testing.T, out, dir string) {
	t.Helper()
	re := regexp.MustCompile(`(?m)^restore: insert disc \d+ "[^"]+" \([0-9a-f-]{36}\) into ` + regexp.QuoteMeta(dir) + ` and run restore again$`)
	if !re.MatchString(out) {
		t.Fatalf("output %q, want the line that names the disc to insert and to run restore again", out)
	}
}

// treeDigest describes every file below dir by its path, size and
// content, so a test can see that a command changed nothing.
func treeDigest(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	for _, rel := range listFilesUnder(t, dir) {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(&b, "%s %d %x\n", rel, len(data), data)
	}
	return b.String()
}

// TestRestoreDiscSwapNoTerminal is row 87 with two discs: with no
// terminal, restore reads the disc at --disc, prints the disc to insert
// and exits 1. The same restore with the next disc mounted completes.
func TestRestoreDiscSwapNoTerminal(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}
	mountDir := filepath.Join(t.TempDir(), "mount")
	outDir := filepath.Join(t.TempDir(), "out")
	spy := &readSpy{}
	setFakeStdin(t, spy)

	for i, seq := range seqs {
		mountDisc(t, mountDir, discRoots[seq])
		code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir)
		if i < len(seqs)-1 {
			if code != 1 {
				t.Fatalf("restore with disc %d: exit %d, want 1: %s", seq, code, out)
			}
			wantInsertAgain(t, out, mountDir)
			if !strings.Contains(out, fmt.Sprintf("restore: insert disc %d ", seqs[i+1])) {
				t.Fatalf("output %q, want it to ask for disc %d", out, seqs[i+1])
			}
			continue
		}
		if code != 0 {
			t.Fatalf("restore with the last disc: exit %d: %s", code, out)
		}
	}
	if spy.read {
		t.Fatal("restore read standard input with no terminal")
	}
	compareTrees(t, outDir, src)
}

// TestRestoreDiscSwapOnePathNarrowsToOneDisc checks that a PATH whose
// chunk data lives on one disc never prompts, when that disc is already
// in the drive.
func TestRestoreDiscSwapOnePathNarrowsToOneDisc(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := chunkDiscSeqs(t, repo, snapID, "sub0")
	if len(seqs) != 1 {
		t.Fatalf("sub0 needs chunks from %d disc(s), want 1", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setScriptedTerminal(t, &scriptedStdin{})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, "sub0", outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Contains(out, "insert disc") {
		t.Fatalf("restore output %q prompted, want no prompt", out)
	}
	compareTrees(t, filepath.Join(outDir, "sub0"), filepath.Join(src, "sub0"))
}

// TestDryRunDiscListMatchesTheDiscsRestoreReads checks, for each
// subdirectory, that the disc lines of "restore --dry-run" name exactly
// the discs that hold a chunk of it.
func TestDryRunDiscListMatchesTheDiscsRestoreReads(t *testing.T) {
	repo, snapID, _, _ := discSwapFixture(t)

	for i := range 6 {
		sub := fmt.Sprintf("sub%d", i)
		planned := restoreDryRunDiscSeqs(t, repo, snapID, sub)
		want := chunkDiscSeqs(t, repo, snapID, sub)
		if !slices.Equal(planned, want) {
			t.Fatalf("%s: plan named disc_seq %v, want exactly the chunk-holding discs %v", sub, planned, want)
		}
	}
}

// TestRestoreDiscSwapReadsTheDiscInTheDriveFirst puts the disc the plan
// names last into the drive before the restore starts. The restore must
// read it with no prompt, and then ask for the other one.
func TestRestoreDiscSwapReadsTheDiscInTheDriveFirst(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[1]])
	setScriptedTerminal(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[0]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if got := foundDiscSeqs(t, out); !slices.Equal(got, []int{seqs[1], seqs[0]}) {
		t.Fatalf("found lines for disc_seq %v, want disc %d first: %s", got, seqs[1], out)
	}
	if n := strings.Count(out, "insert disc"); n != 1 {
		t.Fatalf("restore prompted %d time(s), want 1: %s", n, out)
	}
	compareTrees(t, outDir, src)
}

// TestRestoreDiscSwapRerunListsOnlyTheDiscsStillNeeded stops a restore
// after the first disc, then checks that both --dry-run and the rerun
// list only the disc that is still needed.
func TestRestoreDiscSwapRerunListsOnlyTheDiscsStillNeeded(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := restoreDryRunDiscSeqs(t, repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	outDir := filepath.Join(t.TempDir(), "out")

	if code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir); code != 1 {
		t.Fatalf("restore (stopped): exit %d, want 1: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, "--dry-run", snapID, outDir)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if got := planLineSeqs(t, out); !slices.Equal(got, seqs[1:]) {
		t.Fatalf("restore --dry-run output %q lists disc_seq %v, want only %v", out, got, seqs[1:])
	}
	if !strings.Contains(out, "totals: 1 discs, ") {
		t.Fatalf("restore --dry-run output %q, want the totals of one disc", out)
	}

	mountDisc(t, mountDir, discRoots[seqs[1]])
	code, out = runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore (resumed): exit %d: %s", code, out)
	}
	if got := planLineSeqs(t, out); !slices.Equal(got, seqs[1:]) {
		t.Fatalf("the rerun plan %q lists disc_seq %v, want only %v", out, got, seqs[1:])
	}
	compareTrees(t, outDir, src)
}
