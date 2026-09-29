package object

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// commitFixture commits the tree at src with w and returns the snapshot id.
func commitFixture(t *testing.T, w *Writer, src string) ID {
	t.Helper()
	w.Now = fixedClock
	id, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func fixtureTree(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	buildFixture(t, src)
	return src
}

func TestPathFuncsReceiveTheirOwnKinds(t *testing.T) {
	root := t.TempDir()
	chunkKinds := map[format.ObjectKind]int{}
	metaKinds := map[format.ObjectKind]int{}
	w := testWriter(t.TempDir())
	w.ChunkPath = func(kind format.ObjectKind, id ID) string {
		chunkKinds[kind]++
		return filepath.Join(root, "chunks", id.TextForm())
	}
	w.MetaPath = func(kind format.ObjectKind, id ID) string {
		metaKinds[kind]++
		return filepath.Join(root, "meta", id.TextForm())
	}
	commitFixture(t, w, fixtureTree(t))

	if len(chunkKinds) != 1 || chunkKinds[format.ObjectKindChunk] == 0 {
		t.Fatalf("ChunkPath kinds = %v, want only chunk", chunkKinds)
	}
	for _, k := range []format.ObjectKind{format.ObjectKindBlob, format.ObjectKindTree, format.ObjectKindSnapshot} {
		if metaKinds[k] == 0 {
			t.Fatalf("MetaPath got no call for kind %d (%v)", k, metaKinds)
		}
	}
	if metaKinds[format.ObjectKindChunk] != 0 {
		t.Fatalf("MetaPath got a chunk call: %v", metaKinds)
	}
	if n := len(listFiles(t, filepath.Join(root, "chunks"))); n == 0 {
		t.Fatal("no chunk file under the chunk path")
	}
	if n := len(listFiles(t, filepath.Join(root, "meta"))); n == 0 {
		t.Fatal("no metadata file under the metadata path")
	}
}

// testChunkPath gives the path of a chunk object below dir, in a fan-out
// directory: dir/chunks/ab/<id>.
func testChunkPath(dir string) PathFunc {
	return func(_ format.ObjectKind, id ID) string {
		return filepath.Join(dir, "chunks", id.FanoutByte(), id.TextForm())
	}
}

// testMetaPath gives the path of a blob, tree or snapshot object below
// dir, in a fan-out directory: dir/meta/ab/<id>.
func testMetaPath(dir string) PathFunc {
	return func(_ format.ObjectKind, id ID) string {
		return filepath.Join(dir, "meta", id.FanoutByte(), id.TextForm())
	}
}

// testWriter returns a Writer that writes every object below dir, with
// testChunkPath and testMetaPath.
func testWriter(dir string) *Writer {
	return NewWriter(testChunkPath(dir), testMetaPath(dir))
}

func TestNewWriterUsesTheGivenPaths(t *testing.T) {
	dir := t.TempDir()
	w := testWriter(dir)
	snap := commitFixture(t, w, fixtureTree(t))

	wantSnap := filepath.Join(dir, "meta", snap.FanoutByte(), snap.TextForm())
	if _, err := os.Stat(wantSnap); err != nil {
		t.Fatal(err)
	}
	for _, rel := range listFiles(t, dir) {
		if !strings.HasPrefix(rel, "chunks"+string(filepath.Separator)) &&
			!strings.HasPrefix(rel, "meta"+string(filepath.Separator)) {
			t.Fatalf("file outside chunks and meta: %s", rel)
		}
	}
}

func TestPathFuncsDoNotChangeObjectBytes(t *testing.T) {
	staging := t.TempDir()
	def := testWriter(staging)
	src := fixtureTree(t)
	defSnap := commitFixture(t, def, src)

	root := t.TempDir()
	custom := testWriter(t.TempDir())
	custom.ChunkPath = func(_ format.ObjectKind, id ID) string {
		return filepath.Join(root, "c", id.TextForm())
	}
	custom.MetaPath = func(_ format.ObjectKind, id ID) string {
		return filepath.Join(root, "m", id.TextForm())
	}
	customSnap := commitFixture(t, custom, src)
	if defSnap != customSnap {
		t.Fatalf("snapshot id differs: %s vs %s", defSnap.TextForm(), customSnap.TextForm())
	}

	byName := func(dir string) map[string][]byte {
		m := map[string][]byte{}
		for _, rel := range listFiles(t, dir) {
			b, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				t.Fatal(err)
			}
			m[filepath.Base(rel)] = b
		}
		return m
	}
	want := byName(staging)
	got := byName(root)
	if len(want) != len(got) {
		t.Fatalf("object count = %d, want %d", len(got), len(want))
	}
	for name, b := range want {
		if !bytes.Equal(got[name], b) {
			t.Fatalf("bytes of %s differ", name)
		}
	}
}
