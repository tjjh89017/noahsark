package image

import (
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// TestSelectRunManySmallObjects gives selectRun more tiny objects than
// one disc holds, so that the filesystem overhead of the files, not
// their bytes, fills the disc. selectRun must place a prefix that fits
// with the overhead of its own file count, and the next object must not
// fit.
func TestSelectRunManySmallObjects(t *testing.T) {
	const target = 20_000 // sectors, about 40 MB
	candidates := make([]packUnit, 30_000)
	for i := range candidates {
		var id object.ID
		id[0], id[1], id[2] = byte(i>>16), byte(i>>8), byte(i)
		candidates[i] = packUnit{ID: id, Kind: format.ObjectKindChunk, ByteLen: 68}
	}
	opts := PackOptions{TargetCapacitySectors: target}
	const fixedSectors, fixedFiles = 64, 7

	selected, _, err := selectRun(opts, candidates, fixedSectors, fixedFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) == 0 || len(selected) == len(candidates) {
		t.Fatalf("selectRun placed %d of %d objects, want some but not all", len(selected), len(candidates))
	}
	fits := func(n int) bool {
		files := fixedFiles + n + run2RowCount
		indexLen := format.IndexHeaderLen + files*format.IndexFileRecordLen + n*format.IndexObjectRecordLen
		return fixedSectors+sectorCount(uint64(indexLen))+uint64(n) <= DataBudgetSectors(target, files)
	}
	if n := len(selected); !fits(n) || fits(n+1) {
		t.Fatalf("selectRun placed %d objects: fits %v, one more fits %v; want true, false", n, fits(n), fits(n+1))
	}
}
