package image

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestBuildFECOffWritesNoFECFiles checks that Build with FECEnabled
// false writes fec_scheme 0 into RUN.bin, and neither checksum.bin nor
// a parity directory, while Read still verifies the run by content id
// and file hash.
func TestBuildFECOffWritesNoFECFiles(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	opts.FECEnabled = false

	result, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.StripeCount != 0 {
		t.Fatalf("expected stripe count 0 with FEC off, got %d", result.StripeCount)
	}

	runDir := filepath.Join(outDir, "NOAHSARK", "runs", "0000000001")
	if _, err := os.Stat(filepath.Join(runDir, "checksum.bin")); !os.IsNotExist(err) {
		t.Fatalf("expected no checksum.bin, stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "parity")); !os.IsNotExist(err) {
		t.Fatalf("expected no parity directory, stat error: %v", err)
	}

	rr, err := Read(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if rr.Run.FECScheme != format.FECSchemeNone {
		t.Fatalf("expected fec_scheme 0, got %d", rr.Run.FECScheme)
	}
	if rr.Run.FECK != 0 || rr.Run.FECM != 0 {
		t.Fatalf("expected fec_k and fec_m 0 under fec_scheme 0, got k=%d m=%d", rr.Run.FECK, rr.Run.FECM)
	}
	// RUN.bin and RUN2.bin still exist and still match, even with FEC
	// off: the run header replication rule does not depend on FEC.
	if rr.RunCopies != 2 {
		t.Fatalf("expected 2 run header copies with FEC off, got %d", rr.RunCopies)
	}
	if rr.ObjectsVerified != result.ObjectCount {
		t.Fatalf("read verified %d objects, build wrote %d", rr.ObjectsVerified, result.ObjectCount)
	}
}

// TestBuildFECOnStillWritesFECFiles is the FEC-on control: RUN.bin
// carries fec_scheme 1, and Read's parity and checksum checks still run.
func TestBuildFECOnStillWritesFECFiles(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	opts.FECEnabled = true

	if _, err := Build(opts); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(outDir, "NOAHSARK", "runs", "0000000001")
	if _, err := os.Stat(filepath.Join(runDir, "checksum.bin")); err != nil {
		t.Fatalf("expected checksum.bin with FEC on: %v", err)
	}
	rr, err := Read(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if rr.Run.FECScheme != format.FECSchemeRS255GF8 {
		t.Fatalf("expected fec_scheme 1, got %d", rr.Run.FECScheme)
	}
}

// TestPackFECOff runs Pack with FECEnabled false and checks the same
// absence of FEC files and rows that Build gives.
func TestPackFECOff(t *testing.T) {
	stagingDir, snapID := packFixture(t)
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, l)

	outDir := t.TempDir()
	opts := packOpts(stagingDir, snapID, outDir, sectorsFor(64_000_000), 1, l)
	opts.FECEnabled = false
	result, err := Pack(opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.StripeCount != 0 {
		t.Fatalf("expected stripe count 0 with FEC off, got %d", result.StripeCount)
	}

	rr, err := Read(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if rr.Run.FECScheme != format.FECSchemeNone {
		t.Fatalf("expected fec_scheme 0, got %d", rr.Run.FECScheme)
	}
	for _, f := range rr.Index.Files {
		if f.Role == format.FileRoleChecksum || f.Role == format.FileRoleParity {
			t.Fatalf("expected no checksum or parity Files rows with FEC off, found role %d", f.Role)
		}
	}
}

// TestDataBudgetBlocksNoFEC checks the FEC-off budget rule directly:
// every usable sector after the filesystem overhead estimate, with no
// stripe rounding and no share given to a checksum column or parity.
func TestDataBudgetBlocksNoFEC(t *testing.T) {
	const target = 12_219_392 // bd25
	const fileCount = 200

	got := DataBudgetBlocksNoFEC(target, fileCount)
	want := usableSectors(target, fileCount)
	if got != want {
		t.Fatalf("DataBudgetBlocksNoFEC = %d, want %d", got, want)
	}

	// The no-FEC budget must be strictly larger than the FEC budget at
	// the same target and file count: no stripe rounding and no
	// checksum/parity share taken out of it.
	stripeWidth := 231 + 23 + 1
	fecBudget := DataBudgetBlocks(target, fileCount, 231, stripeWidth)
	if got <= fecBudget {
		t.Fatalf("no-FEC budget %d is not larger than the FEC budget %d", got, fecBudget)
	}
}

// TestDataBudgetBlocksNoFECTooSmall checks the zero-budget edge, matching
// DataBudgetBlocks's own rule for a target too small for the reserved
// sectors alone.
func TestDataBudgetBlocksNoFECTooSmall(t *testing.T) {
	if got := DataBudgetBlocksNoFEC(1, 200); got != 0 {
		t.Fatalf("expected a zero budget for a tiny target, got %d", got)
	}
}
