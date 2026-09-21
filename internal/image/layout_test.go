package image

import (
	"bytes"
	mrand "math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/object"
)

func fixedClock() time.Time {
	return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
}

// stageFixture commits a small, deterministic source tree into a fresh
// staging directory under t.TempDir and returns the staging directory and
// the snapshot id.
func stageFixture(t *testing.T) (string, object.ID) {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("content of b, a bit longer so it is worth chunking on its own"), 0o644); err != nil {
		t.Fatal(err)
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

// stageMultiChunkFixture commits a source tree with a multi-megabyte
// pseudo-random file, large enough to split into several chunk objects
// and to cross more than one FEC stripe, plus a second file that
// duplicates a slice of the first so dedup is exercised too.
func stageMultiChunkFixture(t *testing.T, contentBytes int) (string, object.ID) {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of a"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, contentBytes)
	mrand.New(mrand.NewSource(42)).Read(big)
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), big[:200000], 0o644); err != nil {
		t.Fatal(err)
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

func testOpts(t *testing.T, stagingDir string, snapID object.ID, outDir string) BuildOptions {
	t.Helper()
	return BuildOptions{
		StagingDir:              stagingDir,
		Snapshots:               []SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors:   1 << 20, // generously large for a tiny fixture
		PhysicalCapacitySectors: 1 << 20,
		OutputDir:               outDir,
		RepoUUID:                [16]byte{1, 2, 3, 4},
		DiscUUID:                [16]byte{5, 6, 7, 8},
		Label:                   "test-disc",
		FECEnabled:              true,
		Now:                     fixedClock,
	}
}

func TestBuildAndRead(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)

	result, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.ObjectCount == 0 {
		t.Fatal("expected at least one object")
	}

	rr, err := Read(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if rr.ObjectsVerified != result.ObjectCount {
		t.Fatalf("read verified %d objects, build wrote %d", rr.ObjectsVerified, result.ObjectCount)
	}
	if rr.RunCopies != 2 {
		t.Fatalf("expected 2 run header copies, got %d", rr.RunCopies)
	}
	if rr.Run.RunSeq != 1 || rr.Run.DiscSeq != 0 {
		t.Fatalf("unexpected run identity: %+v", rr.Run)
	}
	if rr.Disc.DiscUUID != opts.DiscUUID {
		t.Fatal("disc uuid mismatch")
	}
}

func TestBuildWritesReadmeAndFormatTxt(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	if _, err := Build(opts); err != nil {
		t.Fatal(err)
	}

	formatOnDisk, err := os.ReadFile(filepath.Join(outDir, "NOAHSARK", "FORMAT.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatOnDisk, FormatTxt) {
		t.Fatal("FORMAT.txt on disk does not match the embedded fixed text")
	}

	readmeOnDisk, err := os.ReadFile(filepath.Join(outDir, "NOAHSARK", "README.txt"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(readmeOnDisk)
	if bytes.ContainsRune(readmeOnDisk, '{') {
		t.Fatal("README.txt still has an unsubstituted slot")
	}
	wantDiscUUID := uuidText(opts.DiscUUID)
	if !strings.Contains(readme, wantDiscUUID) {
		t.Fatalf("README.txt does not contain disc uuid %s", wantDiscUUID)
	}
	if !strings.Contains(readme, "label: test-disc") {
		t.Fatal("README.txt does not contain the disc label")
	}
	if !strings.Contains(readme, "k=231 data columns, m=23 parity columns") {
		t.Fatal("README.txt does not contain the fec_k/fec_m substitution")
	}
	for _, want := range []string{
		"/NOAHSARK/runs/<seq>/checksum.bin",
		"/NOAHSARK/runs/<seq>/parity/",
		"The run carries Reed-Solomon parity",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README.txt of a disc with parity does not contain %q", want)
		}
	}

	rr, err := Read(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if rr.ObjectsVerified == 0 {
		t.Fatal("expected at least one verified object")
	}
}

func TestBuildReadingFromOutputDirOrNoahsarkDir(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	if _, err := Build(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(outDir); err != nil {
		t.Fatalf("Read(outDir): %v", err)
	}
	if _, err := Read(filepath.Join(outDir, "NOAHSARK")); err != nil {
		t.Fatalf("Read(outDir/NOAHSARK): %v", err)
	}
}

func TestBuildRefusesZeroCapacity(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	opts.TargetCapacitySectors = 0
	if _, err := Build(opts); err == nil {
		t.Fatal("expected an error for zero target capacity")
	}
}

func TestBuildRefusesTooSmallCapacity(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	opts.TargetCapacitySectors = 1
	opts.PhysicalCapacitySectors = 1
	if _, err := Build(opts); err == nil {
		t.Fatal("expected an error for a too-small target capacity")
	}
}

// TestBuildStreamsMultiChunkContent builds a run over a fixture whose
// data crosses several chunk objects and several FEC stripes, streamed
// object by object rather than concatenated in memory, and checks Build
// twice over the same staged objects still produces byte-identical
// output, and that Read verifies the result.
func TestBuildStreamsMultiChunkContent(t *testing.T) {
	stagingDir, snapID := stageMultiChunkFixture(t, 3*1024*1024)

	outDir1 := t.TempDir()
	opts1 := testOpts(t, stagingDir, snapID, outDir1)
	opts1.TargetCapacitySectors = 1 << 22
	opts1.PhysicalCapacitySectors = 1 << 22
	if _, err := Build(opts1); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(outDir1); err != nil {
		t.Fatalf("Read: %v", err)
	}

	outDir2 := t.TempDir()
	opts2 := testOpts(t, stagingDir, snapID, outDir2)
	opts2.TargetCapacitySectors = 1 << 22
	opts2.PhysicalCapacitySectors = 1 << 22
	if _, err := Build(opts2); err != nil {
		t.Fatal(err)
	}

	compareTrees(t, outDir1, outDir2)
}

func TestBuildIsDeterministic(t *testing.T) {
	stagingDir, snapID := stageFixture(t)

	outDir1 := t.TempDir()
	opts1 := testOpts(t, stagingDir, snapID, outDir1)
	if _, err := Build(opts1); err != nil {
		t.Fatal(err)
	}

	outDir2 := t.TempDir()
	opts2 := testOpts(t, stagingDir, snapID, outDir2)
	if _, err := Build(opts2); err != nil {
		t.Fatal(err)
	}

	compareTrees(t, outDir1, outDir2)
}

// compareTrees fails the test unless a and b hold byte-identical files at
// every relative path.
func compareTrees(t *testing.T, a, b string) {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(a, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(a, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no files found to compare")
	}
	for _, rel := range paths {
		wantData, err := os.ReadFile(filepath.Join(a, rel))
		if err != nil {
			t.Fatal(err)
		}
		gotData, err := os.ReadFile(filepath.Join(b, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !bytes.Equal(wantData, gotData) {
			t.Fatalf("%s: byte mismatch between two Build runs over the same input", rel)
		}
	}
}

// TestBuildReadmeWithoutParity checks README.txt on a disc whose run
// carries no parity: it names no geometry, lists no checksum.bin and no
// parity/ directory, and sends the reader to another copy for a repair.
func TestBuildReadmeWithoutParity(t *testing.T) {
	stagingDir, snapID := stageFixture(t)
	outDir := t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	opts.FECEnabled = false
	if _, err := Build(opts); err != nil {
		t.Fatal(err)
	}

	readmeOnDisk, err := os.ReadFile(filepath.Join(outDir, "NOAHSARK", "README.txt"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(readmeOnDisk)
	if bytes.ContainsRune(readmeOnDisk, '{') {
		t.Fatal("README.txt still has an unsubstituted slot")
	}
	if !strings.Contains(readme, "\nparity: none\n") {
		t.Fatal("README.txt of a disc with no parity does not print \"parity: none\"")
	}
	for _, unwanted := range []string{
		"parity geometry",
		"/NOAHSARK/runs/<seq>/checksum.bin",
		"/NOAHSARK/runs/<seq>/parity/",
		"k=231",
	} {
		if strings.Contains(readme, unwanted) {
			t.Fatalf("README.txt of a disc with no parity names %q", unwanted)
		}
	}
	if !strings.Contains(readme, "This disc carries no parity") {
		t.Fatal("README.txt of a disc with no parity does not name the no-parity repair rule")
	}
	if strings.Contains(readme, "\n\n\n") {
		t.Fatal("README.txt has a blank line where the {parity_files} line was removed")
	}
}
