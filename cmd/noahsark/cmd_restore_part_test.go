package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
)

// writeCrossDiscFixtureSource writes one file far larger than the
// capacity a disc of this fixture gets, so its own chunks have to land
// on two discs, plus two small files in another directory.
func writeCrossDiscFixtureSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	big := filepath.Join(src, "big")
	small := filepath.Join(src, "small")
	for _, dir := range []string{big, small} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 12<<20)
	if _, err := rng.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(big, "cross.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		part := make([]byte, 200_000)
		if _, err := rng.Read(part); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(small, fmt.Sprintf("s%d.bin", i)), part, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// largestStagedObject returns the size of the largest object file
// staging holds. A forced capacity must be above it, or pack refuses
// the run, so the fixture below sizes its discs from the chunks commit
// actually cut.
func largestStagedObject(t *testing.T, repo string) uint64 {
	t.Helper()
	var largest uint64
	err := filepath.WalkDir(filepath.Join(repo, "staging", "objects"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if uint64(info.Size()) > largest {
			largest = uint64(info.Size())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if largest == 0 {
		t.Fatal("staging holds no object")
	}
	return largest
}

// crossDiscFixture commits writeCrossDiscFixtureSource's tree and packs
// it across discs small enough to cut the big file in two. It returns
// the repository, the snapshot id, the committed source directory, the
// disc roots in disc_seq order, and the big file's path below the
// source.
func crossDiscFixture(t *testing.T) (repo, snapID, src string, discRoots []string, crossRel string) {
	t.Helper()
	work := t.TempDir()
	repo = filepath.Join(work, "repo")
	src = writeCrossDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)

	capacity := packSectors(largestStagedObject(t, repo) + (2 << 20))
	for i := range 8 {
		treeDir := filepath.Join(work, fmt.Sprintf("disc%d", i))
		code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+capacity, "--out="+treeDir)
		if code != 0 {
			// A run's own catalog and its prerequisite rows grow with each
			// disc. pack names the capacity that holds them; take it and
			// pack this disc again.
			var least uint64
			if _, err := fmt.Sscanf(lineWith(out, "use a capacity of"), "noahsark: pack: use a capacity of %d bytes or more", &least); err != nil {
				t.Fatalf("pack %d: exit %d: %s", i, code, out)
			}
			capacity = packSectors(least)
			if err := os.RemoveAll(treeDir); err != nil {
				t.Fatal(err)
			}
			code, out = runCmd(t, "pack", "--repo="+repo, "--capacity="+capacity, "--out="+treeDir)
		}
		if code != 0 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
		if strings.Contains(out, "remaining staged: 0 objects") {
			break
		}
	}
	if len(discRoots) < 2 {
		t.Fatalf("the fixture packed %d disc(s), want at least 2", len(discRoots))
	}

	crossRel = filepath.Join("big", "cross.bin")
	include := strings.TrimPrefix(filepath.Join(src, "big"), "/")
	if seqs := chunkDiscSeqs(t, repo, snapID, include); len(seqs) < 2 {
		t.Fatalf("%s has its chunks on %d disc(s), want at least 2", crossRel, len(seqs))
	}
	return repo, snapID, src, discRoots, crossRel
}

// planDiscSeqs returns the disc_seq of every disc the plan needs, in
// the order a restore reads them. restore --dry-run prints the same
// discs by number instead, for the operator to look up on a sleeve.
func planDiscSeqs(t *testing.T, repo, snapID string) []int {
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
	result, err := plan.Build(c, snap, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int
	for _, d := range result.Discs {
		seqs = append(seqs, int(d.DiscSeq))
	}
	// restore asks for the discs by number, the order it prints them in.
	slices.Sort(seqs)
	return seqs
}

// lineWith returns the first line of out that holds want, or "".
func lineWith(out, want string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// compareFiles checks that got holds exactly want's bytes.
func compareFiles(t *testing.T, got, want string) {
	t.Helper()
	gotBytes, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("%s does not match %s", got, want)
	}
}

// partFilesUnder returns every part file below dir, by path.
func partFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), ".noahsark-part") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// foundDiscSeqs returns the disc_seq of every disc a restore reported as
// found, in the order the restore detected them.
func foundDiscSeqs(t *testing.T, out string) []int {
	t.Helper()
	var seqs []int
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "disc ") || !strings.HasSuffix(line, ": found") {
			continue
		}
		var seq int
		if _, err := fmt.Sscanf(line, "disc %d", &seq); err != nil {
			t.Fatalf("parse found line %q: %v", line, err)
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

// swapSteps returns one scripted step for each disc after the first: at
// the prompt, the operator mounts the next disc the plan wants.
func swapSteps(t *testing.T, mountDir string, discRoots []string, seqs []int) []func() {
	t.Helper()
	var steps []func()
	for _, seq := range seqs[1:] {
		root := discRoots[seq]
		steps = append(steps, func() { mountDisc(t, mountDir, root) })
	}
	return steps
}

// TestRestoreDiscSwapCrossDiscFile restores a file whose chunks lie on
// two discs. Each disc is inserted one time, the file matches the
// source byte for byte, and no part file is left behind.
func TestRestoreDiscSwapCrossDiscFile(t *testing.T) {
	repo, snapID, src, discRoots, crossRel := crossDiscFixture(t)
	seqs := planDiscSeqs(t, repo, snapID)

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: swapSteps(t, mountDir, discRoots, seqs)})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if got := foundDiscSeqs(t, out); !slices.Equal(got, seqs) {
		t.Fatalf("restore detected disc_seq %v, want each plan disc one time: %v", got, seqs)
	}
	if n := strings.Count(out, "insert disc"); n != len(seqs)-1 {
		t.Fatalf("restore prompted %d time(s), want %d: %s", n, len(seqs)-1, out)
	}
	compareFiles(t, filepath.Join(outDir, src, crossRel), filepath.Join(src, crossRel))
	compareTrees(t, filepath.Join(outDir, src), src)
	if left := partFilesUnder(t, outDir); len(left) > 0 {
		t.Fatalf("part file(s) left after a successful restore: %v", left)
	}
}

// TestRestoreDiscSwapCrossDiscResume kills a restore after the first
// disc. The cross-disc file must not carry its final name, and the
// rerun must complete it and ask only for the discs it still needs.
func TestRestoreDiscSwapCrossDiscResume(t *testing.T) {
	repo, snapID, src, discRoots, crossRel := crossDiscFixture(t)
	seqs := planDiscSeqs(t, repo, snapID)

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	outDir := filepath.Join(t.TempDir(), "out")

	// stdin closes with no line at all: the session is killed while it
	// waits for the second disc.
	setRestoreStdin(t, &scriptedStdin{})
	if code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir); code == 0 {
		t.Fatalf("restore (interrupted): exit 0, want non-zero: %s", out)
	}
	cross := filepath.Join(outDir, src, crossRel)
	if _, err := os.Stat(cross); !os.IsNotExist(err) {
		t.Fatalf("%s carries its final name after the first disc alone: %v", cross, err)
	}
	if left := partFilesUnder(t, outDir); len(left) == 0 {
		t.Fatal("the interrupted restore left no part file to continue from")
	}

	setRestoreStdin(t, &scriptedStdin{steps: swapSteps(t, mountDir, discRoots, seqs)})
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore (resumed): exit %d, want 0: %s", code, out)
	}
	if got := foundDiscSeqs(t, out); !slices.Equal(got, seqs[1:]) {
		t.Fatalf("the rerun detected disc_seq %v, want only the discs it still needs: %v", got, seqs[1:])
	}
	compareTrees(t, filepath.Join(outDir, src), src)
	if left := partFilesUnder(t, outDir); len(left) > 0 {
		t.Fatalf("part file(s) left after a successful restore: %v", left)
	}
}

// TestRestoreDiscSwapNoOverwriteAtTheLinkStep plants a file at the
// final name of the cross-disc file while the restore waits for the
// second disc. The restore must not replace it, and must report it.
func TestRestoreDiscSwapNoOverwriteAtTheLinkStep(t *testing.T) {
	repo, snapID, src, discRoots, crossRel := crossDiscFixture(t)
	seqs := planDiscSeqs(t, repo, snapID)

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	outDir := filepath.Join(t.TempDir(), "out")
	cross := filepath.Join(outDir, src, crossRel)
	const planted = "written by somebody else\n"

	steps := swapSteps(t, mountDir, discRoots, seqs)
	swapToSecond := steps[0]
	steps[0] = func() {
		swapToSecond()
		if err := os.WriteFile(cross, []byte(planted), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setRestoreStdin(t, &scriptedStdin{steps: steps})

	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 1 {
		t.Fatalf("restore: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "pass --overwrite to replace it") {
		t.Fatalf("restore output %q does not report the path it left alone", out)
	}
	got, err := os.ReadFile(cross)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != planted {
		t.Fatalf("%s was replaced, want the planted content left alone", cross)
	}
	if left := partFilesUnder(t, outDir); len(left) > 0 {
		t.Fatalf("part file(s) left for a path the restore gave up on: %v", left)
	}
}

// TestRestoreDiscSwapOverwriteReplacesAFileAndRefusesADirectory checks
// --overwrite at the link step: a file in the way is replaced, and a
// directory that holds entries is not removed.
func TestRestoreDiscSwapOverwriteReplacesAFileAndRefusesADirectory(t *testing.T) {
	repo, snapID, src, discRoots, crossRel := crossDiscFixture(t)
	seqs := planDiscSeqs(t, repo, snapID)

	outDir := filepath.Join(t.TempDir(), "out")
	cross := filepath.Join(outDir, src, crossRel)
	blocked := filepath.Join(outDir, src, "small", "s0.bin")
	if err := os.MkdirAll(filepath.Dir(cross), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cross, []byte("old content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: swapSteps(t, mountDir, discRoots, seqs)})

	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "--overwrite", snapID, outDir)
	if code != 1 {
		t.Fatalf("restore --overwrite: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "a directory that is not empty is in the way") {
		t.Fatalf("restore --overwrite output %q does not report the directory it refused", out)
	}
	compareFiles(t, cross, filepath.Join(src, crossRel))
	if fi, err := os.Lstat(blocked); err != nil || !fi.IsDir() {
		t.Fatalf("%s is no longer the directory that was in the way: %v", blocked, err)
	}
}

// TestRestoreDiscSwapPartPathSymlinkIsNotFollowed plants a symlink at
// the part file's own path. The restore must not write through it.
func TestRestoreDiscSwapPartPathSymlinkIsNotFollowed(t *testing.T) {
	repo, snapID, src, discRoots, crossRel := crossDiscFixture(t)
	seqs := planDiscSeqs(t, repo, snapID)

	outDir := filepath.Join(t.TempDir(), "out")
	cross := filepath.Join(outDir, src, crossRel)
	if err := os.MkdirAll(filepath.Dir(cross), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.bin")
	const keep = "not the restore's file\n"
	if err := os.WriteFile(outside, []byte(keep), 0o644); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(filepath.Dir(cross), "."+filepath.Base(cross)+".noahsark-part")
	if err := os.Symlink(outside, part); err != nil {
		t.Fatal(err)
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: swapSteps(t, mountDir, discRoots, seqs)})

	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 1 {
		t.Fatalf("restore: exit %d, want 1: %s", code, out)
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != keep {
		t.Fatalf("the restore wrote through the planted symlink into %s", outside)
	}
	if _, err := os.Stat(cross); !os.IsNotExist(err) {
		t.Fatalf("%s was restored through a symlinked part path: %v", cross, err)
	}
}

// TestRestoreDiscSwapPartNameCollision restores a snapshot that holds a
// file named like the part file of its neighbour. Both files must come
// back exactly.
func TestRestoreDiscSwapPartNameCollision(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.bin"), []byte("the real file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".f.bin.noahsark-part"), []byte("a name the restore also wants\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)
	discRoot := filepath.Join(work, "disc0")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+discRoot); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoot)
	setRestoreStdin(t, &scriptedStdin{})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out = runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
	if left := partFilesUnder(t, filepath.Join(outDir, src, "..")); len(left) != 1 {
		t.Fatalf("part-like file(s) below the output: %v, want the snapshot's own one alone", left)
	}
}
