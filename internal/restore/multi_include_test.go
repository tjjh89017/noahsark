package restore

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// commitTwoSubdirFixture commits a source tree of exactly two
// subdirectories: sub0 holds a small file and sub1 a much larger one, so
// a tight capacity on the first disc of a two-disc sequence packs sub0
// alone, with sub1 and every tree above it rolling to the second disc.
func commitTwoSubdirFixture(t *testing.T) (stagingDir, srcDir string, snapID object.ID) {
	t.Helper()
	srcDir = t.TempDir()

	sub0 := filepath.Join(srcDir, "sub0")
	sub1 := filepath.Join(srcDir, "sub1")
	if err := os.MkdirAll(sub0, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub0, "f.bin"), []byte("small file in sub0"), 0o644); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(3))
	big := make([]byte, 900_000)
	if _, err := rng.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub1, "f.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir = t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = multiFixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	return stagingDir, srcDir, snapID
}

// TestRestoreMultiIncludeOmittedDiscHoldsOnlyExcluded packs sub0 alone
// onto the first disc and sub1, with every tree above it, onto the
// second. Restoring only sub1's file, with the first disc omitted, must
// succeed: the omitted disc holds nothing the include needs.
func TestRestoreMultiIncludeOmittedDiscHoldsOnlyExcluded(t *testing.T) {
	stagingDir, srcDir, snapID := commitTwoSubdirFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{5_500_000, 7_000_000})

	outDir := t.TempDir()
	inc := includePath(srcDir, "sub1/f.bin")
	if _, _, err := RestoreMulti([]string{roots[1]}, snapID, outDir, WithInclude([]string{inc})); err != nil {
		t.Fatalf("RestoreMulti: %v", err)
	}

	root := filepath.Join(outDir, srcDir)
	mustExist(t, filepath.Join(root, "sub1", "f.bin"))
	mustNotExist(t, filepath.Join(root, "sub0"))
}

// TestRestoreMultiIncludeOmittedDiscHoldsIncluded is the same fixture,
// but the include names sub0's file, which lives only on the omitted
// first disc: the restore must fail naming that disc.
func TestRestoreMultiIncludeOmittedDiscHoldsIncluded(t *testing.T) {
	stagingDir, srcDir, snapID := commitTwoSubdirFixture(t)
	roots := packSequence(t, stagingDir, snapID, []uint64{5_500_000, 7_000_000})

	outDir := t.TempDir()
	inc := includePath(srcDir, "sub0/f.bin")
	_, _, err := RestoreMulti([]string{roots[1]}, snapID, outDir, WithInclude([]string{inc}))
	if err == nil {
		t.Fatal("expected a missing-disc error")
	}
	if _, ok := err.(*MissingDiscError); !ok {
		t.Fatalf("expected *MissingDiscError, got %T: %v", err, err)
	}
}
