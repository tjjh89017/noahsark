package image

import (
	"testing"

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
