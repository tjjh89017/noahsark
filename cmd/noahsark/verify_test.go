package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// TestSecondVerifyRaisesTheVerifyCount checks the two verifies of the
// two identical discs: the objects stay CLEAN, the count goes from 1 to
// 2, and "status" reports the count against gc.min_verified_copies.
func TestSecondVerifyRaisesTheVerifyCount(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	mounted := packBurnDisc(t, work, repo, src)

	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify copy 1: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "verify: copy 1 of 2 verified; verify the second copy before gc") {
		t.Fatalf("verify copy 1 output %q, want the copy 1 of 2 line", out)
	}
	cleanAfterFirst, verifiedAfterFirst := discListCounts(t, repo)
	if cleanAfterFirst == 0 {
		t.Fatal("status reports 0 clean objects after the first verify")
	}
	if verifiedAfterFirst != "1/2" {
		t.Fatalf("status verified = %q after the first verify, want 1/2", verifiedAfterFirst)
	}

	code, out = runCmd(t, "--repo="+repo, "verify", mounted)
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
		t.Fatalf("status verified = %q after the second verify, want 2/2", verifiedAfterSecond)
	}
}

// discListCounts returns the clean object count and the verified column
// of the first disc, read the same way "status" computes them.
func discListCounts(t *testing.T, repo string) (clean int, verified string) {
	t.Helper()
	discs := statusDiscs(t, repo)
	if len(discs) == 0 {
		t.Fatalf("status names no disc")
	}
	d := discs[0]
	return d.CleanObjects, strconv.Itoa(int(d.VerifiedCopies)) + "/" + strconv.Itoa(d.MinCopies)
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

// TestVerifyThirdCopyPrintsVerified checks that a verify past
// gc.min_verified_copies prints "verified" and never "3 of 2".
func TestVerifyThirdCopyPrintsVerified(t *testing.T) {
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
	mounted := filepath.Join(work, "mounted")
	copyTree(t, packedTreeDir(t, packOut), mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", packedDiscUUID(t, packOut)); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	var out string
	for copyNumber := 1; copyNumber <= 3; copyNumber++ {
		if code, o := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
			t.Fatalf("verify %d: exit %d: %s", copyNumber, code, o)
		} else {
			out = o
		}
	}
	if strings.Contains(out, "3 of 2") {
		t.Fatalf("third verify output %q counts past the minimum", out)
	}
	if !strings.Contains(out, "verify: verified") {
		t.Fatalf("third verify output %q does not say verified", out)
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

// TestVerifyHealNeverCountsAsACopy reproduces the reported bug: verify
// the real disc once (copy 1 of 2), then heal it into a directory on the
// hard disk. Before this fix, the healed directory's own verify raised
// the count to "2 of 2 copies verified", though no second disc exists.
// A heal must leave the count, and the CLEAN object count, exactly as
// the one real verify left them, and it must tell the operator to burn
// and verify a real second disc instead.
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
		t.Fatalf("verify copy 1: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "verify: copy 1 of 2 verified; verify the second copy before gc") {
		t.Fatalf("verify copy 1 output %q, want the copy 1 of 2 line", out)
	}
	cleanAfterVerify, verifiedAfterVerify := discListCounts(t, repo)

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
	if strings.Contains(out, "copies verified") {
		t.Fatalf("verify --heal output %q, want no verify-count line: a heal is not a copy", out)
	}

	cleanAfterHeal, verifiedAfterHeal := discListCounts(t, repo)
	if cleanAfterHeal != cleanAfterVerify {
		t.Fatalf("clean objects = %d after heal, want %d unchanged", cleanAfterHeal, cleanAfterVerify)
	}
	if verifiedAfterHeal != verifiedAfterVerify {
		t.Fatalf("verified copies = %q after heal, want %q unchanged", verifiedAfterHeal, verifiedAfterVerify)
	}
	if verifiedAfterHeal != "1/2" {
		t.Fatalf("verified copies = %q after heal, want 1/2", verifiedAfterHeal)
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
