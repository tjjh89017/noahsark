package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// packOneOfEachKind packs packFixture's snapshot onto one disc, large
// enough to hold everything on one disc, and returns the disc root and
// the packed run's INDEX, so a test can find one object row of each
// kind.
func packOneOfEachKind(t *testing.T) (discRoot string, idx *format.Index) {
	t.Helper()
	stagingDir, snapID := packFixture(t)
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, l)

	outDir := t.TempDir()
	opts := packOpts(stagingDir, snapID, outDir, sectorsFor(50_000_000), 1, l)
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	runDir, err := NewestRunDir(filepath.Join(outDir, "NOAHSARK", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	idxBuf, err := os.ReadFile(filepath.Join(runDir, "INDEX.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded format.Index
	if _, err := decoded.Decode(idxBuf); err != nil {
		t.Fatal(err)
	}
	return outDir, &decoded
}

// objectRowOfKind returns the first Objects row of kind, failing the
// test when the fixture carries none: a test that means to damage a
// blob, tree or snapshot object must know one exists.
func objectRowOfKind(t *testing.T, idx *format.Index, kind format.ObjectKind) format.IndexObjectRecord {
	t.Helper()
	for _, row := range idx.Objects {
		if row.Kind == kind {
			return row
		}
	}
	t.Fatalf("fixture carries no object of kind %d", kind)
	return format.IndexObjectRecord{}
}

// damageHeaderByte flips one bit at offset 40 of the file at path, inside
// the object header's payload_len field, a byte header_crc32c covers. It
// fails the test if the flip is a no-op (the file was too short).
func damageHeaderByte(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 40 {
		t.Fatalf("fixture object %s is too short to damage at offset 40", path)
	}
	data[40] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyRefusesAHeaderCRCMismatch damages one header byte, covered
// by header_crc32c, of an on-disc object of each kind (chunk, blob, tree,
// snapshot). Read (what verify calls) must refuse the disc and name the
// damaged object; a disc that reads clean before the damage must not
// read clean after it.
func TestVerifyRefusesAHeaderCRCMismatch(t *testing.T) {
	kinds := []struct {
		name string
		kind format.ObjectKind
	}{
		{"chunk", format.ObjectKindChunk},
		{"blob", format.ObjectKindBlob},
		{"tree", format.ObjectKindTree},
		{"snapshot", format.ObjectKindSnapshot},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			discRoot, idx := packOneOfEachKind(t)
			base := filepath.Join(discRoot, "NOAHSARK")

			if _, err := Read(discRoot); err != nil {
				t.Fatalf("Read before damage: %v", err)
			}

			row := objectRowOfKind(t, idx, k.kind)
			paths, err := ObjectPaths(base, idx, NewNameCache())
			if err != nil {
				t.Fatal(err)
			}
			var path string
			for i, r := range idx.Objects {
				if r.ContentID == row.ContentID {
					path = paths[i]
				}
			}
			if path == "" {
				t.Fatal("could not resolve the damaged object's path")
			}
			damageHeaderByte(t, path)

			id := object.ID(row.ContentID)
			_, err = Read(discRoot)
			if err == nil {
				t.Fatalf("Read after damaging the %s header: want an error, got none", k.name)
			}
			if !strings.Contains(err.Error(), id.TextForm()) {
				t.Fatalf("Read error = %q, want it to name the damaged object %s", err, id.TextForm())
			}
		})
	}
}

// The matching restore-side check, that Restore also refuses the same
// damage, lives in internal/restore (TestVerifyAndRestoreAgreeOnAHeaderCRCMismatch),
// since restore imports image and a test here cannot import restore back
// without a cycle.
