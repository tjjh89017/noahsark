package catalog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// testDiscUUID has upper hex digits, so the path shows the lower case.
var testDiscUUID = [16]byte{0xAB, 0xCD, 0xEF, 0x01, 0x23, 0x45, 0x67, 0x89, 0x0A, 0xBC, 0xDE, 0xF0, 0x12, 0x34, 0x56, 0x78}

// TestMetaPathLayout checks the path of each metadata object kind.
func TestMetaPathLayout(t *testing.T) {
	repo := t.TempDir()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID(format.ObjectKindTree, []byte("layout"))
	text := id.TextForm()
	fanout := id.FanoutByte()
	if len(fanout) != 2 || !strings.HasPrefix(text[4:], fanout) {
		t.Fatalf("fan-out %q is not the first two hex digits of the digest of %s", fanout, text)
	}
	cases := []struct {
		kind format.ObjectKind
		want string
	}{
		{format.ObjectKindSnapshot, filepath.Join(repo, "catalog", "snapshots", text)},
		{format.ObjectKindTree, filepath.Join(repo, "catalog", "trees", fanout, text)},
		{format.ObjectKindBlob, filepath.Join(repo, "catalog", "blobs", fanout, text)},
		{format.ObjectKindChunk, ""},
	}
	for _, tc := range cases {
		if got := c.MetaPath(tc.kind, id); got != tc.want {
			t.Errorf("MetaPath(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// TestWriteObjectPaths writes one object of each kind and checks the
// file of each at its path.
func TestWriteObjectPaths(t *testing.T) {
	repo := t.TempDir()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind format.ObjectKind
		dir  string
	}{
		{format.ObjectKindSnapshot, "snapshots"},
		{format.ObjectKindTree, "trees"},
		{format.ObjectKindBlob, "blobs"},
	} {
		raw := []byte("object bytes of " + tc.dir)
		id := object.ComputeID(tc.kind, raw)
		if err := c.WriteObject(tc.kind, id, raw); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(repo, "catalog", tc.dir, id.FanoutByte(), id.TextForm())
		if tc.kind == format.ObjectKindSnapshot {
			want = filepath.Join(repo, "catalog", tc.dir, id.TextForm())
		}
		got, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("%s: %v", tc.dir, err)
		}
		if string(got) != string(raw) {
			t.Fatalf("%s: file holds %q, want %q", tc.dir, got, raw)
		}
	}
	chunk := object.ComputeID(format.ObjectKindChunk, []byte("chunk"))
	if err := c.WriteObject(format.ObjectKindChunk, chunk, []byte("chunk")); err == nil {
		t.Fatal("WriteObject of a chunk = nil, want an error")
	}
	assertNoTempFile(t, repo)
}

// TestWriteObjectKeepsAFileOfTheSameSize checks that WriteObject does
// not write when the file exists with the same size.
func TestWriteObjectKeepsAFileOfTheSameSize(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID(format.ObjectKindBlob, []byte("blob"))
	if err := c.WriteObject(format.ObjectKindBlob, id, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteObject(format.ObjectKindBlob, id, []byte("other")); err != nil {
		t.Fatal(err)
	}
	path := c.MetaPath(format.ObjectKindBlob, id)
	if got, _ := os.ReadFile(path); string(got) != "first" {
		t.Fatalf("file holds %q after a write of the same size, want %q", got, "first")
	}
	if err := c.WriteObject(format.ObjectKindBlob, id, []byte("longer bytes")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "longer bytes" {
		t.Fatalf("file holds %q after a write of another size, want %q", got, "longer bytes")
	}
}

// TestWriteDiscPaths checks the paths of the three tables of a disc and
// the uuid form of the directory name.
func TestWriteDiscPaths(t *testing.T) {
	repo := t.TempDir()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDisc(testDiscUUID, []byte("index"), []byte("refs"), []byte("discs")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, "catalog", "discs", "abcdef01-2345-6789-0abc-def012345678")
	for name, want := range map[string]string{"INDEX.bin": "index", "REFS.bin": "refs", "DISCS.bin": "discs"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s holds %q, want %q", name, got, want)
		}
	}
	assertNoTempFile(t, repo)
}

// TestRemoveDiscKeepsTheOtherDiscs removes the tables of one disc and
// checks that the tables of the other disc stay.
func TestRemoveDiscKeepsTheOtherDiscs(t *testing.T) {
	repo := t.TempDir()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	keep := [16]byte{0x11}
	for _, uuid := range [][16]byte{keep, testDiscUUID} {
		if err := c.WriteDisc(uuid, []byte("index"), []byte("refs"), []byte("discs")); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.RemoveDisc(testDiscUUID); err != nil {
		t.Fatal(err)
	}
	discs := filepath.Join(repo, "catalog", "discs")
	if _, err := os.Stat(filepath.Join(discs, "abcdef01-2345-6789-0abc-def012345678")); !os.IsNotExist(err) {
		t.Fatalf("the removed disc directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(discs, "11000000-0000-0000-0000-000000000000", "INDEX.bin")); err != nil {
		t.Fatalf("the other disc lost its INDEX: %v", err)
	}
	if err := c.RemoveDisc(testDiscUUID); err != nil {
		t.Fatalf("RemoveDisc of an absent disc: %v", err)
	}
}

// TestDiscsHolding checks the lookup over the INDEX tables.
func TestDiscsHolding(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	discA := [16]byte{0xaa}
	discB := [16]byte{0xbb}
	shared := object.ComputeID(format.ObjectKindChunk, []byte("on both discs"))
	onlyB := object.ComputeID(format.ObjectKindChunk, []byte("on disc B"))
	prereq := object.ComputeID(format.ObjectKindChunk, []byte("a prereq row only"))

	idxA := encodeTestIndex(t, 1, []format.IndexObjectRecord{{ContentID: shared, Kind: format.ObjectKindChunk}}, []uint64{10}, nil)
	if err := c.WriteDisc(discA, idxA, encodeTestRefs(t), encodeTestDiscs(t, []format.DiscsRow{{RunSeq: 1, DiscUUID: discA}})); err != nil {
		t.Fatal(err)
	}
	idxB := encodeTestIndex(t, 2, []format.IndexObjectRecord{
		{ContentID: shared, Kind: format.ObjectKindChunk},
		{ContentID: onlyB, Kind: format.ObjectKindChunk},
	}, []uint64{10, 20}, []format.IndexPrereqRecord{{ContentID: prereq, DiscUUID: discA}})
	if err := c.WriteDisc(discB, idxB, encodeTestRefs(t), encodeTestDiscs(t, []format.DiscsRow{{RunSeq: 2, DiscUUID: discB}})); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		id   object.ID
		want [][16]byte
	}{
		{"shared", shared, [][16]byte{discA, discB}},
		{"only disc B", onlyB, [][16]byte{discB}},
		{"a prereq row is not a holder", prereq, nil},
	} {
		got, err := c.DiscsHolding(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: DiscsHolding = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestCatalogStateFile checks the path, the content and the order of
// catalog-state.txt, and that a new Open reads it back.
func TestCatalogStateFile(t *testing.T) {
	repo := t.TempDir()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	a := object.ComputeID(format.ObjectKindSnapshot, []byte("snapshot a"))
	b := object.ComputeID(format.ObjectKindSnapshot, []byte("snapshot b"))
	first, second := a, b
	if first.TextForm() > second.TextForm() {
		first, second = second, first
	}
	if err := c.MarkComplete(second); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkPartial(first); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(repo, "state", "catalog-state.txt")
	if got := catalog.StatePath(repo); got != path {
		t.Fatalf("StatePath = %q, want %q", got, path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := first.TextForm() + " partial\n" + second.TextForm() + " complete\n"
	if string(got) != want {
		t.Fatalf("catalog-state.txt = %q, want %q", got, want)
	}
	assertNoTempFile(t, repo)

	again, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Complete(second) || again.Partial(second) {
		t.Fatal("the complete snapshot does not read back as complete")
	}
	if again.Complete(first) || !again.Partial(first) {
		t.Fatal("the partial snapshot does not read back as partial")
	}
	unknown := object.ComputeID(format.ObjectKindSnapshot, []byte("not listed"))
	if again.Complete(unknown) || again.Partial(unknown) {
		t.Fatal("a snapshot that the file does not list reads as listed")
	}

	if err := again.MarkComplete(first); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want = first.TextForm() + " complete\n" + second.TextForm() + " complete\n"
	if string(got) != want {
		t.Fatalf("catalog-state.txt after a partial snapshot becomes complete = %q, want %q", got, want)
	}
}

// TestCatalogStateFileRejectsAnUnknownMark checks that Open refuses a
// line whose mark is not complete or partial.
func TestCatalogStateFileRejectsAnUnknownMark(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID(format.ObjectKindSnapshot, []byte("snapshot"))
	if err := os.WriteFile(catalog.StatePath(repo), []byte(id.TextForm()+" incomplete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Open(repo); err == nil {
		t.Fatal("Open = nil, want an error for the mark incomplete")
	}
}

// assertNoTempFile fails when a temporary file of an atomic write stays
// below root.
func assertNoTempFile(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("temporary file left: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// countFiles counts the regular files below root.
func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
