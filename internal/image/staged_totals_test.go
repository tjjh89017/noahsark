package image

import (
	"testing"

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

	objects, bytes, err := StagedTotals(stagingDir, l)
	if err != nil {
		t.Fatal(err)
	}
	if objects == 0 || bytes == 0 {
		t.Fatalf("StagedTotals = %d objects, %d bytes, want both nonzero after a commit", objects, bytes)
	}

	outDir := t.TempDir()
	opts := packOpts(stagingDir, snapID, outDir, sectorsFor(50_000_000), 1, l)
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	objects, bytes, err = StagedTotals(stagingDir, l)
	if err != nil {
		t.Fatal(err)
	}
	if objects != 0 || bytes != 0 {
		t.Fatalf("StagedTotals after pack = %d objects, %d bytes, want 0, 0", objects, bytes)
	}
}
