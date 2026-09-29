package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// runImageBuildAs runs image build in a fake env with the user id uid.
func runImageBuildAs(t *testing.T, uid int, args ...string) (int, string, string) {
	t.Helper()
	te := newTestEnv(t.TempDir())
	te.euid = func() int { return uid }
	code, _ := te.run(args...)
	return code, te.out.String(), te.errOut.String()
}

// TestImageBuildUsageErrorsExitTwo checks the usage errors of image
// build: each exits 2.
func TestImageBuildUsageErrorsExitTwo(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	for _, args := range [][]string{
		{"image", "build"},
		{"image", "build", "0", "0"},
		{"image", "build", "--out=" + filepath.Join(fx.work, "x.img"), "0"},
		{"image", "build", "7"},
	} {
		code, _, stderr := runImageBuildAs(t, 0, append([]string{"--repo=" + fx.repo}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", args, code, stderr)
		}
	}
}

// TestImageBuildWritesOnlyTheImage checks that image build writes the
// image file of the disc and no other file, takes no lock, and passes
// the capacity of DISC.bin.
func TestImageBuildWritesOnlyTheImage(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	layout := testLayout(t, fx.repo)
	if err := os.Remove(layout.lockFile()); err != nil {
		t.Fatal(err)
	}
	before := listFilesUnder(t, fx.repo)

	var gotTree string
	var gotSectors uint64
	old := imageHost.makeImage
	imageHost.makeImage = func(treeDir, imagePath string, sectors uint64, prog *progress.Reporter) error {
		gotTree, gotSectors = treeDir, sectors
		return fakeMakeImage(treeDir, imagePath, sectors, prog)
	}
	t.Cleanup(func() { imageHost.makeImage = old })

	code, stdout, stderr := runImageBuildAs(t, 0, "--repo="+fx.repo, "image", "build", "0")
	if code != 0 {
		t.Fatalf("image build: exit %d: %s", code, stderr)
	}
	img := layout.planImage(fx.uuidBytes(t))
	if want := "built image " + img + " (67108864 bytes)\n"; stdout != want {
		t.Errorf("stdout %q, want %q", stdout, want)
	}
	if gotTree != layout.planTree(fx.uuidBytes(t)) || gotSectors != 32768 {
		t.Errorf("makeImage got tree %s and %d sectors", gotTree, gotSectors)
	}

	after := listFilesUnder(t, fx.repo)
	rel, err := filepath.Rel(fx.repo, img)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(before)
	want = append(want, rel)
	slices.Sort(want)
	if !slices.Equal(after, want) {
		t.Errorf("files after image build:\n%v\nwant:\n%v", after, want)
	}
	if _, err := os.Stat(layout.lockFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("image build created the lock file: %v", err)
	}
}

// TestImageBuildIgnoresTheLock checks that image build runs while
// another command holds the repository lock.
func TestImageBuildIgnoresTheLock(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	lk, err := repolock.Acquire(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lk.Release() })
	if code, _, stderr := runImageBuildAs(t, 0, "--repo="+fx.repo, "image", "build", "0"); code != 0 {
		t.Fatalf("image build under a held lock: exit %d: %s", code, stderr)
	}
}

// TestImageBuildOfAPackOutDisc checks that the image of a pack --out disc
// goes to the plan directory, not into the pack --out directory.
func TestImageBuildOfAPackOutDisc(t *testing.T) {
	repo, _ := initAndCommit(t)
	out := filepath.Join(t.TempDir(), "tree")
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+out)
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	code, stdout, stderr := runImageBuildAs(t, 0, "--repo="+repo, "image", "build", "0")
	if code != 0 {
		t.Fatalf("image build: exit %d: %s", code, stderr)
	}
	uuid := packedDiscUUID(t, packOut)
	img := filepath.Join(testLayout(t, repo).plansDir(), uuid, planImageName)
	if !strings.Contains(stdout, "built image "+img+" ") {
		t.Errorf("stdout %q does not name %s", stdout, img)
	}
	if _, err := os.Stat(img); err != nil {
		t.Errorf("no image at %s: %v", img, err)
	}
	if files := listFilesUnder(t, out); slices.Contains(files, planImageName) {
		t.Errorf("image build wrote into the pack --out directory: %v", files)
	}
}

// TestImageBuildForceReplacesTheImage checks that --force removes the
// old image and builds a new one.
func TestImageBuildForceReplacesTheImage(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	img := testLayout(t, fx.repo).planImage(fx.uuidBytes(t))
	if err := os.WriteFile(img, []byte("old image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runImageBuildAs(t, 0, "--repo="+fx.repo, "image", "build", "--force", "0"); code != 0 {
		t.Fatalf("image build --force: exit %d: %s", code, stderr)
	}
	info, err := os.Stat(img)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 67108864 {
		t.Errorf("image size %d, want 67108864", info.Size())
	}
}

// TestImageBuildChecksMkudffsAfterTheState checks the order of the
// checks: the state, then the mkudffs version, then root.
func TestImageBuildChecksMkudffsAfterTheState(t *testing.T) {
	old := imageHost.mkudffsVersion
	imageHost.mkudffsVersion = func() (string, error) {
		return "udftools 2.2", errors.New("mkudffs is from udftools 2.2; image build needs udftools 2.3 or newer")
	}
	t.Cleanup(func() { imageHost.mkudffsVersion = old })

	fx := repoWithDisc(t, stage.DiscPacked)
	code, _, stderr := runImageBuildAs(t, 1000, "--repo="+fx.repo, "image", "build", "0")
	if code != 1 || !strings.Contains(stderr, "image build needs udftools 2.3 or newer") || strings.Contains(stderr, "needs root") {
		t.Errorf("old mkudffs, not root: exit %d: %s", code, stderr)
	}

	lost := repoWithDisc(t, stage.DiscLost)
	code, _, stderr = runImageBuildAs(t, 1000, "--repo="+lost.repo, "image", "build", "0")
	if code != 1 || !strings.Contains(stderr, "no disc root at ") || strings.Contains(stderr, "udftools") {
		t.Errorf("lost disc, old mkudffs: exit %d: %s", code, stderr)
	}
}

// TestImageBuildSudoLineKeepsForce checks that the printed sudo line
// repeats --force.
func TestImageBuildSudoLineKeepsForce(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	code, _, stderr := runImageBuildAs(t, 1000, "--repo="+fx.repo, "image", "build", "--force", "0")
	want := "noahsark: image build needs root for the loop mount; run: sudo noahsark --repo=" + fx.repo + " image build --force 0\n"
	if code != 1 || stderr != want {
		t.Errorf("exit %d, stderr %q, want %q", code, stderr, want)
	}
}
