package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// compareRestoredTree compares srcDir, byte for byte including mode bits
// and symlink targets, against its restored copy under outDir. Restore
// recreates the source's own absolute path under outDir, so the restored
// root is outDir+srcDir.
func compareRestoredTree(t *testing.T, srcDir, outDir string) {
	t.Helper()
	restoredRoot := filepath.Join(outDir, srcDir)

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		got := filepath.Join(restoredRoot, rel)
		gotInfo, err := os.Lstat(got)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			wantTarget, err := os.Readlink(path)
			if err != nil {
				t.Fatal(err)
			}
			gotTarget, err := os.Readlink(got)
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				return nil
			}
			if wantTarget != gotTarget {
				t.Errorf("%s: symlink target %q, want %q", rel, gotTarget, wantTarget)
			}
			return nil
		}
		if info.Mode().Perm() != gotInfo.Mode().Perm() {
			t.Errorf("%s: mode %o, want %o", rel, gotInfo.Mode().Perm(), info.Mode().Perm())
		}
		if info.IsDir() {
			return nil
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		gotBytes, err := os.ReadFile(got)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		if !bytes.Equal(want, gotBytes) {
			t.Errorf("%s: restored content does not match source", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRestoreFromImageAfterStagingDeleted proves Restore needs only the
// disc tree: it deletes the staging directory before restoring, then
// compares the result byte for byte with the source fixture.
func TestRestoreFromImageAfterStagingDeleted(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	stagingDir, treeDir, snapID := buildFixtureTree(t, srcDir)

	if err := os.RemoveAll(stagingDir); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	if _, err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatal(err)
	}
	compareRestoredTree(t, srcDir, outDir)
}

// TestRestoreRejectsCorruptChunk corrupts one chunk's payload bytes and
// checks that Restore, with no healing, reports the file as not
// restored, with a content id mismatch, instead of writing bad data.
func TestRestoreRejectsCorruptChunk(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	base, err := findNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	chunkPath := findAChunkFile(t, base)
	flipByte(t, chunkPath, 70) // inside the payload, past the 64-byte header

	outDir := t.TempDir()
	rep, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !rep.Failed() {
		t.Fatal("expected Restore to report a failure on a corrupted chunk")
	}
	failed := problemsOf(rep, KindFile)
	if len(failed) == 0 {
		t.Fatal("expected a file problem naming the corrupted chunk")
	}
	for _, p := range failed {
		if !strings.Contains(p.Err.Error(), "content id does not verify") &&
			!strings.Contains(p.Err.Error(), "crc mismatch") {
			t.Fatalf("expected a content id or crc error, got: %v", p.Err)
		}
		if p.Path == "" {
			t.Fatal("the failed file has no path")
		}
	}
}

// findAChunkFile walks base/objects and returns the path of the first
// file whose magic_kind is CHUNK.
func findAChunkFile(t *testing.T, base string) string {
	t.Helper()
	var found string
	err := filepath.Walk(filepath.Join(base, "objects"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var h format.CommonHeader
		if err := h.Decode(data); err != nil {
			return nil
		}
		if h.MagicKind == format.MagicChunk {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("no chunk file found under objects/")
	}
	return found
}

// TestRestoreSkipsExistingPathWithoutOverwrite asserts that Restore
// leaves a pre-existing file alone and counts it skipped when
// WithOverwrite is not given, and replaces it when WithOverwrite(true)
// is given.
func TestRestoreSkipsExistingPathWithoutOverwrite(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	preexisting := filepath.Join(outDir, srcDir, "small.txt")
	if err := os.MkdirAll(filepath.Dir(preexisting), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preexisting, []byte("not the source content"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped() != 1 {
		t.Fatalf("skipped = %d, want 1", rep.Skipped())
	}
	got, err := os.ReadFile(preexisting)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "not the source content" {
		t.Fatalf("existing file was modified without WithOverwrite: %q", got)
	}

	rep, err = Restore(treeDir, snapID, outDir, WithOverwrite(true))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped() != 0 {
		t.Fatalf("skipped = %d, want 0 with WithOverwrite", rep.Skipped())
	}
	got, err = os.ReadFile(preexisting)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "not the source content" {
		t.Fatalf("WithOverwrite did not replace the existing file")
	}
}

// TestRestoreDamagedChunkLeavesNoFinalName checks the one write path of
// every restore mode: a file whose chunk is damaged gets no final name
// and no part file, while every other file is restored.
func TestRestoreDamagedChunkLeavesNoFinalName(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	base, err := findNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	flipByte(t, findAChunkFile(t, base), 70)

	outDir := t.TempDir()
	rep, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !rep.Failed() {
		t.Fatal("a damaged chunk must fail its file")
	}
	assertNoPathOfFailedFiles(t, rep, outDir, srcDir)
}

// TestRestoreMissingObjectLeavesNoFinalName is the same check for an
// object that no provided disc holds at all.
func TestRestoreMissingObjectLeavesNoFinalName(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	base, err := findNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(findAChunkFile(t, base)); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	rep, err := Restore(treeDir, snapID, outDir)
	if err == nil {
		t.Fatal("a removed object must report a missing disc")
	}
	if !rep.Failed() {
		t.Fatal("a removed object must fail its file")
	}
	assertNoPathOfFailedFiles(t, rep, outDir, srcDir)
}

// assertNoPathOfFailedFiles checks that every failed file is absent
// under its final name and under its part name, and that at least one
// other file of the fixture did restore.
func assertNoPathOfFailedFiles(t *testing.T, rep Report, outDir, srcDir string) {
	t.Helper()
	failed := problemsOf(rep, KindFile)
	if len(failed) == 0 {
		t.Fatal("no file problem reported")
	}
	for _, p := range failed {
		if _, err := os.Lstat(p.Path); err == nil {
			t.Fatalf("%s: the final name is in place after a failed file", p.Path)
		}
		part := filepath.Join(filepath.Dir(p.Path), "."+filepath.Base(p.Path)+partSuffix)
		if _, err := os.Lstat(part); err == nil {
			t.Fatalf("%s: a part file is left in the all-discs mode", part)
		}
	}
	good := filepath.Join(outDir, srcDir, "small.txt")
	if _, err := os.Lstat(good); err != nil {
		t.Fatalf("a good file was not restored: %v", err)
	}
}
