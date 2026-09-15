package restore

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/fec"
)

// TestHealRepairsCorruptedStripes corrupts up to m blocks in each of
// several stripes, spanning data columns (object files and the
// INDEX-listed fixed files early in the stream) and one parity column
// per stripe, then heals and restores, and compares against the source.
func TestHealRepairsCorruptedStripes(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	stagingDir, treeDir, snapID := buildFixtureTree(t, srcDir)

	paths, sizes, layout := streamLayout(t, treeDir)
	if layout.StripeCount() < 3 {
		t.Fatalf("fixture too small: only %d stripe(s), want at least 3", layout.StripeCount())
	}

	// Stripe 0: corrupt five low-numbered data columns, spanning the
	// INDEX-listed fixed files (FORMAT.txt, decoder.py, REFS.bin) and an
	// object file, plus one parity column. Column 0 is INDEX.bin itself
	// and is left alone: resolving the stream layout needs a readable
	// INDEX.bin, so healing INDEX.bin's own bytes is out of scope here.
	for _, col := range []uint64{1, 2, 3, 6, 7} {
		corruptDataBlockAt(t, paths, sizes, layout, col, 0)
	}
	corruptParityBlock(t, treeDir, 0, 0)

	// Stripe 2: corrupt a different, disjoint set of columns.
	for _, col := range []uint64{8, 9, 10} {
		corruptDataBlockAt(t, paths, sizes, layout, col, 2)
	}
	corruptParityBlock(t, treeDir, 1, 2)

	reports, err := Heal(treeDir, "")
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	touched := map[uint64]bool{}
	for _, r := range reports {
		touched[r.Stripe] = true
	}
	if !touched[0] || !touched[2] {
		t.Fatalf("expected Heal to report stripes 0 and 2, got %+v", reports)
	}

	if err := os.RemoveAll(stagingDir); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if _, _, err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatalf("Restore after Heal: %v", err)
	}
	compareRestoredTree(t, srcDir, outDir)
}

// TestHealFailsOnTooManyErasures corrupts m+1 data blocks in the last
// stripe and checks that Heal names that stripe, returns an error, and
// leaves every earlier stripe healed and untouched by the failure.
func TestHealFailsOnTooManyErasures(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, _ := buildFixtureTree(t, srcDir)

	paths, sizes, layout := streamLayout(t, treeDir)
	if layout.StripeCount() < 2 {
		t.Fatalf("fixture too small: only %d stripe(s), want at least 2", layout.StripeCount())
	}
	badStripe := layout.StripeCount() - 1

	// A decodable corruption on an earlier stripe, to prove Heal still
	// repairs the stripes before the undecodable one. Column 0 is
	// INDEX.bin itself and is left alone throughout, since resolving the
	// stream layout needs a readable INDEX.bin.
	corruptDataBlockAt(t, paths, sizes, layout, 1, 0)

	// The last stripe: corrupt m+1 = 24 data columns, undecodable.
	for col := uint64(1); col < uint64(fec.M+2); col++ {
		corruptDataBlockAt(t, paths, sizes, layout, col, badStripe)
	}

	reports, err := Heal(treeDir, "")
	if err == nil {
		t.Fatal("expected Heal to fail on a stripe with more than m bad blocks")
	}
	wantMsg := "stripe " + strconv.FormatUint(badStripe, 10)
	if !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("expected the error to name %q, got: %v", wantMsg, err)
	}
	found := false
	for _, r := range reports {
		if r.Stripe == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stripe 0 to be healed before the failure, got %+v", reports)
	}
}
