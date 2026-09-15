package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// TestPackRefusesNothingToPack packs a repository in full, then packs
// the same ref again with nothing left staged. The second pack must
// refuse rather than write an empty run, and must exit 1.
func TestPackRefusesNothingToPack(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	firstTree := filepath.Join(work, "tree1")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+firstTree); code != 0 {
		t.Fatalf("first pack: exit %d: %s", code, out)
	}

	secondTree := filepath.Join(work, "tree2")
	code, out := runCmd(t, "pack", "--repo="+repo, "--out="+secondTree)
	if code != 1 {
		t.Fatalf("second pack: exit %d, want 1: %s", code, out)
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
// staging object, snapshot files included, is deleted. A pack run
// after that still has a LATEST ref naming the old snapshot, so it
// must say "nothing to pack", the same as an ordinary already-packed
// repository, not "no snapshot has been committed".
func TestPackAfterGCSaysNothingToPackNotNeverCommitted(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 0d")

	packAndVerifyDisc(t, work, repo, src)

	if code, out := runCmd(t, "gc", "--repo="+repo); code != 0 {
		t.Fatalf("gc: exit %d, want 0: %s", code, out)
	}
	if entries, err := os.ReadDir(filepath.Join(repo, "staging", "snapshots")); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("staging/snapshots still holds %d entries after gc; test fixture did not empty it", len(entries))
	}

	secondTree := filepath.Join(work, "tree2")
	code, out := runCmd(t, "pack", "--repo="+repo, "--out="+secondTree)
	if code != 1 {
		t.Fatalf("pack after gc: exit %d, want 1: %s", code, out)
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

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir); code != 0 {
		t.Fatalf("first pack: exit %d: %s", code, out)
	}

	// Stage something new to pack, so the refusal below is really about
	// --out and not about there being nothing left to place.
	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}

	before, err := os.ReadFile(filepath.Join(treeDir, "NOAHSARK", "DISC.bin"))
	if err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir)
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

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out1 := runCmd(t, "pack", "--repo="+repo)
	if code != 0 {
		t.Fatalf("first pack: exit %d: %s", code, out1)
	}
	firstPath := packedIntoPath(t, out1)

	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	code, out2 := runCmd(t, "pack", "--repo="+repo)
	if code != 0 {
		t.Fatalf("second pack: exit %d: %s", code, out2)
	}
	secondPath := packedIntoPath(t, out2)

	if firstPath == secondPath {
		t.Fatalf("both packs used the same default --out: %s", firstPath)
	}
	if !strings.Contains(firstPath, filepath.Join(repo, "staging", "plans")) {
		t.Fatalf("default --out %q is not under staging/plans", firstPath)
	}
}

// packedIntoPath picks the directory out of pack's "packed run N on disc
// N into PATH" summary line.
func packedIntoPath(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if _, after, found := strings.Cut(line, " into "); found && strings.HasPrefix(line, "packed run") {
			return after
		}
	}
	t.Fatalf("no \"packed run ... into PATH\" line in pack output: %q", output)
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

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=25", "--out="+treeDir)
	if code != 2 {
		t.Fatalf("pack: exit %d, want 2: %s", code, out)
	}
	if strings.Contains(out, "internal error") {
		t.Fatalf("pack: output %q leaked the internal-error wording", out)
	}
	for _, want := range []string{"25", "51200", "this run needs at least"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pack: output %q missing %q", out, want)
		}
	}
}

// TestPackMediaDerivedFromCapacityPreset checks that a BD --capacity
// preset with no --media records the matching media type, and that a
// DVD --capacity preset with no --media records the matching DVD media
// type, since FORMAT.md's media type registry is informational and
// never refuses a pack on its account.
func TestPackMediaDerivedFromCapacityPreset(t *testing.T) {
	work := t.TempDir()

	bdRepo := filepath.Join(work, "repo-bd")
	bdSrc := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+bdRepo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+bdRepo, bdSrc); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree-bd50")
	if code, out := runCmd(t, "pack", "--repo="+bdRepo, "--capacity=bd50", "--out="+treeDir); code != 0 {
		t.Fatalf("pack --capacity=bd50: exit %d: %s", code, out)
	}
	rr, err := image.Read(treeDir)
	if err != nil {
		t.Fatal(err)
	}
	if rr.Disc.MediaType != format.MediaTypeBDRDL50GB {
		t.Fatalf("DISC.bin media_type = %v, want BD-R-DL-50 (%v)", rr.Disc.MediaType, format.MediaTypeBDRDL50GB)
	}

	dvdRepo := filepath.Join(work, "repo-dvd")
	dvdSrc := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+dvdRepo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+dvdRepo, dvdSrc); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	dvdTree := filepath.Join(work, "tree-dvd")
	if code, out := runCmd(t, "pack", "--repo="+dvdRepo, "--capacity=dvd+r", "--out="+dvdTree); code != 0 {
		t.Fatalf("pack --capacity=dvd+r: exit %d: %s", code, out)
	}
	dvdRR, err := image.Read(dvdTree)
	if err != nil {
		t.Fatal(err)
	}
	if dvdRR.Disc.MediaType != format.MediaTypeDVDPlusRSL {
		t.Fatalf("DISC.bin media_type = %v, want DVD+R-SL (%v)", dvdRR.Disc.MediaType, format.MediaTypeDVDPlusRSL)
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

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir)
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
	if !strings.Contains(out, "noahsark verify") {
		t.Fatalf("pack output %q missing the verify command", out)
	}
}

// TestPackCloseFlagSealsBurnLine checks that --close switches the
// printed burn line to -dvd-compat and spare:none, the closing variant.
func TestPackCloseFlagSealsBurnLine(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir, "--close")
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
