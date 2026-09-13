package image

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// smallFixture commits many small, distinct files into a fresh staging
// directory, so the run holds many small chunk and blob objects: the
// UDF in-ICB embedding threshold the mount option must defeat.
func smallFixture(t *testing.T) (string, object.ID) {
	t.Helper()
	srcDir := t.TempDir()
	for i := 0; i < 300; i++ {
		name := filepath.Join(srcDir, fmt.Sprintf("f%03d.txt", i))
		content := fmt.Sprintf("small object number %d, well under one sector", i)
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
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

// TestPopulatedImageHasNoInICBFiles builds a run of many small objects,
// turns it into a populated UDF image under CI, then scans the raw image
// bytes with the carving reader. Every structure the format writes must
// carve out at a 2048-byte sector offset; an in-ICB embedded file would
// carve out at an offset inside its File Entry block instead, which is
// not sector-aligned. This is the sector-boundary rule every file the
// format writes must follow.
func TestPopulatedImageHasNoInICBFiles(t *testing.T) {
	if os.Getenv(ciEnvVar) == "" {
		t.Skip("set NOAHSARK_CI=1 to run the image-populate test")
	}
	if _, err := exec.LookPath("mkudffs"); err != nil {
		t.Skip("mkudffs not installed")
	}
	if _, err := CheckTools(); err != nil {
		t.Skipf("udftools too old: %v", err)
	}

	stagingDir, snapID := smallFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)

	result, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.ObjectCount < 300 {
		t.Fatalf("expected at least 300 small objects, got %d", result.ObjectCount)
	}

	imagePath := filepath.Join(t.TempDir(), "run.img")
	if err := MakeImage(outDir, imagePath, opts.TargetCapacitySectors); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	carver := format.NewCarver(f)
	found := 0
	for {
		c, err := carver.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		found++
		if c.Offset%SectorSize != 0 {
			t.Fatalf("structure %T carved at offset %d, not a multiple of %d bytes: it was not written on its own sector, which the noadinicb mount option should have prevented", c.Value, c.Offset, SectorSize)
		}
	}
	if found < result.ObjectCount {
		t.Fatalf("carver found %d structures, expected at least the %d objects Build wrote", found, result.ObjectCount)
	}
}
