package image

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestStagedTotals checks that StagedTotals counts every STAGED object
// once and sums their on-disk byte length, and that a pack drops
// everything it packed out of the total.
func TestStagedTotals(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}

	snapID := commitNamedFixture(t, stagingDir, "totals")
	markStagedFromCommit(t, stagingDir, snapID, l)

	objects, bytes, missing, err := StagedTotals(testObjectPath(stagingDir), l)
	if err != nil {
		t.Fatal(err)
	}
	if objects == 0 || bytes == 0 || missing != 0 {
		t.Fatalf("StagedTotals = %d objects, %d bytes, %d missing; want objects and bytes nonzero and none missing after a commit", objects, bytes, missing)
	}

	outDir := t.TempDir()
	opts := packOpts(stagingDir, snapID, outDir, sectorsFor(50_000_000), 1, l)
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	objects, bytes, missing, err = StagedTotals(testObjectPath(stagingDir), l)
	if err != nil {
		t.Fatal(err)
	}
	if objects != 0 || bytes != 0 || missing != 0 {
		t.Fatalf("StagedTotals after pack = %d objects, %d bytes, %d missing; want 0, 0, 0", objects, bytes, missing)
	}
}

// TestStagedTotalsCountsMissingFiles checks that a STAGED object with no
// file is counted as missing and is not an error, and that the totals
// hold only the objects whose file exists.
func TestStagedTotalsCountsMissingFiles(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	snapID := commitNamedFixture(t, stagingDir, "missing")
	markStagedFromCommit(t, stagingDir, snapID, l)
	allObjects, allBytes, _, err := StagedTotals(testObjectPath(stagingDir), l)
	if err != nil {
		t.Fatal(err)
	}

	chunks := filepath.Join(stagingDir, testKindDir(format.ObjectKindChunk))
	var removed int
	var removedBytes uint64
	err = filepath.WalkDir(chunks, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		removed++
		removedBytes += uint64(fi.Size())
		return os.Remove(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("the fixture has no chunk file to remove")
	}

	objects, bytes, missing, err := StagedTotals(testObjectPath(stagingDir), l)
	if err != nil {
		t.Fatalf("StagedTotals with missing chunk files: %v", err)
	}
	if missing != removed || objects != allObjects-removed || bytes != allBytes-removedBytes {
		t.Fatalf("StagedTotals = %d objects, %d bytes, %d missing; want %d, %d, %d",
			objects, bytes, missing, allObjects-removed, allBytes-removedBytes, removed)
	}
}
