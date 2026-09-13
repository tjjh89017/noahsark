package restore

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/chunker"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// smallSparseProfile is a small chunker profile, so a hole of a few
// megabytes is guaranteed to contain a whole all-zero chunk instead of
// one long chunk that mixes data and zero throughout. It follows the
// same normalization-level-2 mask rule as the production profiles; it is
// not one of them.
var smallSparseProfile = chunker.Profile{
	Min:   1 << 16,
	Avg:   1 << 18,
	Max:   1 << 20,
	MaskS: spreadMaskForTest(20),
	MaskL: spreadMaskForTest(16),
}

// spreadMaskForTest mirrors FORMAT.md's mask rule for an arbitrary bit
// count, used only to build smallSparseProfile.
func spreadMaskForTest(n int) uint64 {
	var mask uint64
	for j := 0; j < n; j++ {
		mask |= 1 << uint(63-(j*32/n))
	}
	return mask
}

// writeSparseSourceFile creates path as a sparse file of len(data) bytes:
// it writes only the leading and trailing 64 KiB of data and leaves the
// zero middle region as an unwritten hole.
func writeSparseSourceFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(data[:1<<16], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(data[len(data)-1<<16:], int64(len(data)-1<<16)); err != nil {
		t.Fatal(err)
	}
}

// buildSparseFixtureTree commits srcDir with smallSparseProfile and
// builds one run's NOAHSARK tree from it, returning the tree directory
// and the snapshot id.
func buildSparseFixtureTree(t *testing.T, srcDir string) (treeDir string, snapID object.ID) {
	t.Helper()
	stagingDir := t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	w.Profile = smallSparseProfile
	id, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	treeDir = t.TempDir()
	opts := image.BuildOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "LATEST", ID: id, Time: fixedClock()}},
		TargetCapacitySectors:   1 << 21,
		PhysicalCapacitySectors: 1 << 21,
		OutputDir:               treeDir,
		RepoUUID:                [16]byte{1, 2, 3, 4},
		DiscUUID:                [16]byte{5, 6, 7, 8},
		Label:                   "sparse-restore-test",
		Now:                     fixedClock,
	}
	if _, err := image.Build(opts); err != nil {
		t.Fatal(err)
	}
	return treeDir, id
}

// allocatedBytes returns path's allocated size (os.FileInfo Sys, Blocks
// times 512), and whether the platform exposes block counts at all.
func allocatedBytes(t *testing.T, path string) (int64, bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Blocks * 512, true
}

// allocationReportingSupported checks that dir's filesystem reports a
// hole's blocks as unallocated, the property the block assertions below
// depend on. tmpfs reports it; a filesystem that does not is skipped
// rather than failed.
func allocationReportingSupported(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "alloc-probe.bin")
	f, err := os.Create(probe)
	if err != nil {
		t.Fatal(err)
	}
	const size = 4 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(probe)
	blocks, ok := allocatedBytes(t, probe)
	return ok && blocks < size/2
}

// TestRestoreSparseFileStaysSparse commits a source file with a real
// hole in the middle, restores it, and checks the content matches the
// source and the restored file allocates well below its logical size.
func TestRestoreSparseFileStaysSparse(t *testing.T) {
	srcDir := t.TempDir()
	const size = 4 << 20 // several times smallSparseProfile.Max, so the
	// hole holds a genuine all-zero Max-sized chunk, not just a mixed
	// boundary chunk.
	data := make([]byte, size)
	rand.New(rand.NewSource(31)).Read(data[:1<<16])
	rand.New(rand.NewSource(32)).Read(data[size-1<<16:])
	// data[1<<16 : size-1<<16] stays zero: the hole.

	writeSparseSourceFile(t, filepath.Join(srcDir, "sparse.bin"), data)

	treeDir, snapID := buildSparseFixtureTree(t, srcDir)

	outDir := t.TempDir()
	if err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatal(err)
	}

	restored := filepath.Join(outDir, srcDir, "sparse.bin")
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("restored content does not match the source")
	}

	if !allocationReportingSupported(t, outDir) {
		t.Skip("filesystem does not report block allocation")
	}
	blocks, ok := allocatedBytes(t, restored)
	if !ok {
		t.Fatal("expected block reporting to be available")
	}
	if blocks > size*3/4 {
		t.Errorf("restored sparse file allocated %d bytes, want well below %d", blocks, size)
	}
}

// TestRestoreDenseZeroFileStaysDense commits an all-zero file with no
// hole, restores it, and checks the content matches and the restored
// file allocates close to its full size, proving the zero-chunk rule
// does not turn a dense file sparse on restore.
func TestRestoreDenseZeroFileStaysDense(t *testing.T) {
	srcDir := t.TempDir()
	const size = 4 << 20
	data := make([]byte, size) // all zero, written with one dense Write.

	densePath := filepath.Join(srcDir, "dense.bin")
	if err := os.WriteFile(densePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	treeDir, snapID := buildSparseFixtureTree(t, srcDir)

	outDir := t.TempDir()
	if err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatal(err)
	}

	restored := filepath.Join(outDir, srcDir, "dense.bin")
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("restored content does not match the source")
	}

	if !allocationReportingSupported(t, outDir) {
		t.Skip("filesystem does not report block allocation")
	}
	blocks, ok := allocatedBytes(t, restored)
	if !ok {
		t.Fatal("expected block reporting to be available")
	}
	if blocks < size*9/10 {
		t.Errorf("restored dense zero file allocated %d bytes, want near %d", blocks, size)
	}
}
