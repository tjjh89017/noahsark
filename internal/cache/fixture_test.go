package cache_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func fixedClock() time.Time {
	return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
}

// buildFixtureRun commits a small source tree, packs it into a fresh
// run directory with plenty of capacity, and returns the run root
// (ready for cache.WriteFromRoot or image.Read) and the snapshot id.
func buildFixtureRun(t *testing.T) (runRoot string, snapID object.ID) {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("content of b, a bit longer so it is worth its own chunk"), 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir := t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	reachable, err := image.CollectReachable(stagingDir, []object.ID{snapID})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range reachable {
		if err := l.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}

	const capacityBytes = 50_000_000
	capacitySectors := (uint64(capacityBytes) + image.SectorSize - 1) / image.SectorSize

	outDir := filepath.Join(t.TempDir(), "run")
	opts := image.PackOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors:   capacitySectors,
		PhysicalCapacitySectors: capacitySectors,
		OutputDir:               outDir,
		RepoUUID:                [16]byte{1, 2, 3, 4},
		DiscUUID:                [16]byte{5, 6, 7, 8},
		Label:                   "test-disc",
		Now:                     fixedClock,
		StageLog:                l,
	}
	if _, err := image.Pack(opts); err != nil {
		t.Fatal(err)
	}
	return outDir, snapID
}
