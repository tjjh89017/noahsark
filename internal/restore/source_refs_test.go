package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// commitRefsFixture commits a small, distinct source tree under name, so
// two calls with different names produce two snapshots with different
// content, into a shared staging directory.
func commitRefsFixture(t *testing.T, stagingDir, name string) object.ID {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	w := object.NewWriter(stagingDir)
	w.Now = multiFixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	return snapID
}

// markRefsFixtureStaged marks every object id reaches as Staged.
func markRefsFixtureStaged(t *testing.T, stagingDir string, id object.ID, l *stage.Log) {
	t.Helper()
	objs, err := image.CollectReachable(stagingDir, []object.ID{id})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if err := l.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// overwriteREFS replaces discRoot's newest run's catalog/REFS.bin with
// exactly recs, bypassing Pack, so a test can fabricate a disc whose
// REFS is missing or stale for one ref name.
func overwriteREFS(t *testing.T, discRoot string, repoUUID [16]byte, recs []format.RefRecord) {
	t.Helper()
	runsDir := filepath.Join(discRoot, "NOAHSARK", "runs")
	runDir, err := image.NewestRunDir(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	table := format.RefsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs,
			VersionMajor: 1, HeaderLen: format.RefsHeaderLen,
		},
		RepoUUID: repoUUID, RecordCount: uint64(len(recs)), Records: recs,
	}
	buf := make([]byte, table.EncodedLen())
	if _, err := table.Encode(buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "catalog", "REFS.bin"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSourceRefsMergesAcrossDiscs packs one ref per disc, then strips
// the second ref back out of the first disc's own REFS, the way a disc
// packed before pack carried refs forward would read. Refs must still
// resolve both ref names by merging every provided disc's REFS, however
// the disc roots are ordered, the way OpenSource's caller (e.g.
// --discs-dir lexical order) hands them over.
func TestSourceRefsMergesAcrossDiscs(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	repoUUID := [16]byte{1, 2, 3, 4}

	firstSnap := commitRefsFixture(t, stagingDir, "first")
	markRefsFixtureStaged(t, stagingDir, firstSnap, l)
	firstOut := t.TempDir()
	if _, err := image.Pack(image.PackOptions{
		StagingDir: stagingDir, Snapshots: []image.SnapshotRef{{Name: "run1", ID: firstSnap, Time: multiFixedClock()}},
		TargetCapacitySectors: 100_000, PhysicalCapacitySectors: 100_000,
		OutputDir: firstOut, RepoUUID: repoUUID, DiscUUID: [16]byte{1}, Label: "disc-1",
		Now: multiFixedClock, StageLog: l,
	}); err != nil {
		t.Fatalf("first pack: %v", err)
	}

	// Strip run2 back out of disc 1's own REFS, simulating a disc
	// packed before pack carried refs forward: it names run1 only.
	var name1 [format.RefNameLen]byte
	n1 := copy(name1[:], "run1")
	overwriteREFS(t, firstOut, repoUUID, []format.RefRecord{
		{SnapshotID: firstSnap, TimeSec: multiFixedClock().Unix(), NameLen: uint16(n1), Name: name1},
	})

	secondSnap := commitRefsFixture(t, stagingDir, "second")
	markRefsFixtureStaged(t, stagingDir, secondSnap, l)
	secondOut := t.TempDir()
	if _, err := image.Pack(image.PackOptions{
		StagingDir: stagingDir, Snapshots: []image.SnapshotRef{{Name: "run2", ID: secondSnap, Time: multiFixedClock()}},
		TargetCapacitySectors: 100_000, PhysicalCapacitySectors: 100_000,
		OutputDir: secondOut, RepoUUID: repoUUID, DiscUUID: [16]byte{2}, Label: "disc-2",
		Now: multiFixedClock, StageLog: l,
	}); err != nil {
		t.Fatalf("second pack: %v", err)
	}

	// discRoots given in the "wrong", lexically-first-disc-first order:
	// disc 1 (whose REFS was stripped back down to run1 alone) is
	// bases[0]. The old code read only bases[0] and would fail to
	// resolve run2.
	src, err := OpenSource([]string{firstOut, secondOut})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	got, err := src.ParseSnapshotArg("run2")
	if err != nil {
		t.Fatalf("ParseSnapshotArg(run2): %v", err)
	}
	if got != secondSnap {
		t.Fatalf("run2 resolved to %s, want %s", got.TextForm(), secondSnap.TextForm())
	}
	got1, err := src.ParseSnapshotArg("run1")
	if err != nil {
		t.Fatalf("ParseSnapshotArg(run1): %v", err)
	}
	if got1 != firstSnap {
		t.Fatalf("run1 resolved to %s, want %s", got1.TextForm(), firstSnap.TextForm())
	}

	refs, err := src.Refs()
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if len(refs.Records) != 2 {
		t.Fatalf("merged Refs has %d records, want 2", len(refs.Records))
	}
}

// TestSourceParseSnapshotArgUnknownRefNamesProvidedDiscs checks that
// *Source.ParseSnapshotArg, given a ref name not in any provided disc's
// merged REFS, reports it as not on the provided disc(s) rather than as
// an unknown name outright: a later disc in the chain, not given here,
// may still carry it.
func TestSourceParseSnapshotArgUnknownRefNamesProvidedDiscs(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	repoUUID := [16]byte{9, 9, 9}

	snap := commitRefsFixture(t, stagingDir, "only")
	markRefsFixtureStaged(t, stagingDir, snap, l)
	out := t.TempDir()
	if _, err := image.Pack(image.PackOptions{
		StagingDir: stagingDir, Snapshots: []image.SnapshotRef{{Name: "only", ID: snap, Time: multiFixedClock()}},
		TargetCapacitySectors: 100_000, PhysicalCapacitySectors: 100_000,
		OutputDir: out, RepoUUID: repoUUID, DiscUUID: [16]byte{1}, Label: "disc-1",
		Now: multiFixedClock, StageLog: l,
	}); err != nil {
		t.Fatalf("pack: %v", err)
	}

	src, err := OpenSource([]string{out})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	_, err = src.ParseSnapshotArg("later-disc-ref")
	if err == nil {
		t.Fatal("expected an error for a ref not on the provided disc")
	}
	if !strings.Contains(err.Error(), "not on the provided disc(s)") {
		t.Fatalf("error = %q, want the provided-disc(s) wording", err)
	}
	if strings.Contains(err.Error(), "neither a snapshot id nor a known ref name") {
		t.Fatalf("error = %q, want the disc-oriented wording, not the cache one", err)
	}
}

// TestSourceRefsNewestRecordWins gives two discs two records for the
// same ref name and checks the merge keeps the newest one by time,
// whichever disc root is listed first. A ref record carries no run
// number, so only the time and the snapshot id decide.
func TestSourceRefsNewestRecordWins(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	repoUUID := [16]byte{5, 6, 7, 8}

	oldSnap := commitRefsFixture(t, stagingDir, "old")
	markRefsFixtureStaged(t, stagingDir, oldSnap, l)
	firstOut := t.TempDir()
	if _, err := image.Pack(image.PackOptions{
		StagingDir: stagingDir, Snapshots: []image.SnapshotRef{{Name: "LATEST", ID: oldSnap, Time: multiFixedClock()}},
		TargetCapacitySectors: 100_000, PhysicalCapacitySectors: 100_000,
		OutputDir: firstOut, RepoUUID: repoUUID, DiscUUID: [16]byte{1}, Label: "disc-1",
		Now: multiFixedClock, StageLog: l,
	}); err != nil {
		t.Fatalf("first pack: %v", err)
	}

	newSnap := commitRefsFixture(t, stagingDir, "new")
	markRefsFixtureStaged(t, stagingDir, newSnap, l)
	secondOut := t.TempDir()
	if _, err := image.Pack(image.PackOptions{
		StagingDir: stagingDir, Snapshots: []image.SnapshotRef{{Name: "LATEST", ID: newSnap, Time: multiFixedClock().Add(time.Second)}},
		TargetCapacitySectors: 100_000, PhysicalCapacitySectors: 100_000,
		OutputDir: secondOut, RepoUUID: repoUUID, DiscUUID: [16]byte{2}, Label: "disc-2",
		Now: multiFixedClock, StageLog: l,
	}); err != nil {
		t.Fatalf("second pack: %v", err)
	}

	for _, order := range [][]string{{firstOut, secondOut}, {secondOut, firstOut}} {
		src, err := OpenSource(order)
		if err != nil {
			t.Fatalf("OpenSource(%v): %v", order, err)
		}
		got, err := src.ParseSnapshotArg("LATEST")
		if err != nil {
			t.Fatalf("ParseSnapshotArg(LATEST), order %v: %v", order, err)
		}
		if got != newSnap {
			t.Fatalf("order %v: LATEST resolved to %s, want the newest record %s", order, got.TextForm(), newSnap.TextForm())
		}
	}
}
