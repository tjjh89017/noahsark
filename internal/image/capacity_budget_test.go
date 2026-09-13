package image

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// dirBytes sums the apparent size of every regular file under dir,
// matching what `du -sb` reports: real file content, not the run's own
// checksum and parity structure padded to block boundaries any
// differently than it already is on disk.
func dirBytes(t *testing.T, dir string) uint64 {
	t.Helper()
	var total uint64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// TestPackStaysWithinCapacity packs the same fixture at several forced
// capacities and checks, for each, that the packed tree's real bytes
// plus the run's own filesystem overhead estimate never exceed the
// capacity: pack's own budget must hold for the tree it actually
// writes, not just for the numbers it predicted while selecting.
func TestPackStaysWithinCapacity(t *testing.T) {
	capacities := []uint64{2_500_000, 3_500_000, 4_800_000, 8_000_000, 16_000_000}
	for _, mb := range capacities {
		t.Run(fmt.Sprintf("%dbytes", mb), func(t *testing.T) {
			stagingDir, snapID := packFixture(t)
			l, err := stage.Open(stagingDir)
			if err != nil {
				t.Fatal(err)
			}
			markStagedFromCommit(t, stagingDir, snapID, l)

			capSectors := sectorsFor(mb)
			outDir := t.TempDir()
			opts := packOpts(stagingDir, snapID, outDir, capSectors, 1, l)
			res, err := Pack(opts)
			if err != nil {
				t.Fatalf("pack: %v", err)
			}

			treeBytes := dirBytes(t, outDir)
			overhead := EstimateFilesystemOverhead(res.FileCount)
			limit := capSectors * SectorSize
			if treeBytes+overhead > limit {
				t.Fatalf("tree %d bytes + estimated overhead %d bytes = %d, exceeds capacity %d bytes",
					treeBytes, overhead, treeBytes+overhead, limit)
			}
			t.Logf("capacity %d bytes: tree %d bytes, overhead %d bytes, headroom %d bytes",
				limit, treeBytes, overhead, limit-treeBytes-overhead)
		})
	}
}

// TestPackedTreeFitsRealUDFImage builds a real mkudffs UDF image at the
// same capacity pack used, loop-mounts it and copies the packed tree
// in, the way test/e2e/disc/chain.sh does. It needs root and mkudffs,
// so it runs only under NOAHSARK_CI; otherwise it skips.
func TestPackedTreeFitsRealUDFImage(t *testing.T) {
	if os.Getenv(ciEnvVar) == "" {
		t.Skip("NOAHSARK_CI not set; skipping the real mkudffs populate check")
	}
	if _, err := exec.LookPath("mkudffs"); err != nil {
		t.Skip("mkudffs not installed")
	}
	if _, err := CheckTools(); err != nil {
		t.Skipf("udftools too old: %v", err)
	}

	stagingDir, snapID := packFixture(t)
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, l)

	capSectors := sectorsFor(4_800_000)
	outDir := t.TempDir()
	opts := packOpts(stagingDir, snapID, outDir, capSectors, 1, l)
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	imagePath := filepath.Join(t.TempDir(), "run.img")
	if err := MakeImage(outDir, imagePath, capSectors); err != nil {
		t.Fatalf("MakeImage (mkudffs build and populate): %v", err)
	}
}
