package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// stagingLineRe matches the plan line of the items that the staging
// store supplies.
var stagingLineRe = regexp.MustCompile(`(?m)^staging: ([1-9]\d*) items, ([1-9]\d*) bytes$`)

// TestRestoreFromStagingNoDisc restores a snapshot that no disc holds
// yet. The staging store holds every chunk, thus the plan names no disc
// and restore completes with no disc at --disc.
func TestRestoreFromStagingNoDisc(t *testing.T) {
	repo, src := initAndCommit(t)
	before := treeDigest(t, repo)

	code, out := runRestoreDryRun(t, repo, defaultRefName())
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if !stagingLineRe.MatchString(out) {
		t.Fatalf("restore --dry-run output %q has no staging line", out)
	}
	if strings.Contains(out, "no disc known") || planLineRe.MatchString(out) {
		t.Fatalf("restore --dry-run output %q names a disc", out)
	}
	if discs, items, bytes := planTotals(t, out); discs != 0 || items != 0 || bytes != 0 {
		t.Fatalf("totals %d discs, %d items, %d bytes, want 0", discs, items, bytes)
	}

	dest := filepath.Join(t.TempDir(), "out")
	code, out = runCmd(t, "--repo="+repo, "restore", "--disc="+t.TempDir(), defaultRefName(), dest)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Contains(out, "found") || strings.Contains(out, "insert disc") || strings.Contains(out, "warning:") {
		t.Fatalf("restore output %q reads or asks for a disc", out)
	}
	compareTrees(t, dest, src)
	if after := treeDigest(t, repo); after != before {
		t.Fatal("restore changed a file of the repository")
	}
}

// TestRestoreStagingChunkCorruptUsesDisc damages one chunk file of the
// staging store. restore names it on one line, takes that chunk from
// the disc, and takes every other chunk from the staging store.
func TestRestoreStagingChunkCorruptUsesDisc(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)
	ids := stagingChunkIDs(t, repo)
	if len(ids) < 2 {
		t.Fatalf("the staging store holds %d chunk(s), want at least 2", len(ids))
	}
	bad := ids[0]
	flipLastByte(t, testLayout(t, repo).chunkFile(bad))
	warning := "noahsark: restore: staging chunk " + bad.TextForm() + ": "

	code, out := runRestoreDryRun(t, repo, snapID)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if !strings.Contains(out, warning) || !strings.Contains(out, "; restore reads the chunk from a disc\n") {
		t.Fatalf("restore --dry-run output %q does not name the damaged chunk", out)
	}
	if m := stagingLineRe.FindStringSubmatch(out); m == nil || m[1] != strconv.Itoa(len(ids)-1) {
		t.Fatalf("restore --dry-run output %q, want a staging line with %d items", out, len(ids)-1)
	}
	// The warning goes to standard error, which the output holds last.
	if m := totalsRe.FindStringSubmatch(out); m == nil || m[1] != "1" || m[2] != "1" {
		t.Fatalf("restore --dry-run output %q, want totals of one disc with one item", out)
	}

	dest := filepath.Join(t.TempDir(), "out")
	code, out = runCmd(t, "--repo="+repo, "restore", "--disc="+treeDir, snapID, dest)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if strings.Count(out, warning) != 1 || !strings.Contains(out, ": found\n") {
		t.Fatalf("restore output %q: want one line for the damaged chunk and the found line of the disc", out)
	}
	compareTrees(t, dest, src)
}

// TestRestoreDryRunStagingShrinksDiscList puts the chunks of the second
// disc back into the staging store. The plan then names only the first
// disc, and restore completes with the first disc alone.
func TestRestoreDryRunStagingShrinksDiscList(t *testing.T) {
	repo, snapID, src, discRoots := discSwapFixture(t)
	if seqs := restoreDryRunDiscSeqs(t, repo, snapID); !slices.Equal(seqs, []int{0, 1}) {
		t.Fatalf("plan named discs %v, want [0 1]", seqs)
	}
	copied := copyDiscObjectsToStaging(t, repo, discRoots[1])

	code, out := runRestoreDryRun(t, repo, snapID)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if seqs := planLineSeqs(t, out); !slices.Equal(seqs, []int{0}) {
		t.Fatalf("plan named discs %v, want [0]: %s", seqs, out)
	}
	m := stagingLineRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("restore --dry-run output %q has no staging line", out)
	}
	if n, _ := strconv.Atoi(m[1]); n > copied {
		t.Fatalf("restore --dry-run output %q, want a staging line with at most %d items", out, copied)
	}
	if !strings.HasPrefix(out, m[0]) {
		t.Fatalf("restore --dry-run output %q does not start with the staging line", out)
	}

	dest := filepath.Join(t.TempDir(), "out")
	code, out = runCmd(t, "--repo="+repo, "restore", "--disc="+discRoots[0], snapID, dest)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	if got := foundDiscSeqs(t, out); !slices.Equal(got, []int{0}) {
		t.Fatalf("found lines for disc_seq %v, want [0]: %s", got, out)
	}
	compareTrees(t, dest, src)
}

// stagingChunkIDs returns the id of each chunk file of the staging store
// of repo.
func stagingChunkIDs(t *testing.T, repo string) []object.ID {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(testLayout(t, repo).chunksDir(), "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []object.ID
	for _, p := range paths {
		id, err := object.ParseID(filepath.Base(p))
		if err != nil {
			t.Fatalf("chunk file %s: %v", p, err)
		}
		ids = append(ids, id)
	}
	return ids
}

// flipLastByte inverts the last byte of the file at path.
func flipLastByte(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// copyDiscObjectsToStaging copies each object file of the disc root
// into the chunk directory of the staging store of repo, and returns
// the number of files it copied. The staging store holds a chunk in the
// same object file form as a disc.
func copyDiscObjectsToStaging(t *testing.T, repo, root string) int {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "NOAHSARK", "objects", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("the disc root %s holds no object file", root)
	}
	layout := testLayout(t, repo)
	for _, p := range paths {
		id, err := object.ParseID(filepath.Base(p))
		if err != nil {
			t.Fatalf("object file %s: %v", p, err)
		}
		dst := layout.chunkFile(id)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		copyFile(t, p, dst)
	}
	return len(paths)
}

// copyFile copies the file from to the new file to.
func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
