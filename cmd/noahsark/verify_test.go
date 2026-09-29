package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// discVerifyState returns the clean object count of the first disc,
// read the same way "status" computes it, and the lowest verify count of
// its CLEAN objects in the state log.
func discVerifyState(t *testing.T, repo string) (clean int, verifyCount uint8) {
	t.Helper()
	discs := statusDiscs(t, repo)
	if len(discs) == 0 {
		t.Fatalf("status names no disc")
	}
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	l, err := stage.OpenReadOnly(cfg.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range l.MinCleanVerifyCountByDisc() {
		verifyCount = n
	}
	return discs[0].CleanObjects, verifyCount
}

// TestVerifyLeavesObjectsPackedBeforeDiscBurned runs verify on a
// byte-identical copy of the packed tree, outside staging, standing in
// for the guide's loop-mount-before-burning check: since nothing
// has run "disc burned" yet, verify must leave every object PACKED and
// warn that the disc is not marked burned, even though its uuid is
// already in the ledger.
func TestVerifyLeavesObjectsPackedBeforeDiscBurned(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify (unburned): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "not marked burned") || !strings.Contains(out, "run: noahsark disc burned 0") {
		t.Fatalf("verify (unburned) output %q missing the not-marked-burned line for disc 0", out)
	}
	if strings.Contains(out, "0 object(s) verified on") {
		t.Fatalf("verify (unburned) output %q prints a zero verified count; want the line omitted when nothing was BURNED", out)
	}
	if i := strings.Index(out, ", ok"); i < 0 || i > strings.Index(out, "not marked burned") {
		t.Fatalf("verify (unburned) output %q, want the not-marked-burned hint after the ok line", out)
	}
}

// findAChunkFile walks base/objects and returns the path of the first
// file whose magic_kind is CHUNK.
func findAChunkFile(t *testing.T, base string) string {
	t.Helper()
	var found string
	err := filepath.Walk(filepath.Join(base, "objects"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		var h format.CommonHeader
		if h.Decode(data) != nil {
			return nil
		}
		if h.MagicKind == format.MagicChunk {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("no chunk file found")
	}
	return found
}

// flipByte flips one bit at offset in the file at path.
func flipByte(t *testing.T, path string, offset int64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[offset] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyFailureReturnsBurnedToPacked marks a disc burned, corrupts
// a chunk in its mounted copy: verify must fail, and must return every
// object of that run from BURNED to PACKED rather than leaving it
// BURNED forever. Repairing the byte, marking the disc burned again,
// and verifying once more must then reach CLEAN, showing the run is
// not stuck.
func TestVerifyFailureReturnsBurnedToPacked(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	base, err := image.FindNoahsark(mounted, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	chunkPath := findAChunkFile(t, base)
	flipByte(t, chunkPath, 70) // inside the payload, past the header

	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 1 {
		t.Fatalf("verify (corrupt): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "object(s) returned to packed") {
		t.Fatalf("verify (corrupt) output %q missing the returned-to-packed line", out)
	}
	if strings.Contains(out, "0 object(s) returned to packed") {
		t.Fatalf("verify (corrupt) output %q returned no object to packed", out)
	}
	if !strings.Contains(out, "the burn mark is removed") {
		t.Fatalf("verify (corrupt) output %q does not say the burn mark is removed", out)
	}
	if !strings.Contains(out, "next: burn a new disc") || !strings.Contains(out, "noahsark disc burned") {
		t.Fatalf("verify (corrupt) output %q does not name the next step", out)
	}

	flipByte(t, chunkPath, 70) // undo the corruption

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned (again): exit %d: %s", code, out)
	}

	code, out = runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify (repaired): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "object(s) verified on") || strings.Contains(out, "0 object(s) verified on") {
		t.Fatalf("verify (repaired) output %q did not report the objects verified", out)
	}
}

// TestVerifyIgnoresATreeWithNoLedgerRow verifies a NOAHSARK tree whose
// disc uuid the repository's ledger has never seen: verify must refuse
// instead of marking anything, or reporting ok.
func TestVerifyIgnoresATreeWithNoLedgerRow(t *testing.T) {
	work := t.TempDir()
	repoA := filepath.Join(work, "repoA")
	repoB := filepath.Join(work, "repoB")
	src := writeFixtureSource(t)

	for _, repo := range []string{repoA, repoB} {
		if code, out := runIn(t, repo, "init"); code != 0 {
			t.Fatalf("init %s: exit %d: %s", repo, code, out)
		}
	}
	if code, out := runCmd(t, "--repo="+repoB, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repoB, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	treeFromB := packedTreeDir(t, packOut)
	mounted := filepath.Join(work, "mounted")
	copyTree(t, treeFromB, mounted)

	// repoA's ledger has never seen this disc uuid: verify refuses
	// rather than silently reporting ok against the wrong repository.
	code, out := runCmd(t, "--repo="+repoA, "verify", mounted)
	if code != 1 {
		t.Fatalf("verify: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "is not in repository") {
		t.Fatalf("verify output %q missing the not-in-repository refusal", out)
	}
	if strings.Contains(out, "verify: ok") {
		t.Fatalf("verify output %q, want no ok line for a disc not in this repository", out)
	}
	if _, err := os.Stat(filepath.Join(repoA, "staging", "state.db")); err == nil {
		t.Fatal("verify against an unknown disc wrote a state log")
	}
}

// TestVerifyAcceptsPositionalDiscRoot checks that verify takes exactly
// one DISC-ROOT positional argument, and refuses two.
func TestVerifyAcceptsPositionalDiscRoot(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify DISC-ROOT: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "disc 0 ") || !strings.Contains(out, ", ok") {
		t.Fatalf("verify DISC-ROOT output %q missing the disc line with ok", out)
	}

	code, out = runCmd(t, "--repo="+repo, "verify", mounted, mounted)
	if code != 2 {
		t.Fatalf("verify with two DISC-ROOT arguments: exit %d, want 2: %s", code, out)
	}
}

// TestVerifyHealReportsBlocks checks that verify --heal reports in the
// operator's words: repaired blocks, with no talk of stripes.
func TestVerifyHealReportsBlocks(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	tree := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec", "--out="+tree); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	healed := filepath.Join(work, "healed")
	code, out := runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+healed, tree)
	if code != 0 {
		t.Fatalf("verify --heal: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "heal: repaired 0 block(s)") {
		t.Fatalf("verify --heal output %q, want the repaired-blocks line", out)
	}
	if strings.Contains(out, "stripe") {
		t.Fatalf("verify --heal output %q still uses the word stripe", out)
	}
}

// TestVerifyHealNeverCountsAsACopy verifies the real disc once, then
// heals it into a directory on the hard disk. A heal must leave the
// verify count and the CLEAN object count exactly as the one real verify
// left them, and it must tell the operator to burn and verify a new disc.
func TestVerifyHealNeverCountsAsACopy(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	mounted := filepath.Join(work, "mounted")
	copyTree(t, packedTreeDir(t, packOut), mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", packedDiscUUID(t, packOut)); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "verify: verified") {
		t.Fatalf("verify output %q, want the verified line", out)
	}
	cleanAfterVerify, countAfterVerify := discVerifyState(t, repo)

	healed := filepath.Join(work, "healed")
	code, out = runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+healed, mounted)
	if code != 0 {
		t.Fatalf("verify --heal: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "heal: repaired 0 block(s)") {
		t.Fatalf("verify --heal output %q, want the repaired-blocks line", out)
	}
	if !strings.Contains(out, "burn the healed tree to a new disc") {
		t.Fatalf("verify --heal output %q, want it to say to burn the healed tree and verify that disc", out)
	}
	if strings.Contains(out, "verify: verified") {
		t.Fatalf("verify --heal output %q, want no verified line: a heal is not a copy", out)
	}

	cleanAfterHeal, countAfterHeal := discVerifyState(t, repo)
	if cleanAfterHeal != cleanAfterVerify {
		t.Fatalf("clean objects = %d after heal, want %d unchanged", cleanAfterHeal, cleanAfterVerify)
	}
	if countAfterHeal != countAfterVerify || countAfterHeal != 1 {
		t.Fatalf("verify count = %d after heal, want %d and 1", countAfterHeal, countAfterVerify)
	}
}

// TestVerifyHealRefusesWithNoOut checks that --heal with no --out is a
// usage error: healing in place is no longer supported.
func TestVerifyHealRefusesWithNoOut(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	tree := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec", "--out="+tree); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "verify", "--heal", tree)
	if code != 2 {
		t.Fatalf("verify --heal (no --out): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--heal needs --out") {
		t.Fatalf("verify --heal (no --out) output %q, want it to name the missing --out", out)
	}
}

// TestVerifyUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// verify: each case exits 2, never 0 or 1.
func TestVerifyUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"missing DISC-ROOT", []string{"--repo=" + repo, "verify"}},
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
