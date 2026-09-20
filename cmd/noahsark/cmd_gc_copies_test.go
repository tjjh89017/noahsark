package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// packBurnDisc commits src into repo, packs it, copies the packed tree
// outside the staging directory to stand in for a mounted disc, and
// marks the disc burned. It runs no verify, so each test drives the
// verify count itself.
func packBurnDisc(t *testing.T, work, repo, src string) string {
	t.Helper()
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	mounted := filepath.Join(work, filepath.Base(t.TempDir()))
	copyTree(t, packedTreeDir(t, packOut), mounted)
	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, packedDiscUUID(t, packOut)); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	return mounted
}

// TestSecondVerifyRaisesTheVerifyCount checks the two verifies of the
// two identical discs: the objects stay CLEAN, the count goes from 1 to
// 2, and "disc list" reports the count against gc.min_verified_copies.
func TestSecondVerifyRaisesTheVerifyCount(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	mounted := packBurnDisc(t, work, repo, src)

	code, out := runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify copy 1: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "verify: copy 1 of 2 verified; verify the second copy before gc") {
		t.Fatalf("verify copy 1 output %q, want the copy 1 of 2 line", out)
	}
	cleanAfterFirst, verifiedAfterFirst := discListCounts(t, repo)
	if cleanAfterFirst == 0 {
		t.Fatal("disc list reports 0 clean objects after the first verify")
	}
	if verifiedAfterFirst != "1/2" {
		t.Fatalf("disc list verified = %q after the first verify, want 1/2", verifiedAfterFirst)
	}

	code, out = runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify copy 2: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "verify: 2 of 2 copies verified") {
		t.Fatalf("verify copy 2 output %q, want the 2 of 2 line", out)
	}
	cleanAfterSecond, verifiedAfterSecond := discListCounts(t, repo)
	if cleanAfterSecond != cleanAfterFirst {
		t.Fatalf("clean objects = %d after the second verify, want %d", cleanAfterSecond, cleanAfterFirst)
	}
	if verifiedAfterSecond != "2/2" {
		t.Fatalf("disc list verified = %q after the second verify, want 2/2", verifiedAfterSecond)
	}
}

// discListCounts returns the clean object count and the verified column
// of the first disc "disc list" prints.
func discListCounts(t *testing.T, repo string) (clean int, verified string) {
	t.Helper()
	code, out := runCmd(t, "disc", "list", "--repo="+repo)
	if code != 0 {
		t.Fatalf("disc list: exit %d: %s", code, out)
	}
	line, _, _ := strings.Cut(out, "\n")
	m := discListLineRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("disc line %q does not match the expected column order", line)
	}
	n, err := strconv.Atoi(m[8])
	if err != nil {
		t.Fatalf("clean count %q: %v", m[8], err)
	}
	return n, m[9] + "/" + m[10]
}

// TestGCHoldsObjectsUntilTheSecondVerify checks that one verify is not
// enough: after the whole retention period gc still deletes nothing and
// names the disc it holds objects back for. The second verify frees
// them.
func TestGCHoldsObjectsUntilTheSecondVerify(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 1h")

	before := time.Now()
	mounted := packBurnDisc(t, work, repo, src)
	if code, out := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
		t.Fatalf("verify copy 1: exit %d: %s", code, out)
	}
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "would delete 0 object") {
		t.Fatalf("gc --dry-run output %q, want 0 objects", out)
	}
	if !strings.Contains(out, "1 of 2 copies verified") || !strings.Contains(out, "verify the second copy") {
		t.Fatalf("gc --dry-run output %q, want the held line", out)
	}

	objDir := filepath.Join(repo, "staging", "objects")
	staged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "gc", "--repo="+repo)
	if code != 0 {
		t.Fatalf("gc: exit %d, want 0 (nothing eligible): %s", code, out)
	}
	if !strings.Contains(out, "1 of 2 copies verified") {
		t.Fatalf("gc output %q, want the held line", out)
	}
	stillStaged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if stillStaged != staged {
		t.Fatalf("staging/objects has %d files after the held gc, had %d; want no delete", stillStaged, staged)
	}

	if code, out := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
		t.Fatalf("verify copy 2: exit %d: %s", code, out)
	}
	code, out = runCmd(t, "gc", "--repo="+repo)
	if code != 0 {
		t.Fatalf("gc after the second verify: exit %d: %s", code, out)
	}
	if strings.Contains(out, "deleted 0 object") {
		t.Fatalf("gc after the second verify output %q, want more than 0 objects deleted", out)
	}
	if strings.Contains(out, "copies verified;") {
		t.Fatalf("gc after the second verify output %q, want no held line", out)
	}
	afterGC, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if afterGC >= staged {
		t.Fatalf("staging/objects has %d files after gc, had %d before; want fewer", afterGC, staged)
	}
}

// TestGCMinVerifiedCopiesOne checks the escape hatch of an operator who
// keeps one copy: with gc.min_verified_copies = 1, one verify frees the
// objects once the retention period has passed.
func TestGCMinVerifiedCopiesOne(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 1h")
	appendConfigLine(t, repo, "gc.min_verified_copies = 1")

	before := time.Now()
	mounted := packBurnDisc(t, work, repo, src)
	code, out := runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "verify: 1 of 1 copies verified") {
		t.Fatalf("verify output %q, want the 1 of 1 line", out)
	}

	gcClock = func() time.Time { return before.Add(2 * time.Hour) }
	code, out = runCmd(t, "gc", "--repo="+repo)
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if strings.Contains(out, "deleted 0 object") {
		t.Fatalf("gc output %q, want more than 0 objects deleted", out)
	}
}

// TestGCForceAfterDoesNotBypassTheVerifyCount checks that --force-after
// shortens the retention period only. One verified copy still holds the
// objects.
func TestGCForceAfterDoesNotBypassTheVerifyCount(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	mounted := packBurnDisc(t, work, repo, src)
	if code, out := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	objDir := filepath.Join(repo, "staging", "objects")
	staged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	setGCStdin(t, strings.NewReader("y\n"))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=0s")
	if code != 0 {
		t.Fatalf("gc --force-after=0s: exit %d, want 0 (nothing eligible): %s", code, out)
	}
	if !strings.Contains(out, "1 of 2 copies verified") {
		t.Fatalf("gc --force-after output %q, want the held line", out)
	}
	stillStaged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if stillStaged != staged {
		t.Fatalf("staging/objects has %d files after --force-after, had %d; want no delete", stillStaged, staged)
	}
}

// TestConfigRefusesMinVerifiedCopiesBelowOne checks that a value below 1
// is a config error, not a silent "delete at once" setting.
func TestConfigRefusesMinVerifiedCopiesBelowOne(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "gc.min_verified_copies = 0")

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 2 {
		t.Fatalf("gc with gc.min_verified_copies = 0: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "gc.min_verified_copies: must be at least 1") {
		t.Fatalf("gc output %q, want the config error", out)
	}
}
