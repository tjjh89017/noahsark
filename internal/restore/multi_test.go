package restore

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
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

// TestRestoreMultiUnnamedMissingListsDiscsTableCandidate covers the case
// where the missing object is not named by any provided disc's Prereqs
// row, because no provided disc's snapshot ever referenced it. The only
// clue left is the DISCS table: disc 2 lists disc 1, even though disc
// 2's own INDEX never mentions disc 1's objects. Restoring from disc 2
// alone must still name disc 1 as a candidate.
func TestRestoreMultiUnnamedMissingListsDiscsTableCandidate(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}

	// Two unrelated commits into the same staging store: disc 1's
	// snapshot and disc 2's snapshot share no objects.
	firstSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstSrc, "a.bin"), bytes.Repeat([]byte{1}, 500_000), 0o644); err != nil {
		t.Fatal(err)
	}
	w1 := object.NewWriter(stagingDir)
	w1.Now = multiFixedClock
	snap1, _, err := w1.Commit(firstSrc)
	if err != nil {
		t.Fatal(err)
	}
	objs1, err := image.CollectReachable(stagingDir, []object.ID{snap1})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs1 {
		if err := l.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}
	disc1Dir := t.TempDir()
	disc1Sectors := (uint64(10_000_000) + image.SectorSize - 1) / image.SectorSize
	if _, err := image.Pack(image.PackOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "ONE", ID: snap1, Time: multiFixedClock()}},
		TargetCapacitySectors:   disc1Sectors,
		PhysicalCapacitySectors: disc1Sectors,
		OutputDir:               disc1Dir,
		RepoUUID:                [16]byte{9, 9, 9},
		DiscUUID:                [16]byte{1},
		Label:                   "disc-one",
		FECEnabled:              true,
		Now:                     multiFixedClock,
		StageLog:                l,
	}); err != nil {
		t.Fatalf("pack disc 1: %v", err)
	}

	secondSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(secondSrc, "b.bin"), bytes.Repeat([]byte{2}, 500_000), 0o644); err != nil {
		t.Fatal(err)
	}
	w2 := object.NewWriter(stagingDir)
	w2.Now = multiFixedClock
	snap2, _, err := w2.Commit(secondSrc)
	if err != nil {
		t.Fatal(err)
	}
	objs2, err := image.CollectReachable(stagingDir, []object.ID{snap2})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs2 {
		if err := l.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}
	disc2Dir := t.TempDir()
	disc2Sectors := (uint64(10_000_000) + image.SectorSize - 1) / image.SectorSize
	if _, err := image.Pack(image.PackOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "TWO", ID: snap2, Time: multiFixedClock()}},
		TargetCapacitySectors:   disc2Sectors,
		PhysicalCapacitySectors: disc2Sectors,
		OutputDir:               disc2Dir,
		RepoUUID:                [16]byte{9, 9, 9},
		DiscUUID:                [16]byte{2},
		Label:                   "disc-two",
		FECEnabled:              true,
		Now:                     multiFixedClock,
		StageLog:                l,
	}); err != nil {
		t.Fatalf("pack disc 2: %v", err)
	}

	// Restore snap1 (disc 1's snapshot) from disc 2 alone. Disc 2's
	// INDEX and Prereqs never reference snap1's objects, so the only
	// way to name disc 1 is disc 2's DISCS table.
	outDir := t.TempDir()
	err = RestoreMulti([]string{disc2Dir}, snap1, outDir)
	if err == nil {
		t.Fatal("expected a missing-disc error")
	}
	missing, ok := err.(*MissingDiscError)
	if !ok {
		t.Fatalf("expected *MissingDiscError, got %T: %v", err, err)
	}
	if len(missing.ByDisc) != 0 {
		t.Fatalf("expected no Prereqs-named disc, got ByDisc=%v", missing.ByDisc)
	}
	if missing.UnnamedCount == 0 {
		t.Fatal("expected a nonzero UnnamedCount")
	}
	found := false
	for _, c := range missing.Candidates {
		if c.UUID == ([16]byte{1}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected disc 1's uuid among candidates, got %v", missing.Candidates)
	}
	if !strings.Contains(missing.Error(), uuidText([16]byte{1})) {
		t.Fatalf("expected the error text to name disc 1, got %q", missing.Error())
	}
}
