package restore

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestRoundTripLargeSparseFile is the end-to-end sparse round trip:
// commit a 64 MiB file with two small writes far apart, build the run's
// NOAHSARK tree with image.Build, restore it, and check the bytes match
// and the restored file is sparse.
func TestRoundTripLargeSparseFile(t *testing.T) {
	srcDir := t.TempDir()
	const size = 64 << 20
	const chunkSize = 1 << 20
	const secondOffset = 40 << 20

	path := filepath.Join(srcDir, "big.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	first := make([]byte, chunkSize)
	rand.New(rand.NewSource(41)).Read(first)
	if _, err := f.WriteAt(first, 0); err != nil {
		t.Fatal(err)
	}
	second := make([]byte, chunkSize)
	rand.New(rand.NewSource(42)).Read(second)
	if _, err := f.WriteAt(second, secondOffset); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	want := make([]byte, size)
	copy(want[:chunkSize], first)
	copy(want[secondOffset:secondOffset+chunkSize], second)

	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	if err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatal(err)
	}

	restored := filepath.Join(outDir, srcDir, "big.bin")
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("restored content does not match the source")
	}

	if !allocationReportingSupported(t, outDir) {
		t.Skip("filesystem does not report block allocation")
	}
	blocks, ok := allocatedBytes(t, restored)
	if !ok {
		t.Fatal("expected block reporting to be available")
	}
	if blocks > size/2 {
		t.Errorf("restored file allocated %d bytes, want well below %d: not sparse", blocks, size)
	}
}
