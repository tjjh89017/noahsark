package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// packedTreeDir picks the "tree: DIR" line out of pack's output.
func packedTreeDir(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if after, found := strings.CutPrefix(line, "tree: "); found {
			return after
		}
	}
	t.Fatalf("no tree line in pack output: %q", output)
	return ""
}

// packedDiscUUID picks the disc uuid out of pack's "uuid: UUID" line.
func packedDiscUUID(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if after, found := strings.CutPrefix(line, "uuid: "); found {
			return after
		}
	}
	t.Fatalf("no uuid line in pack output: %q", output)
	return ""
}

// copyTree copies src to dst, standing in for burning src's bytes to a
// disc and mounting it back (or loop-mounting the image before it is
// burned): a byte-identical tree outside the repository's staging
// directory.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	cmd := exec.Command("cp", "-a", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cp -a %s %s: %v: %s", src, dst, err, out)
	}
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	code, out := runCmd(t, "verify", "--repo="+repo, mounted)
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

// TestDiscBurnedThenVerifyReachesClean runs "disc burned", then verify:
// only after "disc burned" has moved the run's objects to BURNED can a
// passing verify move them on to CLEAN. "disc burned --undo" reverses
// that, back to PACKED, so a following verify again reports the disc
// not marked burned.
func TestDiscBurnedThenVerifyReachesClean(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID)
	if code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "marked burned") || strings.Contains(out, "marked burned, 0 objects") {
		t.Fatalf("disc burned output %q did not mark objects burned", out)
	}

	code, out = runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify (burned): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "object(s) verified on") || strings.Contains(out, "0 object(s) verified on") {
		t.Fatalf("verify (burned) output %q did not report the objects verified", out)
	}
	if strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (burned) output %q still warned about an unmarked disc", out)
	}

	// A second verify of the same disc is idempotent: every object is
	// already CLEAN, so nothing more is marked, and the CLEAN line does
	// not print at all, since no object was BURNED this time.
	code, out = runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify (mounted, second pass): exit %d: %s", code, out)
	}
	if strings.Contains(out, "object(s) verified on") {
		t.Fatalf("verify (mounted, second pass) output %q, want no verified-count line when nothing was BURNED", out)
	}
}

// TestDiscBurnedUndo marks a disc burned, then undoes it: the run's
// objects must return to PACKED, and a following verify must again
// report the disc as not marked burned rather than reaching CLEAN.
func TestDiscBurnedUndo(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "burned", "--undo", "--repo="+repo, discUUID)
	if code != 0 {
		t.Fatalf("disc burned --undo: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "returned to packed") || strings.Contains(out, "returned to packed, 0 objects") {
		t.Fatalf("disc burned --undo output %q did not return objects to packed", out)
	}

	code, out = runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify (after undo): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (after undo) output %q, want the not-marked-burned line again", out)
	}
}

// TestDiscBurnedUndoRefusedOnceClean checks that "disc burned --undo"
// refuses, and changes nothing, once verify has already moved a disc's
// objects on to CLEAN: a verified disc cannot be returned to packed.
func TestDiscBurnedUndoRefusedOnceClean(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "burned", "--undo", "--repo="+repo, discUUID)
	if code != 1 {
		t.Fatalf("disc burned --undo (verified disc): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "is verified and cannot be returned to packed") {
		t.Fatalf("disc burned --undo output %q missing the verified-disc refusal", out)
	}

	// Nothing changed: a following verify still reports every object
	// CLEAN, not reset to PACKED.
	code, out = runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify (after refused undo): exit %d: %s", code, out)
	}
	if strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (after refused undo) output %q, want the disc still burned and clean", out)
	}
}

// TestDiscBurnedBySeqAndLabel checks that "disc burned" accepts the
// disc_seq and the on-disc label in place of the full uuid.
func TestDiscBurnedBySeqAndLabel(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--label=spare-1"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "burned", "--repo="+repo, "0")
	if code != 0 {
		t.Fatalf("disc burned 0 (by seq): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "marked burned") || strings.Contains(out, "marked burned, 0 objects") {
		t.Fatalf("disc burned 0 output %q did not mark objects burned", out)
	}

	code, out = runCmd(t, "disc", "burned", "--undo", "--repo="+repo, defaultDiscLabel(t, repo, 0))
	if code != 0 {
		t.Fatalf("disc burned --undo (by label): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "returned to packed") || strings.Contains(out, "returned to packed, 0 objects") {
		t.Fatalf("disc burned --undo (by label) output %q did not return objects to packed", out)
	}
}

// defaultDiscLabel returns disc seq's on-disc label from "status".
func defaultDiscLabel(t *testing.T, repo string, seq uint64) string {
	t.Helper()
	for _, r := range statusDiscs(t, repo) {
		if r.Seq == seq {
			return r.Label
		}
	}
	t.Fatalf("status --json names no disc %d", seq)
	return ""
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	base, err := image.FindNoahsark(mounted, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	chunkPath := findAChunkFile(t, base)
	flipByte(t, chunkPath, 70) // inside the payload, past the header

	code, out := runCmd(t, "verify", "--repo="+repo, mounted)
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

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned (again): exit %d: %s", code, out)
	}

	code, out = runCmd(t, "verify", "--repo="+repo, mounted)
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
		if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
			t.Fatalf("init %s: exit %d: %s", repo, code, out)
		}
	}
	if code, out := runCmd(t, "commit", "--repo="+repoB, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repoB, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	treeFromB := packedTreeDir(t, packOut)
	mounted := filepath.Join(work, "mounted")
	copyTree(t, treeFromB, mounted)

	// repoA's ledger has never seen this disc uuid: verify refuses
	// rather than silently reporting ok against the wrong repository.
	code, out := runCmd(t, "verify", "--repo="+repoA, mounted)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	code, out := runCmd(t, "verify", "--repo="+repo, mounted)
	if code != 0 {
		t.Fatalf("verify DISC-ROOT: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "disc 0 ") || !strings.Contains(out, ", ok") {
		t.Fatalf("verify DISC-ROOT output %q missing the disc line with ok", out)
	}

	code, out = runCmd(t, "verify", "--repo="+repo, mounted, mounted)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	mounted := filepath.Join(work, "mounted")
	copyTree(t, packedTreeDir(t, packOut), mounted)
	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, packedDiscUUID(t, packOut)); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	var out string
	for copyNumber := 1; copyNumber <= 3; copyNumber++ {
		if code, o := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
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
