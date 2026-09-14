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

// packedTreeDir picks the "packed run ... into DIR" line out of pack's
// output.
func packedTreeDir(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if strings.HasPrefix(line, "packed run ") {
			if i := strings.LastIndex(line, " into "); i >= 0 {
				return line[i+len(" into "):]
			}
		}
	}
	t.Fatalf("no packed run line in pack output: %q", output)
	return ""
}

// packedDiscUUID picks the disc uuid out of pack's own "noahsark disc
// burned --repo=... UUID" next-steps line.
func packedDiscUUID(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "noahsark" && fields[1] == "disc" {
			return fields[len(fields)-1]
		}
	}
	t.Fatalf("no disc burned line in pack output: %q", output)
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
// for the walkthrough's loop-mount-before-burning check: since nothing
// has run "disc burned" yet, verify must leave every object PACKED and
// warn that the disc is not marked burned, even though its uuid is
// already in the ledger.
func TestVerifyLeavesObjectsPackedBeforeDiscBurned(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
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

	code, out := runCmd(t, "verify", "--repo="+repo, "--image="+mounted)
	if code != 0 {
		t.Fatalf("verify (unburned): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "not marked burned") || !strings.Contains(out, "disc burned --repo="+repo+" "+discUUID) {
		t.Fatalf("verify (unburned) output %q missing the not-marked-burned line, with --repo, for %s", out, discUUID)
	}
	if strings.Contains(out, "marked 0 object(s) CLEAN") {
		t.Fatalf("verify (unburned) output %q prints marked 0 object(s) CLEAN; want it omitted when nothing was BURNED", out)
	}
	if i := strings.Index(out, "verify: ok"); i < 0 || i > strings.Index(out, "not marked burned") {
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

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
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

	code, out = runCmd(t, "verify", "--repo="+repo, "--image="+mounted)
	if code != 0 {
		t.Fatalf("verify (burned): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "marked") || !strings.Contains(out, "CLEAN") || strings.Contains(out, "marked 0 object") {
		t.Fatalf("verify (burned) output %q did not mark objects CLEAN", out)
	}
	if strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (burned) output %q still warned about an unmarked disc", out)
	}

	// A second verify of the same disc is idempotent: every object is
	// already CLEAN, so nothing more is marked, and the CLEAN line does
	// not print at all, since no object was BURNED this time.
	code, out = runCmd(t, "verify", "--repo="+repo, "--image="+mounted)
	if code != 0 {
		t.Fatalf("verify (mounted, second pass): exit %d: %s", code, out)
	}
	if strings.Contains(out, "marked") && strings.Contains(out, "CLEAN") {
		t.Fatalf("verify (mounted, second pass) output %q, want no marked-CLEAN line when nothing was BURNED", out)
	}
}

// TestDiscBurnedUndo marks a disc burned, then undoes it: the run's
// objects must return to PACKED, and a following verify must again
// report the disc as not marked burned rather than reaching CLEAN.
func TestDiscBurnedUndo(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
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

	code, out = runCmd(t, "verify", "--repo="+repo, "--image="+mounted)
	if code != 0 {
		t.Fatalf("verify (after undo): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "not marked burned") {
		t.Fatalf("verify (after undo) output %q, want the not-marked-burned line again", out)
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

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
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

	code, out := runCmd(t, "verify", "--repo="+repo, "--image="+mounted)
	if code != 1 {
		t.Fatalf("verify (corrupt): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "returned") || !strings.Contains(out, "PACKED") {
		t.Fatalf("verify (corrupt) output %q missing the returned-to-PACKED line", out)
	}
	if strings.Contains(out, "returned 0 object") {
		t.Fatalf("verify (corrupt) output %q returned no objects to PACKED", out)
	}

	flipByte(t, chunkPath, 70) // undo the corruption

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned (again): exit %d: %s", code, out)
	}

	code, out = runCmd(t, "verify", "--repo="+repo, "--image="+mounted)
	if code != 0 {
		t.Fatalf("verify (repaired): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "marked") || !strings.Contains(out, "CLEAN") || strings.Contains(out, "marked 0 object") {
		t.Fatalf("verify (repaired) output %q did not mark objects CLEAN", out)
	}
}

// TestVerifyIgnoresATreeWithNoLedgerRow verifies a NOAHSARK tree whose
// disc uuid the repository's ledger has never seen: verify must not
// mark anything.
func TestVerifyIgnoresATreeWithNoLedgerRow(t *testing.T) {
	work := t.TempDir()
	repoA := filepath.Join(work, "repoA")
	repoB := filepath.Join(work, "repoB")
	src := writeFixtureSource(t)

	for _, repo := range []string{repoA, repoB} {
		if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
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

	// repoA's ledger has never seen this disc uuid.
	code, out := runCmd(t, "verify", "--repo="+repoA, "--image="+mounted)
	if code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "not a burned disc") {
		t.Fatalf("verify output %q missing the not-a-burned-disc line", out)
	}
	if _, err := os.Stat(filepath.Join(repoA, "staging", "state.db")); err == nil {
		t.Fatal("verify against an unknown disc wrote a state log")
	}
}
