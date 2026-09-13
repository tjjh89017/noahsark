package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMakeImageBuildsEmptyImage builds a NOAHSARK tree and turns it into a
// UDF image with mkudffs, without populating it (no CI env, no root). It
// skips when mkudffs is missing or too old.
func TestMakeImageBuildsEmptyImage(t *testing.T) {
	if _, err := exec.LookPath("mkudffs"); err != nil {
		t.Skip("mkudffs not installed")
	}
	if _, err := CheckTools(); err != nil {
		t.Skipf("udftools too old: %v", err)
	}

	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	if _, err := Build(opts); err != nil {
		t.Fatal(err)
	}

	imagePath := filepath.Join(t.TempDir(), "run.img")
	if err := MakeImage(outDir, imagePath, opts.TargetCapacitySectors, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(info.Size()) != opts.TargetCapacitySectors*SectorSize {
		t.Fatalf("image size = %d, want %d", info.Size(), opts.TargetCapacitySectors*SectorSize)
	}
}

func TestCheckToolsSkipsWithoutMkudffs(t *testing.T) {
	if _, err := exec.LookPath("mkudffs"); err == nil {
		t.Skip("mkudffs is installed; nothing to test here")
	}
	if _, err := CheckTools(); err == nil {
		t.Fatal("expected an error when mkudffs is not installed")
	}
}
