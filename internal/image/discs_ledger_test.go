package image

import (
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestPackRecordsUsedSectorsInTheLedger checks that the local disc
// ledger's row for a packed run carries the run's real stream size,
// not the always-zero placeholder the on-disc DISCS row still carries
// for itself until the next run's copy fills it in.
func TestPackRecordsUsedSectorsInTheLedger(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	snapID := commitNamedFixture(t, stagingDir, "ledger")
	markStagedFromCommit(t, stagingDir, snapID, l)

	outDir := t.TempDir()
	opts := packOpts(stagingDir, snapID, outDir, sectorsFor(50_000_000), 1, l)
	result, err := Pack(opts)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	ledger, err := LoadDiscsLedger(stagingDir, opts.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Rows) != 1 {
		t.Fatalf("ledger has %d rows, want 1", len(ledger.Rows))
	}
	if ledger.Rows[0].UsedSectors != result.StreamBlocks {
		t.Fatalf("ledger UsedSectors = %d, want %d (result.StreamBlocks)", ledger.Rows[0].UsedSectors, result.StreamBlocks)
	}
	if ledger.Rows[0].UsedSectors == 0 {
		t.Fatal("ledger UsedSectors = 0, want the run's actual stream size")
	}
}

// TestNextSeqNumbersWithGap stands in for a ledger rebuild-cache
// rebuilt from surviving discs after the newest disc's own row never
// reached any fed disc's DISCS table: the ledger holds rows 0 and 1
// but not the lost row 2, so it has 2 rows while the highest disc_seq
// seen is 1. Counting rows would hand out disc_seq 2 again, colliding
// with the lost disc; NextSeqNumbers must instead continue from the
// highest number the ledger holds.
func TestNextSeqNumbersWithGap(t *testing.T) {
	rows := []format.DiscsRow{
		{RunSeq: 1, DiscSeq: 0},
		{RunSeq: 4, DiscSeq: 3},
	}
	runSeq, discSeq := NextSeqNumbers(rows)
	if runSeq != 5 {
		t.Fatalf("runSeq = %d, want 5 (max run_seq 4, plus 1)", runSeq)
	}
	if discSeq != 4 {
		t.Fatalf("discSeq = %d, want 4 (max disc_seq 3, plus 1)", discSeq)
	}
}

// TestNextSeqNumbersEmptyLedger checks the fresh-repository case stays
// exactly as before: run_seq 1, disc_seq 0.
func TestNextSeqNumbersEmptyLedger(t *testing.T) {
	runSeq, discSeq := NextSeqNumbers(nil)
	if runSeq != 1 || discSeq != 0 {
		t.Fatalf("NextSeqNumbers(nil) = (%d, %d), want (1, 0)", runSeq, discSeq)
	}
}

// TestPackAfterLedgerGapAvoidsReuse packs onto a ledger that already
// has a gap (as a partial rebuild-cache could leave it) and checks the
// new run's numbers continue past the highest one the ledger holds,
// not past its row count.
func TestPackAfterLedgerGapAvoidsReuse(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	gapRows := []format.DiscsRow{
		{RunSeq: 1, DiscSeq: 0, DiscUUID: [16]byte{1}, CapacitySectors: 1000},
		{RunSeq: 4, DiscSeq: 3, DiscUUID: [16]byte{2}, CapacitySectors: 1000},
	}
	if err := SaveDiscsLedger(stagingDir, [16]byte{1, 2, 3, 4}, gapRows); err != nil {
		t.Fatal(err)
	}

	snapID := commitNamedFixture(t, stagingDir, "gap")
	markStagedFromCommit(t, stagingDir, snapID, l)

	outDir := t.TempDir()
	opts := packOpts(stagingDir, snapID, outDir, sectorsFor(50_000_000), 5, l)
	result, err := Pack(opts)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if result.RunSeq != 5 {
		t.Fatalf("RunSeq = %d, want 5 (past the ledger's max run_seq 4)", result.RunSeq)
	}
	if result.DiscSeq != 4 {
		t.Fatalf("DiscSeq = %d, want 4 (past the ledger's max disc_seq 3)", result.DiscSeq)
	}
}
