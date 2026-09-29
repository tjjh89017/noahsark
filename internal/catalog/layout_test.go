package catalog_test

import (
	"bytes"
	"errors"
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
	treeRaw, treeID := encodeTestTree(t)
	snapRaw, snapID := encodeTestSnapshot(t, treeID)
	blobRaw, blobID := encodeTestBlob(t)
	for _, tc := range []struct {
		kind format.ObjectKind
		dir  string
		raw  []byte
		id   object.ID
	}{
		{format.ObjectKindSnapshot, "snapshots", snapRaw, snapID},
		{format.ObjectKindTree, "trees", treeRaw, treeID},
		{format.ObjectKindBlob, "blobs", blobRaw, blobID},
	} {
		raw, id := tc.raw, tc.id
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

// TestWriteObjectRefusesADamagedCopy checks that WriteObject writes
// no file from bytes that do not give the id.
func TestWriteObjectRefusesADamagedCopy(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, id := encodeTestBlob(t, format.BlobEntry{Length: 5})
	bad := slices.Clone(raw)
	bad[len(bad)-1] ^= 0x01
	if err := c.WriteObject(format.ObjectKindBlob, id, bad); err == nil {
		t.Fatal("WriteObject of a damaged copy = nil, want an error")
	}
	if _, err := os.Stat(c.MetaPath(format.ObjectKindBlob, id)); !os.IsNotExist(err) {
		t.Fatalf("the damaged copy was written: %v", err)
	}
}

// TestWriteObjectRepairsADamagedFile damages a catalog object file in
// place, with no change of its size. A read reports the damage, and a
// write of a good copy repairs the file.
func TestWriteObjectRepairsADamagedFile(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, id := encodeTestBlob(t, format.BlobEntry{Length: 5})
	if err := c.WriteObject(format.ObjectKindBlob, id, raw); err != nil {
		t.Fatal(err)
	}
	path := c.MetaPath(format.ObjectKindBlob, id)
	bad := slices.Clone(raw)
	bad[len(bad)-1] ^= 0x01
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = c.ReadBlob(id)
	damaged, ok := errors.AsType[*catalog.DamagedObjectError](err)
	if !ok {
		t.Fatalf("ReadBlob of a damaged file: err %v, want a *catalog.DamagedObjectError", err)
	}
	if damaged.ID != id || !strings.Contains(err.Error(), id.TextForm()) || !strings.Contains(err.Error(), "run recover") {
		t.Fatalf("the error %q does not name the object and the repair", err)
	}

	if err := c.WriteObject(format.ObjectKindBlob, id, raw); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, raw) {
		t.Fatal("WriteObject kept the damaged file")
	}
	if _, err := c.ReadBlob(id); err != nil {
		t.Fatalf("ReadBlob after the repair: %v", err)
	}
	assertNoTempFile(t, c.Dir())
}

// TestReadChecksTheContentID writes a tree and a snapshot file whose
// headers are valid but whose content ids are not the ids of their
// names. Each read reports the damage and names the disc that holds the
// object.
func TestReadChecksTheContentID(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	treeRaw, treeID := encodeTestTree(t, format.TreeEntry{EntryType: format.EntryTypeRegular, Name: []byte("a"), Size: 1})
	otherTree, _ := encodeTestTree(t, format.TreeEntry{EntryType: format.EntryTypeRegular, Name: []byte("a"), Size: 2})
	_, snapID := encodeTestSnapshot(t, treeID)
	otherSnap, _ := encodeTestSnapshot(t, object.ID{1})

	holder := [16]byte{7, 7}
	idx := encodeTestIndex(t, 1, []format.IndexObjectRecord{{ContentID: treeID, Kind: format.ObjectKindTree}}, []uint64{uint64(len(treeRaw))}, nil)
	if err := c.WriteDisc(holder, idx, encodeTestRefs(t), encodeTestDiscs(t, []format.DiscsRow{{DiscUUID: holder}})); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		path string
		raw  []byte
	}{
		{c.MetaPath(format.ObjectKindTree, treeID), otherTree},
		{c.MetaPath(format.ObjectKindSnapshot, snapID), otherSnap},
	} {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path, f.raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err = c.ReadTree(treeID)
	damaged, ok := errors.AsType[*catalog.DamagedObjectError](err)
	if !ok {
		t.Fatalf("ReadTree: err %v, want a *catalog.DamagedObjectError", err)
	}
	if !damaged.HasDiscUUID || damaged.DiscUUID != holder {
		t.Fatalf("the error names disc %v (%v), want %v", damaged.DiscUUID, damaged.HasDiscUUID, holder)
	}
	if _, err := c.ReadSnapshot(snapID); !errors.As(err, &damaged) {
		t.Fatalf("ReadSnapshot: err %v, want a *catalog.DamagedObjectError", err)
	}
	if _, err := c.ReadTree(object.ID{9}); !os.IsNotExist(err) {
		t.Fatalf("ReadTree of a missing tree: err %v, want a not-exist error", err)
	}
}

// TestReadFileBlobChecksTheSize checks that ReadFileBlob refuses a tree
// entry whose size is not the sum of the chunk lengths of its blob.
func TestReadFileBlobChecksTheSize(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, id := encodeTestBlob(t, format.BlobEntry{ContentID: [32]byte{1}, Length: 5}, format.BlobEntry{ContentID: [32]byte{2}, Length: 7})
	if err := c.WriteObject(format.ObjectKindBlob, id, raw); err != nil {
		t.Fatal(err)
	}
	entry := format.TreeEntry{EntryType: format.EntryTypeRegular, ContentID: id, Size: 12}
	if _, err := c.ReadFileBlob(entry); err != nil {
		t.Fatalf("ReadFileBlob of a matching size: %v", err)
	}
	entry.Size = 12 + 1<<16
	_, err = c.ReadFileBlob(entry)
	if _, ok := errors.AsType[*catalog.FileSizeError](err); !ok {
		t.Fatalf("ReadFileBlob of a wrong size: err %v, want a *catalog.FileSizeError", err)
	}
}

// TestCompletenessRefusesAShortBlob checks that a blob file that is
// empty or short does not count as present.
func TestCompletenessRefusesAShortBlob(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blobRaw, blobID := encodeTestBlob(t, format.BlobEntry{ContentID: [32]byte{1}, Length: 5})
	treeRaw, treeID := encodeTestTree(t, format.TreeEntry{EntryType: format.EntryTypeRegular, Name: []byte("f"), ContentID: blobID, Size: 5})
	snapRaw, snapID := encodeTestSnapshot(t, treeID)
	for _, o := range []struct {
		kind format.ObjectKind
		id   object.ID
		raw  []byte
	}{
		{format.ObjectKindBlob, blobID, blobRaw},
		{format.ObjectKindTree, treeID, treeRaw},
		{format.ObjectKindSnapshot, snapID, snapRaw},
	} {
		if err := c.WriteObject(o.kind, o.id, o.raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.RefreshComplete(snapID); err != nil || !c.Complete(snapID) {
		t.Fatalf("the whole snapshot is not complete: %v", err)
	}
	path := c.MetaPath(format.ObjectKindBlob, blobID)
	for _, short := range [][]byte{nil, blobRaw[:len(blobRaw)-1]} {
		if err := os.WriteFile(path, short, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := c.RefreshComplete(snapID); err != nil {
			t.Fatal(err)
		}
		if !c.Partial(snapID) {
			t.Fatalf("a blob file of %d bytes counts as present", len(short))
		}
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
	// The catalog does not hold the snapshot object of first.
	if err := c.RefreshComplete(first); err != nil {
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
