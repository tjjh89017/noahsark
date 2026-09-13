package image

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/chunker"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// packFixtureChunkProfile cuts chunks far smaller than the production
// default, purely so packFixture's big file splits into enough chunks,
// over a small enough span, for a small forced capacity to land its
// selection boundary between two of them.
var packFixtureChunkProfile = chunker.Profile{
	Min:   1 << 16,
	Avg:   1 << 18,
	Max:   1 << 20,
	MaskS: spreadMask(20),
	MaskL: spreadMask(16),
}

// spreadMask mirrors the chunker package's own bit-spread mask rule
// for a mask of n set bits, used only to build packFixtureChunkProfile.
func spreadMask(n int) uint64 {
	var mask uint64
	for j := range n {
		pos := 63 - (j * 32 / n)
		mask |= 1 << uint(pos)
	}
	return mask
}

// packFixture commits a source tree of a few MiB of low-entropy-free
// (so it neither dedups nor compresses away) content, spread over
// several files so a run over a small forced capacity has to split it,
// and returns the staging directory and the snapshot id.
func packFixture(t *testing.T) (string, object.ID) {
	t.Helper()
	srcDir := t.TempDir()
	rng := rand.New(rand.NewSource(1))
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
	// One file, chunked under packFixtureChunkProfile, cuts into many
	// small chunks, so a capacity boundary can fall between two of
	// them: the blob that lands on the later disc then references a
	// chunk the earlier disc already stored, and that disc's INDEX
	// carries a Prereqs row for it.
	bigDir := filepath.Join(srcDir, "big")
	if err := os.MkdirAll(bigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 4<<20)
	if _, err := rng.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bigDir, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir := t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	w.Profile = packFixtureChunkProfile
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	return stagingDir, snapID
}

// markStagedFromCommit marks every object the snapshot reaches as
// Staged, the way cmd_commit does after a real commit.
func markStagedFromCommit(t *testing.T, stagingDir string, snapID object.ID, l *stage.Log) {
	t.Helper()
	objs, err := CollectReachable(stagingDir, []object.ID{snapID})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if err := l.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func packOpts(stagingDir string, snapID object.ID, outDir string, capacitySectors uint64, discUUID byte, l *stage.Log) PackOptions {
	return PackOptions{
		StagingDir:              stagingDir,
		Snapshots:               []SnapshotRef{{Name: "LATEST", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors:   capacitySectors,
		PhysicalCapacitySectors: capacitySectors,
		OutputDir:               outDir,
		RepoUUID:                [16]byte{1, 2, 3, 4},
		DiscUUID:                [16]byte{discUUID},
		Label:                   "test-disc",
		FECEnabled:              true,
		Now:                     fixedClock,
		StageLog:                l,
	}
}

// sectorsFor converts a byte size to whole 2048-byte sectors.
func sectorsFor(bytes uint64) uint64 {
	return (bytes + SectorSize - 1) / SectorSize
}

// commitNamedFixture commits a small, distinct source tree into
// stagingDir under name, so two calls with different names produce two
// snapshots with different content, and returns the new snapshot id.
func commitNamedFixture(t *testing.T, stagingDir, name string) object.ID {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("more content, fixture "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	return snapID
}

// TestPackTwoSnapshotsSnapobjOrder packs a run that carries two
// snapshots' objects in catalog/snapobj. Every disc always stores every
// repository snapshot's own object there, regardless of which ref the
// run's REFS table names, so two committed snapshots always produce two
// snapobj files. Read must resolve the FEC stream over those two files
// in the same order Pack wrote them in.
func TestPackTwoSnapshotsSnapobjOrder(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	// Several snapshots, not just two: the snapshot object id (a hash of
	// its own payload) and the snapshot file's whole-bytes hash sort
	// independently of each other, so with enough snapshots at least one
	// pair is certain to land in a different relative order under the
	// two keys.
	var firstSnap object.ID
	for i := range 8 {
		id := commitNamedFixture(t, stagingDir, fmt.Sprintf("snap-%d", i))
		if i == 0 {
			firstSnap = id
		}
		markStagedFromCommit(t, stagingDir, id, l)
	}

	outDir := t.TempDir()
	opts := packOpts(stagingDir, firstSnap, outDir, sectorsFor(50_000_000), 1, l)
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	if _, err := Read(outDir); err != nil {
		t.Fatalf("Read: %v", err)
	}
}

func TestPackSpansThreeDiscsWithRemainder(t *testing.T) {
	stagingDir, snapID := packFixture(t)
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, l)

	capacities := []uint64{sectorsFor(7_000_000), sectorsFor(7_000_000), sectorsFor(7_000_000)}
	var discRoots []string
	var results []*PackResult
	for i, cap := range capacities {
		outDir := t.TempDir()
		opts := packOpts(stagingDir, snapID, outDir, cap, byte(i+1), l)
		res, err := Pack(opts)
		if err != nil {
			t.Fatalf("pack %d: %v", i, err)
		}
		discRoots = append(discRoots, outDir)
		results = append(results, res)
		if res.RunSeq != uint64(i+1) || res.DiscSeq != uint64(i) {
			t.Fatalf("pack %d: got run %d disc %d", i, res.RunSeq, res.DiscSeq)
		}
	}

	if results[len(results)-1].RemainingObjects == 0 {
		t.Fatal("expected objects to remain STAGED after three small-capacity packs")
	}
	t.Logf("remaining after 3 packs: %d objects, %d bytes", results[len(results)-1].RemainingObjects, results[len(results)-1].RemainingBytes)

	// Every disc must read back clean, and DISCS must list every earlier
	// disc plus itself.
	for i, root := range discRoots {
		rr, err := Read(root)
		if err != nil {
			t.Fatalf("disc %d: Read: %v", i, err)
		}
		if int(rr.Discs.RecordCount) != i+1 {
			t.Fatalf("disc %d: DISCS has %d rows, want %d", i, rr.Discs.RecordCount, i+1)
		}
		for j, row := range rr.Discs.Rows {
			if row.RunSeq != uint64(j+1) {
				t.Fatalf("disc %d: DISCS row %d has run_seq %d, want %d", i, j, row.RunSeq, j+1)
			}
		}
	}

	// The last disc's INDEX must carry a Prereqs row for every object
	// this disc's own metadata objects reference but do not store, and
	// that row's run_seq must name an earlier, real run.
	lastRunDir, err := NewestRunDir(filepath.Join(discRoots[len(discRoots)-1], "NOAHSARK", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	idxBuf, err := os.ReadFile(filepath.Join(lastRunDir, "INDEX.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var idx format.Index
	if _, err := idx.Decode(idxBuf); err != nil {
		t.Fatal(err)
	}
	if idx.PrereqCount == 0 {
		t.Fatal("expected at least one Prereqs row on the last disc, since not everything fit on one disc")
	}
	for _, p := range idx.Prereqs {
		if p.RunSeq == 0 || p.RunSeq > uint64(len(discRoots)) {
			t.Fatalf("prereq names run_seq %d, out of range for %d built runs", p.RunSeq, len(discRoots))
		}
	}
}
