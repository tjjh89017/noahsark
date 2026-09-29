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

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
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
	w := testWriter(stagingDir)
	w.Now = multiFixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	return stagingDir, srcDir, snapID
}

// packSequence packs one disc per entry of capacitiesBytes, in order,
// against the same staging directory and state log, and returns every
// disc's output root. Disc i has the uuid {i+1} and the disc_seq i.
func packSequence(t *testing.T, stagingDir string, snapID object.ID, capacitiesBytes []uint64) []string {
	t.Helper()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	objs, err := image.CollectReachable(testObjectPath(stagingDir), []object.ID{snapID})
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
			Store:                 testStore(stagingDir),
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
			// in one run leaves nothing for a later disc to pack.
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

// catalogOfRoots builds one catalog from the tables of every disc root.
func catalogOfRoots(t *testing.T, roots []string) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if _, err := catalog.WriteFromRoot(c, root); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// planDisc is one disc of a multi-disc test: its root, and the plan that
// gives chunks to it.
type planDisc struct {
	root string
	uuid [16]byte
	p    *plan.Plan
}

func (d *planDisc) Has(id object.ID) bool { return d.p.Owns(d.uuid, id) }

func (d *planDisc) Read(id object.ID) ([]byte, error) { return ReadChunkFromRoot(d.root, id) }

// restoreDiscs plans snapID over the discs of roots, with the discs in
// lost marked lost, and reads every disc that is not lost.
func restoreDiscs(t *testing.T, roots []string, lost map[int]bool, snapID object.ID, outDir string) (*plan.Plan, Report) {
	t.Helper()
	c := catalogOfRoots(t, roots)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := plan.Select(c, snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	var discs []plan.Disc
	for i := range roots {
		discs = append(discs, plan.Disc{DiscUUID: [16]byte{byte(i + 1)}, DiscSeq: uint64(i), Lost: lost[i]})
	}
	p, err := plan.New(c, sel, discs)
	if err != nil {
		t.Fatal(err)
	}
	if err := Scan(c, sel, outDir, false, p.Add); err != nil {
		t.Fatal(err)
	}
	a, err := NewAssembler(c, sel, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for i, root := range roots {
		if lost[i] {
			continue
		}
		if err := a.Disc(&planDisc{root: root, uuid: [16]byte{byte(i + 1)}, p: p}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Finish(); err != nil {
		t.Fatal(err)
	}
	return p, a.Report()
}

// TestAssemblerAcrossThreeDiscs restores one snapshot whose chunks lie
// on three discs, one disc at a time, and checks the plan counts.
func TestAssemblerAcrossThreeDiscs(t *testing.T) {
	stagingDir, srcDir, snapID := commitMultiFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{7_000_000, 7_000_000, 10_000_000})
	if len(roots) < 3 {
		t.Fatalf("the fixture packed %d disc(s), want 3", len(roots))
	}

	outDir := t.TempDir()
	p, rep := restoreDiscs(t, roots, nil, snapID, outDir)
	if rep.Failed() {
		t.Fatalf("restore reported %s", rep.Summary())
	}
	compareTrees(t, srcDir, outDir)
	if n := len(p.Discs()); n != 3 {
		t.Fatalf("the plan names %d disc(s), want 3", n)
	}
	if p.NoDisc() != 0 {
		t.Fatalf("%d item(s) with no disc, want 0", p.NoDisc())
	}
}

// TestAssemblerLostDiscFailsOnlyItsFiles marks the middle disc lost. The
// files that need it are not restored; every other file is restored.
func TestAssemblerLostDiscFailsOnlyItsFiles(t *testing.T) {
	stagingDir, srcDir, snapID := commitMultiFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{7_000_000, 7_000_000, 10_000_000})
	if len(roots) < 3 {
		t.Fatalf("the fixture packed %d disc(s), want 3", len(roots))
	}

	outDir := t.TempDir()
	p, rep := restoreDiscs(t, roots, map[int]bool{1: true}, snapID, outDir)
	var lost *plan.DiscEntry
	for _, d := range p.Discs() {
		if d.Lost {
			lost = &d
		}
	}
	if lost == nil || lost.DiscSeq != 1 || lost.Items == 0 {
		t.Fatalf("the plan does not name disc 1 as lost with items: %+v", p.Discs())
	}
	failed := problemsOf(rep, KindFile)
	if len(failed) == 0 {
		t.Fatal("no file is reported as not restored")
	}
	restored := 0
	for i := range 6 {
		name := filepath.Join(fmt.Sprintf("sub%d", i), "f.bin")
		if _, err := os.Stat(filepath.Join(outDir, name)); err == nil {
			compareFileBytes(t, filepath.Join(outDir, name), filepath.Join(srcDir, name))
			restored++
		}
	}
	if restored == 0 || restored+len(failed) != 6 {
		t.Fatalf("%d file(s) restored, %d failed, want the 6 files split", restored, len(failed))
	}
	if parts := partsUnder(t, outDir); len(parts) > len(failed) {
		t.Fatalf("part files %v, want at most one for each of %d failed file(s)", parts, len(failed))
	}
}

// TestRestoreResumesMatchingSizeSkipsMismatch asserts that a file that
// is already there with the content of the snapshot counts as resumed,
// and that one with another size counts as skipped.
func TestRestoreResumesMatchingSizeSkipsMismatch(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	want, err := os.ReadFile(filepath.Join(srcDir, "small.txt"))
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir, "small.txt"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := restoreTree(t, treeDir, snapID, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resumed != 1 || rep.Skipped() != 0 {
		t.Fatalf("resumed = %d, skipped = %d, want 1 and 0", rep.Resumed, rep.Skipped())
	}

	outDir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir2, "small.txt"), append(want, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = restoreTree(t, treeDir, snapID, outDir2, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped() != 1 || rep.Resumed != 0 {
		t.Fatalf("skipped = %d, resumed = %d, want 1 and 0", rep.Skipped(), rep.Resumed)
	}
}
