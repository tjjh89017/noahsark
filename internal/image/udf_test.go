package image

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCheckToolsSkipsWithoutMkudffs checks that CheckTools reports an
// error, rather than panicking, when mkudffs is not installed.
func TestCheckToolsSkipsWithoutMkudffs(t *testing.T) {
	if _, err := exec.LookPath("mkudffs"); err == nil {
		t.Skip("mkudffs is installed; nothing to test here")
	}
	if _, err := CheckTools(); err == nil {
		t.Fatal("expected an error when mkudffs is not installed")
	}
}

// buildImageFixture packs a fixture tree and returns its output
// directory and the target capacity to build an image at.
func buildImageFixture(t *testing.T) (outDir string, sectors uint64) {
	t.Helper()
	stagingDir, snapID := stageFixture(t)
	outDir = t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	if _, err := Build(opts); err != nil {
		t.Fatal(err)
	}
	return outDir, opts.TargetCapacitySectors
}

// TestMakeImageNeedsRoot checks that MakeImage refuses to populate the
// image and removes the partial file when the calling process is not
// root, without shelling out to sudo itself.
func TestMakeImageNeedsRoot(t *testing.T) {
	if _, err := exec.LookPath("mkudffs"); err != nil {
		t.Skip("mkudffs not installed")
	}
	if _, err := CheckTools(); err != nil {
		t.Skipf("udftools too old: %v", err)
	}

	origGeteuid := geteuid
	geteuid = func() int { return 1000 }
	defer func() { geteuid = origGeteuid }()

	outDir, sectors := buildImageFixture(t)
	imagePath := filepath.Join(t.TempDir(), "run.img")

	err := MakeImage(outDir, imagePath, sectors, nil)
	if !errors.Is(err, ErrPopulateNeedsRoot) {
		t.Fatalf("MakeImage error = %v, want ErrPopulateNeedsRoot", err)
	}
	if _, statErr := os.Stat(imagePath); !os.IsNotExist(statErr) {
		t.Fatal("MakeImage left a partial image behind after refusing to populate")
	}
}

// TestMakeImageRemovesPartialImageOnMountFailure checks that a failing
// mount step also leaves no partial image behind, using an injected
// mount command so the test needs neither real root nor a real loop
// device.
func TestMakeImageRemovesPartialImageOnMountFailure(t *testing.T) {
	if _, err := exec.LookPath("mkudffs"); err != nil {
		t.Skip("mkudffs not installed")
	}
	if _, err := CheckTools(); err != nil {
		t.Skipf("udftools too old: %v", err)
	}

	origGeteuid := geteuid
	origMountCmd := newMountCmd
	geteuid = func() int { return 0 }
	newMountCmd = func(imagePath, mnt string) *exec.Cmd {
		return exec.Command("false")
	}
	defer func() {
		geteuid = origGeteuid
		newMountCmd = origMountCmd
	}()

	outDir, sectors := buildImageFixture(t)
	imagePath := filepath.Join(t.TempDir(), "run.img")

	if err := MakeImage(outDir, imagePath, sectors, nil); err == nil {
		t.Fatal("expected an error when the mount command fails")
	}
	if _, statErr := os.Stat(imagePath); !os.IsNotExist(statErr) {
		t.Fatal("MakeImage left a partial image behind after a mount failure")
	}
}
