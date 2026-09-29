package image

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// readTree returns every file under root, keyed by relative path.
func readTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		files[rel] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestPackReadsObjectsThroughPathFunctions gives Pack two directories:
// chunks in one, snapshot, tree and blob objects in the other. The
// staging directory of the second pack holds no object. Pack must write
// the same run that it writes from the staging layout.
func TestPackReadsObjectsThroughPathFunctions(t *testing.T) {
	stagingDir, snapID := packFixture(t)
	objs, err := CollectReachable(testObjectPath(stagingDir), []object.ID{snapID})
	if err != nil {
		t.Fatal(err)
	}

	refLog, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, snapID, refLog)
	refOut := t.TempDir()
	if _, err := Pack(packOpts(stagingDir, snapID, refOut, sectorsFor(50_000_000), 1, refLog)); err != nil {
		t.Fatalf("reference pack: %v", err)
	}

	chunkDir, metaDir := t.TempDir(), t.TempDir()
	paths := make(map[object.ID]string, len(objs))
	for _, o := range objs {
		dst := filepath.Join(metaDir, o.ID.TextForm())
		if o.Kind == format.ObjectKindChunk {
			dst = filepath.Join(chunkDir, "c-"+o.ID.TextForm())
		}
		data, err := os.ReadFile(testObjectPath(stagingDir)(o.Kind, o.ID))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
		paths[o.ID] = dst
	}

	altStaging := t.TempDir()
	altLog, err := stage.Open(altStaging)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if err := altLog.EnsureStaged(o.ID); err != nil {
			t.Fatal(err)
		}
	}
	altOut := t.TempDir()
	opts := packOpts(altStaging, snapID, altOut, sectorsFor(50_000_000), 1, altLog)
	opts.ObjectPath = func(_ format.ObjectKind, id object.ID) string { return paths[id] }
	opts.SnapshotIDs = func() ([]object.ID, error) { return []object.ID{snapID}, nil }
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack through path functions: %v", err)
	}

	want, got := readTree(t, refOut), readTree(t, altOut)
	if len(want) != len(got) {
		t.Fatalf("run has %d files, want %d", len(got), len(want))
	}
	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Errorf("file %s differs from the staging layout run", name)
		}
	}
}
