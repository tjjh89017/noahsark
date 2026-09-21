package restore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestVerifyAndRestoreAgreeOnAHeaderCRCMismatch packs one disc, damages
// one header byte of the on-disc tree object header_crc32c covers, and
// checks that both image.Read (what verify runs) and Restore refuse it.
//
// Before this fix, image.Read never checked header_crc32c, so a disc
// carrying this exact damage read back clean; a repeated verify could
// then move the disc's objects to CLEAN, gc could free the staged
// copies, and a later restore from that disc alone would fail with no
// copy left. This test is the reported data-loss case: verify and
// restore must refuse the same object, not just one of them.
func TestVerifyAndRestoreAgreeOnAHeaderCRCMismatch(t *testing.T) {
	stagingDir, srcDir, snapID := commitMultiFixture(t)
	_ = srcDir
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

	outDir := t.TempDir()
	opts := image.PackOptions{
		StagingDir:            stagingDir,
		Snapshots:             []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: multiFixedClock()}},
		TargetCapacitySectors: (50_000_000 + image.SectorSize - 1) / image.SectorSize,
		OutputDir:             outDir,
		RepoUUID:              [16]byte{9, 9, 9},
		DiscUUID:              [16]byte{1},
		Label:                 "agree-disc",
		FECEnabled:            true,
		Now:                   multiFixedClock,
		StageLog:              l,
	}
	if _, err := image.Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	runDir, err := image.NewestRunDir(filepath.Join(outDir, "NOAHSARK", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	idxBuf, err := os.ReadFile(filepath.Join(runDir, "INDEX.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var idx format.Index
	if _, err := idx.Decode(idxBuf); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(outDir, "NOAHSARK")
	paths, err := image.ObjectPaths(base, &idx, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	var treePath string
	for i, row := range idx.Objects {
		if row.Kind == format.ObjectKindTree {
			treePath = paths[i]
			break
		}
	}
	if treePath == "" {
		t.Fatal("fixture carries no tree object")
	}

	data, err := os.ReadFile(treePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 40 {
		t.Fatalf("tree object %s is too short to damage at offset 40", treePath)
	}
	data[40] ^= 0xff
	if err := os.WriteFile(treePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := image.Read(outDir); err == nil {
		t.Fatal("image.Read: want an error over the damaged tree header, got none")
	}

	restoreOut := t.TempDir()
	report, restoreErr := Restore(outDir, snapID, restoreOut)
	if restoreErr != nil {
		// A MissingDiscError over a single, provided disc would itself be
		// a surprise; either form of refusal satisfies this test, since
		// what matters is that Restore never silently accepts the
		// damaged tree.
		return
	}
	if report.Count(KindFile) == 0 {
		t.Fatal("Restore: want the damaged tree reported as a file(s) not restored problem, got none")
	}
}

// TestRestoreRefusesAChunkHeaderCRCMismatch damages a chunk object's
// header, never its payload, leaving the chunk's content id intact. A
// chunk's payload is written straight into a restored file, with no
// kind-specific Decode call to catch a bad header_crc32c the way tree,
// blob and snapshot objects already did; before this fix, this exact
// damage restored a file with silently accepted bytes. It must now be
// refused, the same as any other kind.
func TestRestoreRefusesAChunkHeaderCRCMismatch(t *testing.T) {
	stagingDir, _, snapID := commitMultiFixture(t)
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

	outDir := t.TempDir()
	opts := image.PackOptions{
		StagingDir:            stagingDir,
		Snapshots:             []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: multiFixedClock()}},
		TargetCapacitySectors: (50_000_000 + image.SectorSize - 1) / image.SectorSize,
		OutputDir:             outDir,
		RepoUUID:              [16]byte{9, 9, 9},
		DiscUUID:              [16]byte{1},
		Label:                 "agree-disc-chunk",
		FECEnabled:            true,
		Now:                   multiFixedClock,
		StageLog:              l,
	}
	if _, err := image.Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	runDir, err := image.NewestRunDir(filepath.Join(outDir, "NOAHSARK", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	idxBuf, err := os.ReadFile(filepath.Join(runDir, "INDEX.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var idx format.Index
	if _, err := idx.Decode(idxBuf); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(outDir, "NOAHSARK")
	paths, err := image.ObjectPaths(base, &idx, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	var chunkPath string
	for i, row := range idx.Objects {
		if row.Kind == format.ObjectKindChunk {
			chunkPath = paths[i]
			break
		}
	}
	if chunkPath == "" {
		t.Fatal("fixture carries no chunk object")
	}

	data, err := os.ReadFile(chunkPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 40 {
		t.Fatalf("chunk object %s is too short to damage at offset 40", chunkPath)
	}
	data[40] ^= 0xff
	if err := os.WriteFile(chunkPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	restoreOut := t.TempDir()
	report, restoreErr := Restore(outDir, snapID, restoreOut)
	if restoreErr != nil {
		return
	}
	if report.Count(KindFile) == 0 {
		t.Fatal("Restore: want the damaged chunk reported as a file(s) not restored problem, got none")
	}
}
