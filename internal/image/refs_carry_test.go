package image

import (
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestPackCarriesRefsForward packs two different refs onto two
// different discs, one ref per pack. FORMAT.md requires REFS to be
// replicated in full on every run, so the second disc's REFS must name
// both refs, the first one still carrying the run_seq of the run that
// packed it.
func TestPackCarriesRefsForward(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}

	firstSnap := commitNamedFixture(t, stagingDir, "first")
	markStagedFromCommit(t, stagingDir, firstSnap, l)

	firstOut := t.TempDir()
	firstOpts := PackOptions{
		StagingDir: stagingDir, Snapshots: []SnapshotRef{{Name: "run1", ID: firstSnap, Time: fixedClock()}},
		TargetCapacitySectors: sectorsFor(50_000_000), PhysicalCapacitySectors: sectorsFor(50_000_000),
		OutputDir: firstOut, RepoUUID: [16]byte{1, 2, 3, 4}, DiscUUID: [16]byte{1}, Label: "disc-1",
		Now: fixedClock, StageLog: l,
	}
	if _, err := Pack(firstOpts); err != nil {
		t.Fatalf("first pack: %v", err)
	}

	secondSnap := commitNamedFixture(t, stagingDir, "second")
	markStagedFromCommit(t, stagingDir, secondSnap, l)

	secondOut := t.TempDir()
	secondOpts := PackOptions{
		StagingDir: stagingDir, Snapshots: []SnapshotRef{{Name: "run2", ID: secondSnap, Time: fixedClock()}},
		TargetCapacitySectors: sectorsFor(50_000_000), PhysicalCapacitySectors: sectorsFor(50_000_000),
		OutputDir: secondOut, RepoUUID: [16]byte{1, 2, 3, 4}, DiscUUID: [16]byte{2}, Label: "disc-2",
		Now: fixedClock, StageLog: l,
	}
	if _, err := Pack(secondOpts); err != nil {
		t.Fatalf("second pack: %v", err)
	}

	rr, err := Read(secondOut)
	if err != nil {
		t.Fatalf("Read disc 2: %v", err)
	}
	if len(rr.Refs.Records) != 2 {
		t.Fatalf("disc 2 REFS has %d records, want 2 (run1 and run2)", len(rr.Refs.Records))
	}
	byName := make(map[string]bool)
	for _, r := range rr.Refs.Records {
		byName[string(r.Name[:r.NameLen])] = true
	}
	if !byName["run1"] {
		t.Fatal("run1 was not carried into disc 2's REFS")
	}
	if !byName["run2"] {
		t.Fatal("run2 is missing from disc 2's REFS")
	}

	// The local refs ledger on disk must hold the same union, so a
	// third pack would carry both refs forward again.
	ledger, err := LoadRefsLedger(stagingDir, [16]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("LoadRefsLedger: %v", err)
	}
	if len(ledger.Records) != 2 {
		t.Fatalf("refs ledger has %d records, want 2", len(ledger.Records))
	}
}
