package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// buildFixtureTreeNoFEC is buildFixtureTree with FECEnabled false: a
// run that carries no checksum column and no parity for Heal to refuse.
func buildFixtureTreeNoFEC(t *testing.T, srcDir string) (treeDir string, snapID object.ID) {
	t.Helper()
	stagingDir := t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	treeDir = t.TempDir()
	opts := image.BuildOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "LATEST", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors:   1 << 21,
		PhysicalCapacitySectors: 1 << 21,
		OutputDir:               treeDir,
		RepoUUID:                [16]byte{1, 2, 3, 4},
		DiscUUID:                [16]byte{5, 6, 7, 8},
		Label:                   "restore-nofec-test",
		FECEnabled:              false,
		Now:                     fixedClock,
	}
	if _, err := image.Build(opts); err != nil {
		t.Fatal(err)
	}
	return treeDir, snapID
}

// TestHealRefusesRunWithNoFEC checks that Heal on a scheme 0 run (no
// FEC) fails clearly, naming the run, and touches nothing: there is no
// checksum column or parity to repair from.
func TestHealRefusesRunWithNoFEC(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	treeDir, _ := buildFixtureTreeNoFEC(t, srcDir)

	runDir := filepath.Join(treeDir, "NOAHSARK", "runs", "0000000001")
	if _, err := os.Stat(filepath.Join(runDir, "checksum.bin")); !os.IsNotExist(err) {
		t.Fatalf("expected no checksum.bin, stat error: %v", err)
	}

	_, err := Heal(treeDir, "")
	if err == nil {
		t.Fatal("expected Heal to refuse a run with no FEC")
	}
	if !strings.Contains(err.Error(), "no FEC") {
		t.Fatalf("expected the error to say the run has no FEC, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1") {
		t.Fatalf("expected the error to name the run seq, got: %v", err)
	}
}
