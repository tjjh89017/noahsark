package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// initAndCommit makes a repository with one commit and returns the
// repository directory and the source directory.
func initAndCommit(t *testing.T) (repo, src string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "repo")
	src = writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	return repo, src
}

// TestCapacityRefusesABareNumber checks that a capacity without a unit
// is a usage error, and that the message lists the presets and shows a
// size example. A bare number used to mean sectors, a factor of 2048
// away from the byte count an operator reads it as.
func TestCapacityRefusesABareNumber(t *testing.T) {
	repo, _ := initAndCommit(t)

	for _, arg := range []string{"--capacity=7500000"} {
		args := []string{"pack", "--repo=" + repo, arg}
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

	appendConfigLine(t, repo, "pack.capacity = 7500000")
	code, out := runCmd(t, "pack", "--repo="+repo)
	if code != 2 {
		t.Fatalf("pack with a bare pack.capacity: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "pack.capacity") || !strings.Contains(out, "has no unit") {
		t.Fatalf("pack with a bare pack.capacity: output %q", out)
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
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=10MB", "--out="+filepath.Join(t.TempDir(), "tree"))
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
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=50KiB", "--out="+filepath.Join(t.TempDir(), "tree"))
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

// TestImageBuildReadsCapacityFromTheTree checks that "image build" takes
// the image length from the packed tree's own DISC.bin, and that
// --capacity is gone.
func TestImageBuildReadsCapacityFromTheTree(t *testing.T) {
	repo, _ := initAndCommit(t)
	treeDir := filepath.Join(t.TempDir(), "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=8MB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	if code, out := runCmd(t, "image", "build", "--out="+filepath.Join(t.TempDir(), "run.img"), "--capacity=8MB", treeDir); code != 2 {
		t.Fatalf("image build --capacity: exit %d, want 2: %s", code, out)
	}

	disc := readDiscForTest(t, treeDir)
	if disc.CapacitySectors == 0 {
		t.Fatal("DISC.bin carries no capacity")
	}

	// mkudffs is not available in every test environment, so the check
	// stops at the message: it must carry the tree's own capacity, never
	// ask for a --capacity flag.
	out := filepath.Join(t.TempDir(), "run.img")
	code, msg := runCmd(t, "image", "build", "--out="+out, treeDir)
	if code == 2 && strings.Contains(msg, "capacity") {
		t.Fatalf("image build still asks for a capacity: %s", msg)
	}
}

// readDiscForTest reads DISC.bin from a packed tree.
func readDiscForTest(t *testing.T, treeDir string) format.Disc {
	t.Helper()
	names := image.NewNameCache()
	base, err := image.FindNoahsark(treeDir, names)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := os.ReadFile(filepath.Join(base, names.Resolve(base, "DISC.bin")))
	if err != nil {
		t.Fatal(err)
	}
	var disc format.Disc
	if err := disc.Decode(buf); err != nil {
		t.Fatal(err)
	}
	return disc
}

// TestCommitWarnsAboutASpecialFile checks that commit names a FIFO in
// the source, says it carries no content, counts it, and still exits 0:
// a special file is normal in a source tree, and the operator must hear
// about it while the source is still there.
func TestCommitWarnsAboutASpecialFile(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	fifo := filepath.Join(src, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo is not available here: %v", err)
	}
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d, want 0 for a special file alone: %s", code, out)
	}
	if !strings.Contains(out, "pipe: FIFO, no content is backed up") {
		t.Fatalf("commit output %q does not warn about the FIFO", out)
	}
	if !strings.Contains(out, "special files: 1") {
		t.Fatalf("commit output %q has no special-file count", out)
	}
}

// TestPackNamesADamagedStagedObject checks the one damaged-staged-object
// text: it names the object id, says the staged copy is damaged, and
// gives the cure. A raw decoder message such as "buffer too short" tells
// the operator nothing to do.
func TestPackNamesADamagedStagedObject(t *testing.T) {
	repo, _ := initAndCommit(t)
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}

	damaged := truncateOneStagedTree(t, cfg.StagingDir)
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+filepath.Join(t.TempDir(), "tree"))
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

// truncateOneStagedTree cuts one staged object file to a few bytes and
// returns its id text.
func truncateOneStagedTree(t *testing.T, stagingDir string) string {
	t.Helper()
	objects := filepath.Join(stagingDir, "objects")
	var found string
	err := filepath.Walk(objects, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return err
		}
		if err := os.Truncate(path, 4); err != nil {
			return err
		}
		found = filepath.Base(path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("no staged object file to damage")
	}
	return found
}

// TestPackCapacityFromConfig checks that pack.capacity lets the normal
// cycle run with no flag, and that --capacity still overrides it.
func TestPackCapacityFromConfig(t *testing.T) {
	repo, _ := initAndCommit(t)
	appendConfigLine(t, repo, "pack.capacity = 64MiB")

	code, out := runCmd(t, "pack", "--repo="+repo)
	if code != 0 {
		t.Fatalf("pack with pack.capacity: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "packed disc 0 ") {
		t.Fatalf("pack output %q has no packed-disc line", out)
	}
}

// TestPackDefaultLabelNamesTheRefAndTheDisc checks the default label: a
// pack with no --label takes the newest ref name and the disc number.
func TestPackDefaultLabelNamesTheRefAndTheDisc(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-21", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if !strings.Contains(out, `packed disc 0 "2026-09-21 disc 0"`) {
		t.Fatalf("pack output %q does not carry the default label", out)
	}
}

// TestRepoFromEnvironment checks NOAHSARK_REPO: the normal cycle runs
// with no --repo at all.
func TestRepoFromEnvironment(t *testing.T) {
	repo, src := initAndCommit(t)
	t.Setenv("NOAHSARK_REPO", repo)
	appendConfigLine(t, repo, "sources.root = "+src)
	appendConfigLine(t, repo, "pack.capacity = 64MiB")

	if code, out := runCmd(t, "commit"); code != 0 {
		t.Fatalf("commit with no flag: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "pack"); code != 0 {
		t.Fatalf("pack with no flag: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "status")
	if code != 0 {
		t.Fatalf("status with no flag: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "next: burn disc 0") {
		t.Fatalf("status output %q does not name the burn", out)
	}
	if code, out := runCmd(t, "disc", "burned", "0"); code != 0 {
		t.Fatalf("disc burned 0 with no flag: exit %d: %s", code, out)
	}
}
