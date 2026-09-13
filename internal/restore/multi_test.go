package restore

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func multiFixedClock() time.Time {
	return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
}

// commitMultiFixture commits a source tree of several files, each in its
// own subdirectory so a run over a small forced capacity can finish some
// files' subtrees without finishing the root.
func commitMultiFixture(t *testing.T) (stagingDir, srcDir string, snapID object.ID) {
	t.Helper()
	srcDir = t.TempDir()
	rng := rand.New(rand.NewSource(7))
	for i := range 6 {
		dir := filepath.Join(srcDir, fmt.Sprintf("sub%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 600_000)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stagingDir = t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = multiFixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	return stagingDir, srcDir, snapID
}

// packSequence packs one disc per entry of capacitiesBytes, in order,
// against the same staging directory and state log, and returns every
// disc's output root.
func packSequence(t *testing.T, stagingDir string, snapID object.ID, capacitiesBytes []uint64) []string {
	t.Helper()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	objs, err := image.CollectReachable(stagingDir, []object.ID{snapID})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if err := l.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}

	var roots []string
	for i, capBytes := range capacitiesBytes {
		outDir := t.TempDir()
		sectors := (capBytes + image.SectorSize - 1) / image.SectorSize
		opts := image.PackOptions{
			StagingDir:              stagingDir,
			Snapshots:               []image.SnapshotRef{{Name: "LATEST", ID: snapID, Time: multiFixedClock()}},
			TargetCapacitySectors:   sectors,
			PhysicalCapacitySectors: sectors,
			OutputDir:               outDir,
			RepoUUID:                [16]byte{9, 9, 9},
			DiscUUID:                [16]byte{byte(i + 1)},
			Label:                   fmt.Sprintf("disc-%d", i),
			FECEnabled:              true,
			Now:                     multiFixedClock,
			StageLog:                l,
		}
		if _, err := image.Pack(opts); err != nil {
			t.Fatalf("pack %d: %v", i, err)
		}
		roots = append(roots, outDir)
	}
	return roots
}

// compareTrees fails the test unless every regular file under want has
// byte-identical content under got.
func compareTrees(t *testing.T, want, got string) {
	t.Helper()
	err := filepath.Walk(want, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(want, path)
		if err != nil {
			return err
		}
		wantData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		gotData, err := os.ReadFile(filepath.Join(got, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		if !bytes.Equal(wantData, gotData) {
			t.Errorf("%s: content differs after restore", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreMultiAcrossThreeDiscs(t *testing.T) {
	stagingDir, srcDir, snapID := commitMultiFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{7_000_000, 7_000_000, 10_000_000})

	outDir := t.TempDir()
	if err := RestoreMulti(roots, snapID, outDir); err != nil {
		t.Fatalf("RestoreMulti: %v", err)
	}
	compareTrees(t, srcDir, filepath.Join(outDir, srcDir))
}

func TestRestoreMultiCapacityOrderDoesNotMatterForResult(t *testing.T) {
	stagingDir, srcDir, snapID := commitMultiFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{10_000_000, 7_000_000, 7_000_000})

	outDir := t.TempDir()
	if err := RestoreMulti(roots, snapID, outDir); err != nil {
		t.Fatalf("RestoreMulti: %v", err)
	}
	compareTrees(t, srcDir, filepath.Join(outDir, srcDir))
}

func TestRestoreMultiMissingDiscNamesIt(t *testing.T) {
	stagingDir, _, snapID := commitMultiFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{7_000_000, 7_000_000, 10_000_000})

	// Drop the middle disc: restore from the other two only.
	partial := []string{roots[0], roots[2]}

	outDir := t.TempDir()
	err := RestoreMulti(partial, snapID, outDir)
	if err == nil {
		t.Fatal("expected a missing-disc error")
	}
	missing, ok := err.(*MissingDiscError)
	if !ok {
		t.Fatalf("expected *MissingDiscError, got %T: %v", err, err)
	}
	if len(missing.ByDisc) == 0 {
		t.Fatal("expected at least one named missing disc")
	}
	t.Logf("missing disc error: %v", missing)
}
