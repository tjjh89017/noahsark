package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
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
	if err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatal(err)
	}
	compareRestoredTree(t, srcDir, outDir)
}

// TestRestoreRejectsCorruptChunk corrupts one chunk's payload bytes and
// checks that Restore, with no healing, fails with a content id
// mismatch instead of writing bad data.
func TestRestoreRejectsCorruptChunk(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	base, err := findNoahsark(treeDir)
	if err != nil {
		t.Fatal(err)
	}
	chunkPath := findAChunkFile(t, base)
	flipByte(t, chunkPath, 70) // inside the payload, past the 64-byte header

	outDir := t.TempDir()
	err = Restore(treeDir, snapID, outDir)
	if err == nil {
		t.Fatal("expected Restore to fail on a corrupted chunk")
	}
	if !strings.Contains(err.Error(), "content id does not verify") &&
		!strings.Contains(err.Error(), "crc mismatch") {
		t.Fatalf("expected a content id or crc error, got: %v", err)
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
