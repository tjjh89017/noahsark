package plan

import (
	"fmt"

	"github.com/tjjh89017/noahsark/internal/format"
)

// PassSplit is how a staging budget divides a Result's discs into read
// passes, OPERATIONS.md's "14.3 Staging budget". Only chunk objects
// count: a disc-swap restore resolves every tree and blob from the
// cache, and spools chunk payloads alone.
//
// This is a worst-case bound, not a tight one: a disc's chunk objects
// are read in plan order, which is not grouped by the file each chunk
// belongs to, so a real restore, which frees a file's chunks only once
// that whole file is complete, may hold several files' chunks at once
// before any of them frees. DiscPasses assumes nothing ever frees mid-
// disc, so a real restore never needs more passes than this predicts,
// though it usually needs fewer.
type PassSplit struct {
	// DiscPasses holds one entry per disc, aligned with the Result's
	// Discs slice, each at least 1.
	DiscPasses []int
	// Total is the most passes any one disc needs: 1 when the budget
	// forces no disc to split, OPERATIONS.md "14.4 The plan file"'s
	// passes field.
	Total int
	// PeakBytes is the largest single pass's chunk bytes across every
	// disc: the plan file's peak_staging_bytes field.
	PeakBytes uint64
}

// ComputePasses buckets each disc's chunk objects, in the order Build
// already placed them, into passes of at most budget bytes each. A
// budget of 0 means unlimited: every disc reads in one pass, and
// PeakBytes is the largest single disc's chunk bytes.
//
// It fails when one object alone exceeds budget: no split can place it.
func ComputePasses(discs []DiscEntry, budget uint64) (PassSplit, error) {
	var ps PassSplit
	ps.DiscPasses = make([]int, len(discs))
	ps.Total = 1
	for i, d := range discs {
		passes, peak, err := passesForDisc(d, budget)
		if err != nil {
			return PassSplit{}, err
		}
		ps.DiscPasses[i] = passes
		if passes > ps.Total {
			ps.Total = passes
		}
		if peak > ps.PeakBytes {
			ps.PeakBytes = peak
		}
	}
	return ps, nil
}

// passesForDisc sums d's chunk bytes and returns the worst-case pass
// count: ceil(total/budget), assuming nothing frees until the disc's
// whole read finishes. A real restore, freeing per file as it goes,
// never needs more passes than this, so the count printed before any
// disc is read always matches or exceeds the pass counter a restore
// prints while it runs.
func passesForDisc(d DiscEntry, budget uint64) (passes int, peak uint64, err error) {
	var total uint64
	for _, o := range d.Objects {
		if o.Kind != format.ObjectKindChunk {
			continue
		}
		if budget > 0 && o.Bytes > budget {
			return 0, 0, fmt.Errorf("object %s alone needs %d bytes, above the staging budget of %d bytes", o.ID.TextForm(), o.Bytes, budget)
		}
		total += o.Bytes
	}
	if budget == 0 || total == 0 {
		return 1, total, nil
	}
	passes = int((total + budget - 1) / budget)
	peak = min(budget, total)
	return passes, peak, nil
}
