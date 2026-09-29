package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
)

// restoreTree restores snapID from the one disc root treeDir into
// outDir: it builds the catalog of the disc, selects paths, and walks
// the selection against the disc.
func restoreTree(t *testing.T, treeDir string, snapID object.ID, outDir string, overwrite bool, paths ...string) (Report, error) {
	t.Helper()
	return restoreWith(t, catalogOfTree(t, treeDir), treeDir, snapID, outDir, overwrite, paths...)
}

// restoreWith is restoreTree with the catalog c, which a test builds
// before it damages the disc.
func restoreWith(t *testing.T, c *catalog.Catalog, treeDir string, snapID object.ID, outDir string, overwrite bool, paths ...string) (Report, error) {
	t.Helper()
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		return Report{}, err
	}
	sel, err := plan.Select(c, snap, paths)
	if err != nil {
		return Report{}, err
	}
	a, err := NewAssembler(c, sel, outDir, overwrite)
	if err != nil {
		return Report{}, err
	}
	defer a.Close()
	if err := a.Disc(&treeDisc{root: treeDir}, nil); err != nil {
		return a.Report(), err
	}
	err = a.Finish()
	return a.Report(), err
}

// compareRestoredTree compares srcDir, byte for byte including mode bits
// and symlink targets, against its restored copy in outDir. The content
// of the one source root goes directly into outDir.
func compareRestoredTree(t *testing.T, srcDir, outDir string) {
	t.Helper()
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		got := filepath.Join(outDir, rel)
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
		if rel != "." && info.Mode().Perm() != gotInfo.Mode().Perm() {
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

// TestRestoreFromImageAfterStagingDeleted proves a restore needs only
// the catalog and the disc: it deletes the staging directory before it
// restores, then compares the result byte for byte with the source.
func TestRestoreFromImageAfterStagingDeleted(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	stagingDir, treeDir, snapID := buildFixtureTree(t, srcDir)

	if err := os.RemoveAll(stagingDir); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	if _, err := restoreTree(t, treeDir, snapID, outDir, false); err != nil {
		t.Fatal(err)
	}
	compareRestoredTree(t, srcDir, outDir)
}

// TestRestoreRejectsCorruptChunk corrupts one chunk's payload bytes and
// checks that the restore reports the file as not restored, with a
// content id mismatch, instead of writing bad data.
func TestRestoreRejectsCorruptChunk(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	base, err := findNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	c := catalogOfTree(t, treeDir)
	flipByte(t, findAChunkFile(t, base), 70) // inside the payload, past the 64-byte header

	outDir := t.TempDir()
	rep, err := restoreWith(t, c, treeDir, snapID, outDir, false)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !rep.Failed() {
		t.Fatal("expected the restore to report a failure on a corrupted chunk")
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

// TestRestoreSkipsExistingPathWithoutOverwrite asserts that a restore
// leaves a file that is already there alone and counts it skipped
// without overwrite, and replaces it with overwrite.
func TestRestoreSkipsExistingPathWithoutOverwrite(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	preexisting := filepath.Join(outDir, "small.txt")
	if err := os.WriteFile(preexisting, []byte("not the source content"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := restoreTree(t, treeDir, snapID, outDir, false)
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
		t.Fatalf("existing file was modified without overwrite: %q", got)
	}

	rep, err = restoreTree(t, treeDir, snapID, outDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped() != 0 {
		t.Fatalf("skipped = %d, want 0 with overwrite", rep.Skipped())
	}
	got, err = os.ReadFile(preexisting)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "not the source content" {
		t.Fatalf("overwrite did not replace the existing file")
	}
}

// TestRestoreDamagedChunkLeavesNoFinalName checks that a file whose
// chunk is damaged gets no final name, while every other file is
// restored.
func TestRestoreDamagedChunkLeavesNoFinalName(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	base, err := findNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	c := catalogOfTree(t, treeDir)
	flipByte(t, findAChunkFile(t, base), 70)

	outDir := t.TempDir()
	rep, err := restoreWith(t, c, treeDir, snapID, outDir, false)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !rep.Failed() {
		t.Fatal("a damaged chunk must fail its file")
	}
	assertNoFinalNameOfFailedFiles(t, rep, outDir)
}

// TestRestoreMissingObjectLeavesNoFinalName is the same check for a
// chunk that the INDEX of the disc lists and the disc does not hold.
func TestRestoreMissingObjectLeavesNoFinalName(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	base, err := findNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	c := catalogOfTree(t, treeDir)
	if err := os.Remove(findAChunkFile(t, base)); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	rep, err := restoreWith(t, c, treeDir, snapID, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Failed() {
		t.Fatal("a removed object must fail its file")
	}
	assertNoFinalNameOfFailedFiles(t, rep, outDir)
}

// assertNoFinalNameOfFailedFiles checks that every failed file is absent
// under its final name, and that at least one other file of the fixture
// did restore.
func assertNoFinalNameOfFailedFiles(t *testing.T, rep Report, outDir string) {
	t.Helper()
	failed := problemsOf(rep, KindFile)
	if len(failed) == 0 {
		t.Fatal("no file problem reported")
	}
	for _, p := range failed {
		if _, err := os.Lstat(p.Path); err == nil {
			t.Fatalf("%s: the final name is in place after a failed file", p.Path)
		}
	}
	good := filepath.Join(outDir, "small.txt")
	if _, err := os.Lstat(good); err != nil {
		t.Fatalf("a good file was not restored: %v", err)
	}
}

// TestRestorePaths checks the PATH rule: a path without a trailing slash
// makes the entry in the destination, a path with one puts the content
// of a directory into the destination, and a path that the snapshot
// does not hold is a *plan.PathError before anything is written.
func TestRestorePaths(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	t.Run("one file", func(t *testing.T) {
		outDir := t.TempDir()
		if _, err := restoreTree(t, treeDir, snapID, outDir, false, "sub/big2.bin"); err != nil {
			t.Fatal(err)
		}
		compareFileBytes(t, filepath.Join(outDir, "big2.bin"), filepath.Join(srcDir, "sub", "big2.bin"))
		mustHoldOnly(t, outDir, "big2.bin")
	})
	t.Run("a directory", func(t *testing.T) {
		outDir := t.TempDir()
		if _, err := restoreTree(t, treeDir, snapID, outDir, false, "sub"); err != nil {
			t.Fatal(err)
		}
		compareFileBytes(t, filepath.Join(outDir, "sub", "big2.bin"), filepath.Join(srcDir, "sub", "big2.bin"))
		mustHoldOnly(t, outDir, "sub")
	})
	t.Run("the content of a directory", func(t *testing.T) {
		outDir := t.TempDir()
		if _, err := restoreTree(t, treeDir, snapID, outDir, false, "sub/"); err != nil {
			t.Fatal(err)
		}
		compareFileBytes(t, filepath.Join(outDir, "big2.bin"), filepath.Join(srcDir, "sub", "big2.bin"))
		mustHoldOnly(t, outDir, "big2.bin")
	})
	t.Run("two paths", func(t *testing.T) {
		outDir := t.TempDir()
		if _, err := restoreTree(t, treeDir, snapID, outDir, false, "small.txt", "link-to-small"); err != nil {
			t.Fatal(err)
		}
		mustHoldOnly(t, outDir, "link-to-small", "small.txt")
	})
	for _, bad := range []string{"no-such", "sub/no-such", "small.txt/", "small.txt/x", "/"} {
		t.Run("not held "+bad, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "out")
			_, err := restoreTree(t, treeDir, snapID, outDir, false, bad)
			if _, ok := err.(*plan.PathError); !ok {
				t.Fatalf("err %v, want a *plan.PathError", err)
			}
			if _, err := os.Lstat(outDir); !os.IsNotExist(err) {
				t.Fatalf("the destination exists after a refused path: %v", err)
			}
		})
	}
}

// mustHoldOnly fails unless dir holds exactly names.
func mustHoldOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("%s holds %v, want %v", dir, got, names)
	}
}
