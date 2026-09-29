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
	w := NewWriter(t.TempDir())
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

func TestDefaultPathsMatchStagingLayout(t *testing.T) {
	staging := t.TempDir()
	w := NewWriter(staging)
	snap := commitFixture(t, w, fixtureTree(t))

	id := ComputeID(format.ObjectKindChunk, []byte("x"))
	wantObj := filepath.Join(staging, "objects", id.FanoutByte(), id.TextForm())
	for _, k := range []format.ObjectKind{format.ObjectKindChunk, format.ObjectKindBlob, format.ObjectKindTree} {
		fn := w.ChunkPath
		if k != format.ObjectKindChunk {
			fn = w.MetaPath
		}
		if got := fn(k, id); got != wantObj {
			t.Fatalf("kind %d path = %s, want %s", k, got, wantObj)
		}
	}
	wantSnap := filepath.Join(staging, "snapshots", snap.TextForm())
	if got := w.MetaPath(format.ObjectKindSnapshot, snap); got != wantSnap {
		t.Fatalf("snapshot path = %s, want %s", got, wantSnap)
	}
	if _, err := os.Stat(wantSnap); err != nil {
		t.Fatal(err)
	}
	for _, rel := range listFiles(t, staging) {
		if !strings.HasPrefix(rel, "objects"+string(filepath.Separator)) &&
			!strings.HasPrefix(rel, "snapshots"+string(filepath.Separator)) {
			t.Fatalf("file outside objects and snapshots: %s", rel)
		}
	}
}

func TestPathFuncsDoNotChangeObjectBytes(t *testing.T) {
	staging := t.TempDir()
	def := NewWriter(staging)
	src := fixtureTree(t)
	defSnap := commitFixture(t, def, src)

	root := t.TempDir()
	custom := NewWriter(t.TempDir())
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
