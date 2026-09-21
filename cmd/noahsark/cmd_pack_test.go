package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackWithNothingStagedSucceeds packs a repository in full, then
// packs the same ref again with nothing left staged. The second pack
// must write no run, say why, and exit 0: nothing to do is success.
func TestPackWithNothingStagedSucceeds(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	firstTree := filepath.Join(work, "tree1")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+firstTree); code != 0 {
		t.Fatalf("first pack: exit %d: %s", code, out)
	}

	secondTree := filepath.Join(work, "tree2")
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+secondTree)
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
// staging object, snapshot files included, is deleted. A pack run
// after that still has a LATEST ref naming the old snapshot, so it
// must say "nothing to pack", the same as an ordinary already-packed
// repository, not "no snapshot has been committed".
func TestPackAfterGCSaysNothingToPackNotNeverCommitted(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
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
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+secondTree)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
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

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out1 := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
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
	code, out2 := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
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

// TestPackDefaultOutputFollowsStagingDir checks that pack's default
// --out is built from the staging.dir config key, not a hardcoded
// "<repo>/staging" path, so a repository whose staging store was moved
// still packs into it.
func TestPackDefaultOutputFollowsStagingDir(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	stagingDir := filepath.Join(work, "elsewhere-staging")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	cfgPath := filepath.Join(repo, "config")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.ReplaceAll(string(data), "staging.dir = staging\n", "staging.dir = "+stagingDir+"\n")
	if edited == string(data) {
		t.Fatalf("config %q has no staging.dir line to replace: %q", cfgPath, data)
	}
	if err := os.WriteFile(cfgPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, "staging"), stagingDir); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	path := packedIntoPath(t, out)
	if !strings.HasPrefix(path, filepath.Join(stagingDir, "plans")) {
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=50KiB", "--out="+treeDir)
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

// TestPackRefusesCapacityAbovePhysical packs with --capacity above
// --physical-capacity. The disc is write-once, so the run must refuse
// with exit code 2, name both values, and write nothing under --out.
func TestPackRefusesCapacityAbovePhysical(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=50MB", "--physical-capacity=4MB", "--out="+treeDir)
	if code != 2 {
		t.Fatalf("pack: exit %d, want 2: %s", code, out)
	}
	for _, want := range []string{"is above the physical capacity"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pack: output %q missing %q", out, want)
		}
	}
	if entries, err := os.ReadDir(treeDir); err == nil && len(entries) != 0 {
		t.Fatalf("pack: %s is not empty, a run was written despite the refusal", treeDir)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	treeDir := filepath.Join(work, "tree")
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir, "--close")
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
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "nothing to pack") || !strings.Contains(out, "no snapshot has been committed") {
		t.Fatalf("pack output %q, want the nothing-to-pack reason", out)
	}
}
