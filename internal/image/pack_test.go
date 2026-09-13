package image

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

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

	stagingDir := t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
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
		Now:                     fixedClock,
		StageLog:                l,
	}
}

// sectorsFor converts a byte size to whole 2048-byte sectors.
func sectorsFor(bytes uint64) uint64 {
	return (bytes + SectorSize - 1) / SectorSize
}

func TestPackSpansThreeDiscsWithRemainder(t *testing.T) {
	stagingDir, snapID := packFixture(t)
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, l)

	capacities := []uint64{sectorsFor(2_000_000), sectorsFor(2_000_000), sectorsFor(4_000_000)}
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
