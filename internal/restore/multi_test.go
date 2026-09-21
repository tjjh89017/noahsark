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
			StagingDir:            stagingDir,
			Snapshots:             []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: multiFixedClock()}},
			TargetCapacitySectors: sectors,
			OutputDir:             outDir,
			RepoUUID:              [16]byte{9, 9, 9},
			DiscUUID:              [16]byte{byte(i + 1)},
			Label:                 fmt.Sprintf("disc-%d", i),
			FECEnabled:            true,
			Now:                   multiFixedClock,
			StageLog:              l,
		}
		if _, err := image.Pack(opts); err != nil {
			// A capacity large enough to finish every remaining object
			// in one run leaves nothing for a later, smaller capacity
			// in the same sequence to pack. That is a valid outcome
			// for a fixture this small, not a test failure: stop
			// packing early and let the caller restore from whatever
			// discs exist so far.
			if strings.Contains(err.Error(), "nothing to pack") {
				break
			}
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
	if _, err := RestoreMulti(roots, snapID, outDir); err != nil {
		t.Fatalf("RestoreMulti: %v", err)
	}
	compareTrees(t, srcDir, filepath.Join(outDir, srcDir))
}

func TestRestoreMultiCapacityOrderDoesNotMatterForResult(t *testing.T) {
	stagingDir, srcDir, snapID := commitMultiFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{10_000_000, 7_000_000, 7_000_000})

	outDir := t.TempDir()
	if _, err := RestoreMulti(roots, snapID, outDir); err != nil {
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
	_, err := RestoreMulti(partial, snapID, outDir)
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

// packOneDisc commits src into stagingDir and packs whatever that commit
// left unpacked onto one new disc.
func packOneDisc(t *testing.T, stagingDir string, l *stage.Log, src string, repoUUID, discUUID [16]byte, name, label string) (object.ID, string) {
	t.Helper()
	w := object.NewWriter(stagingDir)
	w.Now = multiFixedClock
	snap, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	objs, err := image.CollectReachable(stagingDir, []object.ID{snap})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if err := l.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	sectors := (uint64(20_000_000) + image.SectorSize - 1) / image.SectorSize
	if _, err := image.Pack(image.PackOptions{
		StagingDir:            stagingDir,
		Snapshots:             []image.SnapshotRef{{Name: name, ID: snap, Time: multiFixedClock()}},
		TargetCapacitySectors: sectors,
		OutputDir:             dir,
		RepoUUID:              repoUUID,
		DiscUUID:              discUUID,
		Label:                 label,
		FECEnabled:            true,
		Now:                   multiFixedClock,
		StageLog:              l,
	}); err != nil {
		t.Fatalf("pack %s: %v", label, err)
	}
	return snap, dir
}

// TestRestoreMultiRunSeqRepeatedAcrossLineages gives the restore two
// discs of different lineages that both carry run sequence number 1.
// The prerequisite must resolve through the DISCS table of the disc that
// wrote the Prereqs row, so the missing object is reported against the
// disc that really holds it, not against the unrelated disc that reuses
// the same number.
func TestRestoreMultiRunSeqRepeatedAcrossLineages(t *testing.T) {
	writeRandom := func(dir, name string, seed int64) {
		t.Helper()
		data := make([]byte, 400_000)
		if _, err := rand.New(rand.NewSource(seed)).Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Lineage A: disc 1 holds the first commit, disc 2 holds only what
	// the second commit added. Disc 2's Prereqs name run 1 of lineage A.
	stagingA := t.TempDir()
	logA, err := stage.Open(stagingA)
	if err != nil {
		t.Fatal(err)
	}
	repoA := [16]byte{9, 9, 9}
	srcA := t.TempDir()
	writeRandom(srcA, "first.bin", 11)
	_, disc1Dir := packOneDisc(t, stagingA, logA, srcA, repoA, [16]byte{1}, "A1", "a-one")
	writeRandom(srcA, "second.bin", 12)
	snapA2, disc2Dir := packOneDisc(t, stagingA, logA, srcA, repoA, [16]byte{2}, "A2", "a-two")

	// Lineage B: a separate repository. Its only disc is run 1 as well.
	stagingB := t.TempDir()
	logB, err := stage.Open(stagingB)
	if err != nil {
		t.Fatal(err)
	}
	srcB := t.TempDir()
	writeRandom(srcB, "other.bin", 21)
	_, disc3Dir := packOneDisc(t, stagingB, logB, srcB, [16]byte{8, 8, 8}, [16]byte{3}, "B1", "b-one")

	// Disc 1 is not provided. Lineage B's disc must not be mistaken for
	// it, although both discs hold a run 1.
	outDir := t.TempDir()
	_, err = RestoreMulti([]string{disc2Dir, disc3Dir}, snapA2, outDir)
	if err == nil {
		t.Fatal("expected a missing-disc error")
	}
	missing, ok := err.(*MissingDiscError)
	if !ok {
		t.Fatalf("expected *MissingDiscError, got %T: %v", err, err)
	}
	if _, ok := missing.ByDisc[[16]byte{1}]; !ok {
		t.Fatalf("expected disc 1 to be named, got ByDisc=%v", missing.ByDisc)
	}
	if _, ok := missing.ByDisc[[16]byte{3}]; ok {
		t.Fatalf("lineage B's disc must not be named, got ByDisc=%v", missing.ByDisc)
	}
	if !strings.Contains(missing.Error(), uuidText([16]byte{1})) || !strings.Contains(missing.Error(), "a-one") {
		t.Fatalf("expected the error to name disc 1 and its label, got %q", missing.Error())
	}

	// With disc 1 back, the restore finds every object on the right
	// disc, although lineage B's disc is still in the set.
	outDir = t.TempDir()
	if _, err := RestoreMulti([]string{disc1Dir, disc2Dir, disc3Dir}, snapA2, outDir); err != nil {
		t.Fatalf("RestoreMulti: %v", err)
	}
	compareTrees(t, srcA, filepath.Join(outDir, srcA))
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
		StagingDir:            stagingDir,
		Snapshots:             []image.SnapshotRef{{Name: "ONE", ID: snap1, Time: multiFixedClock()}},
		TargetCapacitySectors: disc1Sectors,
		OutputDir:             disc1Dir,
		RepoUUID:              [16]byte{9, 9, 9},
		DiscUUID:              [16]byte{1},
		Label:                 "disc-one",
		FECEnabled:            true,
		Now:                   multiFixedClock,
		StageLog:              l,
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
		StagingDir:            stagingDir,
		Snapshots:             []image.SnapshotRef{{Name: "TWO", ID: snap2, Time: multiFixedClock()}},
		TargetCapacitySectors: disc2Sectors,
		OutputDir:             disc2Dir,
		RepoUUID:              [16]byte{9, 9, 9},
		DiscUUID:              [16]byte{2},
		Label:                 "disc-two",
		FECEnabled:            true,
		Now:                   multiFixedClock,
		StageLog:              l,
	}); err != nil {
		t.Fatalf("pack disc 2: %v", err)
	}

	// Restore snap1 (disc 1's snapshot) from disc 2 alone. Disc 2's
	// INDEX and Prereqs never reference snap1's objects, so the only
	// way to name disc 1 is disc 2's DISCS table.
	outDir := t.TempDir()
	_, err = RestoreMulti([]string{disc2Dir}, snap1, outDir)
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
	if !missing.RootTreeMissing {
		t.Fatal("expected RootTreeMissing: snap1's root tree is not on disc 2")
	}
	if !strings.Contains(missing.Error(), "the snapshot's root tree is not on the provided disc(s)") {
		t.Fatalf("expected the root-tree wording, got %q", missing.Error())
	}
}

// TestMissingDiscErrorCandidatesOnePerLine checks that a
// *MissingDiscError with more than one candidate disc lists them one
// per line, indented, the same shape the "missing disc(s):" case uses.
func TestMissingDiscErrorCandidatesOnePerLine(t *testing.T) {
	e := &MissingDiscError{
		UnnamedCount: 3,
		Candidates: []DiscCandidate{
			{UUID: [16]byte{1}, Name: DiscName{Seq: 1, Label: "disc-one"}},
			{UUID: [16]byte{2}, Name: DiscName{Seq: 2, Label: "disc-two"}},
		},
	}
	want := fmt.Sprintf(
		"3 object(s) not found on any provided disc and named by no provided disc's INDEX; disc(s) not provided, that may hold them:\n  disc 1 \"disc-one\" (%s)\n  disc 2 \"disc-two\" (%s)",
		uuidText([16]byte{1}), uuidText([16]byte{2}))
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// TestRestoreMultiKnownDiscsCandidateBothDirections restores from the
// middle disc of a three-disc chain, with WithKnownDiscs naming both the
// earlier and the later disc. Disc 2's own DISCS table only ever names
// disc 1, since disc 3 did not exist yet when disc 2 was packed, so
// disc 3 can only reach the candidate list through WithKnownDiscs.
func TestRestoreMultiKnownDiscsCandidateBothDirections(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}

	commitAndPack := func(fill byte, discUUID byte, name, label string) (object.ID, string) {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "f.bin"), bytes.Repeat([]byte{fill}, 500_000), 0o644); err != nil {
			t.Fatal(err)
		}
		w := object.NewWriter(stagingDir)
		w.Now = multiFixedClock
		snap, _, err := w.Commit(src)
		if err != nil {
			t.Fatal(err)
		}
		objs, err := image.CollectReachable(stagingDir, []object.ID{snap})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range objs {
			if err := l.EnsureStaged(o.ID); err != nil {
				t.Fatal(err)
			}
		}
		dir := t.TempDir()
		sectors := (uint64(10_000_000) + image.SectorSize - 1) / image.SectorSize
		if _, err := image.Pack(image.PackOptions{
			StagingDir:            stagingDir,
			Snapshots:             []image.SnapshotRef{{Name: name, ID: snap, Time: multiFixedClock()}},
			TargetCapacitySectors: sectors,
			OutputDir:             dir,
			RepoUUID:              [16]byte{9, 9, 9},
			DiscUUID:              [16]byte{discUUID},
			Label:                 label,
			FECEnabled:            true,
			Now:                   multiFixedClock,
			StageLog:              l,
		}); err != nil {
			t.Fatalf("pack disc %d: %v", discUUID, err)
		}
		return snap, dir
	}

	snap1, _ := commitAndPack(1, 1, "ONE", "disc-one")
	_, disc2Dir := commitAndPack(2, 2, "TWO", "disc-two")
	commitAndPack(3, 3, "THREE", "disc-three")

	outDir := t.TempDir()
	known := map[[16]byte]DiscName{
		{1}: {Seq: 1, Label: "disc-one"},
		{3}: {Seq: 3, Label: "disc-three"},
	}
	_, err = RestoreMultiWithProgress([]string{disc2Dir}, snap1, outDir, nil, WithKnownDiscs(known))
	if err == nil {
		t.Fatal("expected a missing-disc error")
	}
	missing, ok := err.(*MissingDiscError)
	if !ok {
		t.Fatalf("expected *MissingDiscError, got %T: %v", err, err)
	}
	var sawOne, sawThree bool
	for _, c := range missing.Candidates {
		if c.UUID == ([16]byte{1}) {
			sawOne = true
		}
		if c.UUID == ([16]byte{3}) {
			sawThree = true
		}
	}
	if !sawOne || !sawThree {
		t.Fatalf("expected both disc 1 and disc 3 among candidates, got %v", missing.Candidates)
	}
}

// TestRestoreMultiResumesMatchingSizeSkipsMismatch asserts that
// RestoreMulti (the engine behind restore's all-discs-at-once mode)
// applies the same resumed/skipped rule as the disc-swap --mount mode:
// a pre-existing file whose size matches the snapshot's tree entry
// counts as resumed and is left alone, while one whose size disagrees
// counts as skipped and is also left alone until --overwrite is given.
func TestRestoreMultiResumesMatchingSizeSkipsMismatch(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	target := filepath.Join(outDir, srcDir, "small.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(srcDir, "small.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, want, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := RestoreMulti([]string{treeDir}, snapID, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resumed != 1 {
		t.Fatalf("resumed = %d, want 1", rep.Resumed)
	}
	if rep.Skipped() != 0 {
		t.Fatalf("skipped = %d, want 0", rep.Skipped())
	}

	// A file present with the wrong size is a conflict, not a resume.
	// Use a fresh, otherwise-empty OUT-DIR so only that one file exists
	// ahead of time.
	outDir2 := t.TempDir()
	target2 := filepath.Join(outDir2, srcDir, "small.txt")
	if err := os.MkdirAll(filepath.Dir(target2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target2, append(want, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = RestoreMulti([]string{treeDir}, snapID, outDir2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped() != 1 {
		t.Fatalf("skipped = %d, want 1", rep.Skipped())
	}
	if rep.Resumed != 0 {
		t.Fatalf("resumed = %d, want 0", rep.Resumed)
	}
}
