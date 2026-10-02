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

// TestRestoreRefusesAChunkHeaderCRCMismatch damages a chunk object's
// header, never its payload, leaving the chunk's content id intact. A
// chunk's payload is written straight into a restored file, with no
// kind-specific Decode call to catch a bad header_crc32c. The restore
// must refuse it, the same as any other kind.
func TestRestoreRefusesAChunkHeaderCRCMismatch(t *testing.T) {
	stagingDir, _, snapID := commitMultiFixture(t)
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

	outDir := t.TempDir()
	opts := image.PackOptions{
		Store:                 testStore(stagingDir),
		Snapshots:             []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: multiFixedClock()}},
		TargetCapacitySectors: (50_000_000 + image.SectorSize - 1) / image.SectorSize,
		OutputDir:             outDir,
		RepoUUID:              [16]byte{9, 9, 9},
		DiscUUID:              [16]byte{1},
		Label:                 "agree-disc-chunk",
		Now:                   multiFixedClock,
		StageLog:              l,
	}
	if _, err := image.Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}
	c := catalogOfTree(t, outDir)

	runDir, err := image.RunDir(filepath.Join(outDir, "NOAHSARK", "runs"))
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

	report, err := restoreWith(t, c, outDir, snapID, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Count(KindFile) == 0 {
		t.Fatal("want the damaged chunk reported as a file(s) not restored problem, got none")
	}
}
