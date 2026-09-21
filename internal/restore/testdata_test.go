package restore

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

func fixedClock() time.Time {
	return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
}

// buildFixtureSrc writes a small, deterministic source tree with enough
// bytes to span several FEC stripes: two pseudo-random files, a
// subdirectory, and a symlink.
func buildFixtureSrc(t *testing.T) string {
	t.Helper()
	srcDir := t.TempDir()

	r := rand.New(rand.NewSource(1))
	big := make([]byte, 1_400_000)
	r.Read(big)
	if err := os.WriteFile(filepath.Join(srcDir, "big1.bin"), big, 0o640); err != nil {
		t.Fatal(err)
	}
	r2 := rand.New(rand.NewSource(2))
	big2 := make([]byte, 1_400_000)
	r2.Read(big2)
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "big2.bin"), big2, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "small.txt"), []byte("small file content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("small.txt", filepath.Join(srcDir, "link-to-small")); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

// buildFixtureTree commits srcDir into a fresh staging directory, builds
// one run's NOAHSARK tree from it under treeDir, and returns the
// staging directory, the tree directory and the snapshot id.
func buildFixtureTree(t *testing.T, srcDir string) (stagingDir, treeDir string, snapID object.ID) {
	t.Helper()
	stagingDir = t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	treeDir = t.TempDir()
	opts := image.BuildOptions{
		StagingDir:            stagingDir,
		Snapshots:             []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors: 1 << 21,
		OutputDir:             treeDir,
		RepoUUID:              [16]byte{1, 2, 3, 4},
		DiscUUID:              [16]byte{5, 6, 7, 8},
		Label:                 "restore-test",
		FECEnabled:            true,
		Now:                   fixedClock,
	}
	if _, err := image.Build(opts); err != nil {
		t.Fatal(err)
	}
	return stagingDir, treeDir, snapID
}

// streamLayout resolves the FEC stream geometry and file paths for
// treeDir's one run, the same way Heal does.
func streamLayout(t *testing.T, treeDir string) (paths []string, sizes []uint64, layout *fec.StreamLayout) {
	t.Helper()
	base, err := image.FindNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	paths, sizes, _, err = image.StreamFiles(base, runDir)
	if err != nil {
		t.Fatal(err)
	}
	layout, err = fec.NewStreamLayout(sizes, fec.K)
	if err != nil {
		t.Fatal(err)
	}
	return paths, sizes, layout
}

// corruptDataBlockAt flips one byte of the FEC stream's data block at
// (column, stripe), by writing directly into the underlying file the
// given, already-resolved stream layout maps it to. It fails the test
// if that block falls in a file's virtual zero padding, since there is
// nothing real there to corrupt.
func corruptDataBlockAt(t *testing.T, paths []string, sizes []uint64, layout *fec.StreamLayout, column, stripe uint64) {
	t.Helper()
	block := column*layout.StripeCount() + stripe
	idx, off, err := layout.Locate(block)
	if err != nil {
		t.Fatal(err)
	}
	if off >= sizes[idx] {
		t.Fatalf("column %d stripe %d falls in padding of %s, nothing to corrupt", column, stripe, paths[idx])
	}
	flipByte(t, paths[idx], int64(off))
}

// corruptParityBlock flips one byte of parity column j's block for
// stripe.
func corruptParityBlock(t *testing.T, treeDir string, j int, stripe uint64) {
	t.Helper()
	base, err := image.FindNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(runDir, "parity", parityFileName(j))
	off := int64(1+stripe) * fec.BlockSize
	flipByte(t, p, off)
}

func parityFileName(j int) string {
	return fmt.Sprintf("p%04d.bin", fec.K+1+j)
}

func flipByte(t *testing.T, path string, off int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var b [1]byte
	if _, err := f.ReadAt(b[:], off); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b[:], off); err != nil {
		t.Fatal(err)
	}
}

// problemsOf returns every problem of kind k that rep holds.
func problemsOf(rep Report, k Kind) []Problem {
	var out []Problem
	for _, p := range rep.Problems {
		if p.Kind == k {
			out = append(out, p)
		}
	}
	return out
}
