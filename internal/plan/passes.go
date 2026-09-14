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
// This is a conservative estimate: it assumes no spool object is ever
// freed until its whole pass finishes, while a real restore frees a
// file's chunks as soon as that file is complete. A real restore may
// therefore need no more passes than this predicts, never more.
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

// passesForDisc greedily accumulates d's chunk objects, in their
// existing order, into passes of at most budget bytes, returning the
// pass count and the largest single pass's bytes.
func passesForDisc(d DiscEntry, budget uint64) (passes int, peak uint64, err error) {
	var cur uint64
	passes = 1
	for _, o := range d.Objects {
		if o.Kind != format.ObjectKindChunk {
			continue
		}
		if budget > 0 && o.Bytes > budget {
			return 0, 0, fmt.Errorf("object %s alone needs %d bytes, above the staging budget of %d bytes", o.ID.TextForm(), o.Bytes, budget)
		}
		if budget > 0 && cur > 0 && cur+o.Bytes > budget {
			passes++
			cur = 0
		}
		cur += o.Bytes
		if cur > peak {
			peak = cur
		}
	}
	return passes, peak, nil
}
