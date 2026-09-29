package restore

import (
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// A test repository keeps every file below one directory dir. A chunk
// object is dir/chunks/ab/<id>. A blob, tree or snapshot object is
// dir/<kind>/ab/<id>. The two ledgers are dir/discs.bin and
// dir/refslog.bin. The state log is dir/state.db.

// testKindDir names the directory of one object kind below a test
// repository.
func testKindDir(kind format.ObjectKind) string {
	switch kind {
	case format.ObjectKindChunk:
		return "chunks"
	case format.ObjectKindBlob:
		return "blob"
	case format.ObjectKindTree:
		return "tree"
	default:
		return "snapshot"
	}
}

// testObjectPath gives the object paths of the test repository dir.
func testObjectPath(dir string) image.ObjectPathFunc {
	return func(kind format.ObjectKind, id object.ID) string {
		return filepath.Join(dir, testKindDir(kind), id.FanoutByte(), id.TextForm())
	}
}

// testWriter returns a Writer that commits into the test repository dir.
func testWriter(dir string) *object.Writer {
	paths := testObjectPath(dir)
	return object.NewWriter(object.PathFunc(paths), object.PathFunc(paths))
}

// testStore gives every path of the test repository dir to Pack.
func testStore(dir string) image.Store {
	return image.Store{
		ObjectPath: testObjectPath(dir),
		SnapshotIDs: func() ([]object.ID, error) {
			files, err := filepath.Glob(filepath.Join(dir, testKindDir(format.ObjectKindSnapshot), "*", "*"))
			if err != nil {
				return nil, err
			}
			var ids []object.ID
			for _, f := range files {
				if id, err := object.ParseID(filepath.Base(f)); err == nil {
					ids = append(ids, id)
				}
			}
			return ids, nil
		},
		DiscsLedger: filepath.Join(dir, "discs.bin"),
		RefsLedger:  filepath.Join(dir, "refslog.bin"),
	}
}
