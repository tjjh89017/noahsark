package plan

import (
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// chunkEntry builds one chunk ObjectEntry of n bytes, with a distinct id
// so ComputePasses' per-object error message can name it.
func chunkEntry(t *testing.T, seed byte, n uint64) ObjectEntry {
	t.Helper()
	return ObjectEntry{ID: object.ComputeID([]byte{seed}), Kind: format.ObjectKindChunk, Bytes: n}
}

// TestComputePassesUnlimitedBudget checks a zero budget never splits,
// and PeakBytes is the largest single disc's chunk total.
func TestComputePassesUnlimitedBudget(t *testing.T) {
	discs := []DiscEntry{
		{Objects: []ObjectEntry{chunkEntry(t, 1, 100), chunkEntry(t, 2, 50)}},
		{Objects: []ObjectEntry{chunkEntry(t, 3, 10)}},
	}
	ps, err := ComputePasses(discs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Total != 1 {
		t.Fatalf("Total = %d, want 1 (no disc splits)", ps.Total)
	}
	if ps.PeakBytes != 150 {
		t.Fatalf("PeakBytes = %d, want 150", ps.PeakBytes)
	}
}

// TestComputePassesSplitsOverBudget checks a disc whose chunk bytes
// exceed the budget splits into more than one pass, and the total
// reflects every disc's own pass count.
func TestComputePassesSplitsOverBudget(t *testing.T) {
	discs := []DiscEntry{
		{Objects: []ObjectEntry{chunkEntry(t, 1, 40), chunkEntry(t, 2, 40), chunkEntry(t, 3, 40)}},
		{Objects: []ObjectEntry{chunkEntry(t, 4, 10)}},
	}
	ps, err := ComputePasses(discs, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.DiscPasses) != 2 || ps.DiscPasses[0] < 2 {
		t.Fatalf("DiscPasses = %v, want disc 0 split into at least 2 passes", ps.DiscPasses)
	}
	if ps.DiscPasses[1] != 1 {
		t.Fatalf("DiscPasses[1] = %d, want 1", ps.DiscPasses[1])
	}
	if ps.Total != ps.DiscPasses[0] {
		t.Fatalf("Total = %d, want the largest per-disc pass count %d", ps.Total, ps.DiscPasses[0])
	}
	if ps.PeakBytes > 50 {
		t.Fatalf("PeakBytes = %d, want no pass over the 50-byte budget", ps.PeakBytes)
	}
}

// TestComputePassesRefusesOversizeObject checks one object alone above
// the budget is refused, naming the object, rather than silently
// producing a plan no restore could honour.
func TestComputePassesRefusesOversizeObject(t *testing.T) {
	discs := []DiscEntry{
		{Objects: []ObjectEntry{chunkEntry(t, 1, 200)}},
	}
	_, err := ComputePasses(discs, 50)
	if err == nil {
		t.Fatal("ComputePasses: no error, want a refusal for the oversize object")
	}
}
