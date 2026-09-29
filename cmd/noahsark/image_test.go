package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// TestImageBuildRefusesExistingOutputUnlessForced checks that "image
// build" refuses to overwrite an existing --out path, the same way
// pack refuses an existing --out directory, and that --force lets it
// proceed (here failing later, on the tree directory itself, to prove
// the existence check no longer stood in the way).
func TestImageBuildRefusesExistingOutputUnlessForced(t *testing.T) {
	work := t.TempDir()
	out := filepath.Join(work, "run.img")
	if err := os.WriteFile(out, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	treeDir := filepath.Join(work, "tree")

	code, cmdOut := runCmd(t, "image", "build", "--out="+out, treeDir)
	if code != 2 {
		t.Fatalf("image build (existing --out): exit %d, want 2: %s", code, cmdOut)
	}
	if !strings.Contains(cmdOut, "already exists") || !strings.Contains(cmdOut, "--force") {
		t.Fatalf("image build (existing --out) output %q missing the --force hint", cmdOut)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "already here" {
		t.Fatalf("image build (existing --out) overwrote %s despite refusing", out)
	}

	code, cmdOut = runCmd(t, "image", "build", "--out="+out, "--force", treeDir)
	if code == 2 && strings.Contains(cmdOut, "already exists") {
		t.Fatalf("image build --force still refused an existing --out: %s", cmdOut)
	}
}

// TestImageBuildReadsCapacityFromTheTree checks that "image build" takes
// the image length from the packed tree's own DISC.bin, and that
// --capacity is gone.
func TestImageBuildReadsCapacityFromTheTree(t *testing.T) {
	repo, _ := initAndCommit(t)
	treeDir := filepath.Join(t.TempDir(), "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=8MB", "--out="+treeDir); code != 0 {
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

// TestImageUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// image build: each case exits 2, never 0 or 1.
func TestImageUsageErrorsExitTwo(t *testing.T) {
	work := t.TempDir()

	cases := []struct {
		name string
		args []string
	}{
		{"missing --out", []string{"image", "build", work}},
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
