package image

import (
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestDryRunMatchesRealPackDiscCount checks that pack --dry-run
// predicts exactly the discs a loop of real packs then writes: the same
// disc numbers, the same object counts and the same byte counts.
func TestDryRunMatchesRealPackDiscCount(t *testing.T) {
	stagingDir, snapID := packFixture(t)

	dryLog, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, dryLog)

	capacitySectors := sectorsFor(7_000_000)
	dryOpts := packOpts(stagingDir, snapID, t.TempDir(), capacitySectors, 0, dryLog)
	discs, err := DryRun(dryOpts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(discs) < 2 {
		t.Fatalf("DryRun predicted %d disc(s), want at least 2 for this fixture and capacity", len(discs))
	}

	// DryRun must not have changed staging state: no object moved to
	// Packed, and no ledger row was written.
	if n := dryLog.CountState(stage.Packed); n != 0 {
		t.Fatalf("DryRun marked %d object(s) Packed, want 0", n)
	}
	if ledger, err := LoadDiscsLedger(stagingDir, dryOpts.RepoUUID); err != nil {
		t.Fatal(err)
	} else if len(ledger.Rows) != 0 {
		t.Fatalf("DryRun wrote %d disc ledger row(s), want 0", len(ledger.Rows))
	}

	// Now run the real thing and compare disc counts.
	realLog, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	discCount := 0
	for {
		opts := packOpts(stagingDir, snapID, filepath.Join(t.TempDir(), "tree"), capacitySectors, byte(discCount+1), realLog)
		result, err := Pack(opts)
		if err != nil {
			t.Fatalf("disc %d: Pack: %v", discCount, err)
		}
		if discCount >= len(discs) {
			t.Fatalf("real pack wrote disc %d, DryRun predicted %d disc(s) only", discCount, len(discs))
		}
		want := discs[discCount]
		if result.DiscSeq != want.DiscSeq || result.ObjectCount != want.ObjectCount || result.ObjectBytes != want.ObjectBytes {
			t.Fatalf("real pack wrote disc %d: %d objects, %d bytes; DryRun predicted disc %d: %d objects, %d bytes",
				result.DiscSeq, result.ObjectCount, result.ObjectBytes,
				want.DiscSeq, want.ObjectCount, want.ObjectBytes)
		}
		discCount++
		if result.RemainingObjects == 0 {
			break
		}
	}
	if discCount != len(discs) {
		t.Fatalf("real pack used %d disc(s), DryRun predicted %d", discCount, len(discs))
	}
}

// TestDryRunReturnsNoDiscsWhenNothingStaged checks that a repository
// with everything already packed reports zero predicted discs rather
// than an error, matching "how many more discs do I need".
func TestDryRunReturnsNoDiscsWhenNothingStaged(t *testing.T) {
	stagingDir, snapID := packFixture(t)
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, l)

	opts := packOpts(stagingDir, snapID, filepath.Join(t.TempDir(), "tree"), sectorsFor(64<<20), 1, l)
	if _, err := Pack(opts); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	discs, err := DryRun(packOpts(stagingDir, snapID, "", sectorsFor(64<<20), 0, l), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(discs) != 0 {
		t.Fatalf("DryRun predicted %d disc(s) with nothing staged, want 0", len(discs))
	}
}
