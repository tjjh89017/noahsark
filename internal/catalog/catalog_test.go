package catalog_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// TestDirIsCatalogInsideTheRepository checks that the catalog directory
// is always "catalog" inside the repository directory.
func TestDirIsCatalogInsideTheRepository(t *testing.T) {
	want := filepath.Join("/repo", "catalog")
	if got := catalog.Dir("/repo"); got != want {
		t.Fatalf("Dir(%q) = %q, want %q", "/repo", got, want)
	}
}

// TestOpenReadOnlyCreatesNothing checks that OpenReadOnly of a
// repository with no catalog directory creates no directory, and that
// Open creates it.
func TestOpenReadOnlyCreatesNothing(t *testing.T) {
	repo := t.TempDir()
	if _, err := catalog.OpenReadOnly(repo); err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	if _, err := os.Stat(catalog.Dir(repo)); !os.IsNotExist(err) {
		t.Fatalf("catalog directory after OpenReadOnly: %v, want none", err)
	}
	if _, err := catalog.Open(repo); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if fi, err := os.Stat(catalog.Dir(repo)); err != nil || !fi.IsDir() {
		t.Fatalf("catalog directory after Open: %v", err)
	}
}

// TestWriteFromRootPopulatesCatalog packs one run and copies it into the
// catalog, then checks every read method resolves the same content.
func TestWriteFromRootPopulatesCatalog(t *testing.T) {
	runRoot, snapID := buildFixtureRun(t)

	rr, err := image.Read(runRoot)
	if err != nil {
		t.Fatal(err)
	}

	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.WriteFromRoot(c, runRoot); err != nil {
		t.Fatalf("WriteFromRoot: %v", err)
	}

	ids, err := c.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != snapID {
		t.Fatalf("ListSnapshots = %v, want [%s]", ids, snapID.TextForm())
	}

	idx, err := c.IndexForDisc(rr.Disc.DiscUUID)
	if err != nil {
		t.Fatalf("IndexForDisc: %v", err)
	}
	if len(idx.Objects) != len(rr.Index.Objects) {
		t.Fatalf("catalog INDEX object count = %d, want %d", len(idx.Objects), len(rr.Index.Objects))
	}

	refs, err := c.Refs()
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if len(refs.Records) != len(rr.Refs.Records) {
		t.Fatalf("catalog REFS record count = %d, want %d", len(refs.Records), len(rr.Refs.Records))
	}

	discs, err := c.Discs()
	if err != nil {
		t.Fatalf("Discs: %v", err)
	}
	if len(discs.Rows) != len(rr.Discs.Rows) {
		t.Fatalf("catalog DISCS row count = %d, want %d", len(discs.Rows), len(rr.Discs.Rows))
	}

	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}

	tree, err := c.ReadTree(object.ID(snap.RootTree))
	if err != nil {
		t.Fatalf("ReadTree(root): %v", err)
	}
	if len(tree.Entries) == 0 {
		t.Fatal("root tree has no entries")
	}

	if !c.Complete(snapID) {
		t.Fatal("Complete = false, want true")
	}
	if err := c.CheckComplete(snapID); err != nil {
		t.Fatalf("CheckComplete: %v", err)
	}
}

// TestWriteTablesFromRootCopiesOnlyTheTables copies the tables of one
// run into an empty catalog, and checks that the catalog holds the
// three tables of the disc and no object.
func TestWriteTablesFromRootCopiesOnlyTheTables(t *testing.T) {
	runRoot, snapID := buildFixtureRun(t)
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rr, err := catalog.WriteTablesFromRoot(c, runRoot)
	if err != nil {
		t.Fatalf("WriteTablesFromRoot: %v", err)
	}
	if _, err := c.IndexForDisc(rr.Disc.DiscUUID); err != nil {
		t.Fatalf("IndexForDisc: %v", err)
	}
	if _, err := c.Refs(); err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if ids, err := c.ListSnapshots(); err != nil || len(ids) != 0 {
		t.Fatalf("ListSnapshots = %v, %v; want no snapshot", ids, err)
	}
	if _, err := c.ReadSnapshot(snapID); err == nil {
		t.Fatal("the catalog holds the snapshot object; want the tables only")
	}
}

// TestWriteFromRootIsIdempotent runs WriteFromRoot twice over the same
// run and checks the catalog ends up with the same content, not
// duplicated or corrupted entries.
func TestWriteFromRootIsIdempotent(t *testing.T) {
	runRoot, snapID := buildFixtureRun(t)
	dir := t.TempDir()

	c, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.WriteFromRoot(c, runRoot); err != nil {
		t.Fatal(err)
	}
	treesBefore := countFiles(t, filepath.Join(dir, "catalog", "trees"))

	c2, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.WriteFromRoot(c2, runRoot); err != nil {
		t.Fatal(err)
	}
	if treesAfter := countFiles(t, filepath.Join(dir, "catalog", "trees")); treesAfter != treesBefore {
		t.Fatalf("tree count after second write = %d, want %d", treesAfter, treesBefore)
	}
	if !c2.Complete(snapID) {
		t.Fatal("Complete = false after second write, want true")
	}
}

// TestWriteFromRootRepairsADamagedObject flips one bit of the size of
// a tree entry in the catalog. The file keeps its size. A read of the
// tree reports the damage, and a second copy of the run repairs it.
func TestWriteFromRootRepairsADamagedObject(t *testing.T) {
	runRoot, snapID := buildFixtureRun(t)
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.WriteFromRoot(c, runRoot); err != nil {
		t.Fatal(err)
	}
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	rootID := object.ID(snap.RootTree)
	path := c.MetaPath(format.ObjectKindTree, rootID)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := bytes.Clone(good)
	bad[len(bad)-1] ^= 0x01
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadTree(rootID); err == nil {
		t.Fatal("ReadTree of a damaged tree = nil, want an error")
	}

	if _, err := catalog.WriteFromRoot(c, runRoot); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, good) {
		t.Fatal("WriteFromRoot kept the damaged tree")
	}
	if _, err := c.ReadTree(rootID); err != nil {
		t.Fatalf("ReadTree after the repair: %v", err)
	}
}

// TestCheckCompleteReportsMissingTree builds a catalog holding a
// snapshot whose root tree references a child tree the catalog never
// received, and checks CheckComplete reports an *PartialError
// naming that child.
func TestCheckCompleteReportsMissingTree(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	missingID := object.ComputeID(format.ObjectKindChunk, []byte("a tree that is never written to the catalog"))
	rootTree, rootID := encodeTestTree(t, format.TreeEntry{
		EntryType: format.EntryTypeDirectory,
		Name:      []byte("child"),
		ContentID: missingID,
	})
	if err := c.WriteObject(format.ObjectKindTree, rootID, rootTree); err != nil {
		t.Fatal(err)
	}

	snapBuf, snapID := encodeTestSnapshot(t, rootID)
	if err := c.WriteObject(format.ObjectKindSnapshot, snapID, snapBuf); err != nil {
		t.Fatal(err)
	}

	err = c.CheckComplete(snapID)
	if err == nil {
		t.Fatal("CheckComplete = nil, want a *PartialError")
	}
	partial, ok := err.(*catalog.PartialError)
	if !ok {
		t.Fatalf("CheckComplete error type = %T, want *catalog.PartialError", err)
	}
	if partial.Snapshot != snapID {
		t.Fatalf("PartialError.Snapshot = %s, want %s", partial.Snapshot.TextForm(), snapID.TextForm())
	}
	if partial.MissingObject != missingID {
		t.Fatalf("PartialError.MissingObject = %s, want %s", partial.MissingObject.TextForm(), missingID.TextForm())
	}
	if partial.HasDiscUUID {
		t.Fatalf("PartialError resolved a disc it should not have: %+v", partial)
	}
	if c.Complete(snapID) {
		t.Fatal("Complete = true, want false")
	}
}

// TestCheckCompleteReportsMissingBlob builds a catalog that holds a
// snapshot and its root tree, but not the blob of a file of the tree.
// The snapshot is partial, and CheckComplete names the blob.
func TestCheckCompleteReportsMissingBlob(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	missingID := object.ComputeID(format.ObjectKindBlob, []byte("a blob that is never written to the catalog"))
	rootTree, rootID := encodeTestTree(t, format.TreeEntry{
		EntryType: format.EntryTypeRegular,
		Name:      []byte("file"),
		ContentID: missingID,
	})
	if err := c.WriteObject(format.ObjectKindTree, rootID, rootTree); err != nil {
		t.Fatal(err)
	}
	snapBuf, snapID := encodeTestSnapshot(t, rootID)
	if err := c.WriteObject(format.ObjectKindSnapshot, snapID, snapBuf); err != nil {
		t.Fatal(err)
	}

	partial, ok := c.CheckComplete(snapID).(*catalog.PartialError)
	if !ok {
		t.Fatal("CheckComplete did not return a *catalog.PartialError")
	}
	if partial.MissingObject != missingID {
		t.Fatalf("PartialError.MissingObject = %s, want %s", partial.MissingObject.TextForm(), missingID.TextForm())
	}
	if err := c.RefreshComplete(snapID); err != nil {
		t.Fatal(err)
	}
	if !c.Partial(snapID) {
		t.Fatal("Partial = false after RefreshComplete, want true")
	}
}

// TestCheckCompleteResolvesDisc plants one catalog disc whose INDEX
// Prereqs table and whose own DISCS table together name the disc that
// holds a missing tree, and checks CheckComplete resolves that disc.
func TestCheckCompleteResolvesDisc(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	missingID := object.ComputeID(format.ObjectKindChunk, []byte("a tree only another disc stores"))
	rootTree, rootID := encodeTestTree(t, format.TreeEntry{
		EntryType: format.EntryTypeDirectory,
		Name:      []byte("child"),
		ContentID: missingID,
	})
	if err := c.WriteObject(format.ObjectKindTree, rootID, rootTree); err != nil {
		t.Fatal(err)
	}
	snapBuf, snapID := encodeTestSnapshot(t, rootID)
	if err := c.WriteObject(format.ObjectKindSnapshot, snapID, snapBuf); err != nil {
		t.Fatal(err)
	}

	namingDisc := [16]byte{3, 3, 3, 3}
	holderDisc := [16]byte{9, 9, 9, 9}
	idxBuf := encodeTestIndex(t, 3, nil, nil, []format.IndexPrereqRecord{{ContentID: missingID, DiscUUID: holderDisc}})
	discsBuf := encodeTestDiscs(t, []format.DiscsRow{
		{RunSeq: 3, DiscUUID: namingDisc},
		{RunSeq: 7, DiscUUID: holderDisc},
	})
	if err := c.WriteDisc(namingDisc, idxBuf, encodeTestRefs(t), discsBuf); err != nil {
		t.Fatal(err)
	}

	err = c.CheckComplete(snapID)
	partial, ok := err.(*catalog.PartialError)
	if !ok {
		t.Fatalf("CheckComplete error type = %T, want *catalog.PartialError", err)
	}
	if !partial.HasDiscUUID || partial.DiscUUID != holderDisc {
		t.Fatalf("PartialError disc = %v (has=%v), want %v", partial.DiscUUID, partial.HasDiscUUID, holderDisc)
	}
}

// TestLocateObjectKeysByDiscNotRunSeq writes two discs that carry the
// same run_seq, as two discs do after a lost repository. Each object
// must resolve to the disc that really holds it. The Prereqs row of one
// disc must resolve through that disc's own DISCS table, never through
// the other disc's table.
func TestLocateObjectKeysByDiscNotRunSeq(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	discA := [16]byte{0xaa}
	discB := [16]byte{0xbb}
	objA := object.ComputeID(format.ObjectKindChunk, []byte("object stored on disc A"))
	objB := object.ComputeID(format.ObjectKindChunk, []byte("object stored on disc B"))
	prereqOnly := object.ComputeID(format.ObjectKindChunk, []byte("object only a prereq row names"))
	const sharedRunSeq = 5

	idxA := encodeTestIndex(t, sharedRunSeq,
		[]format.IndexObjectRecord{{ContentID: objA, Kind: format.ObjectKindChunk}}, []uint64{11}, nil)
	discsA := encodeTestDiscs(t, []format.DiscsRow{{RunSeq: sharedRunSeq, DiscUUID: discA}})
	if err := c.WriteDisc(discA, idxA, encodeTestRefs(t), discsA); err != nil {
		t.Fatal(err)
	}

	idxB := encodeTestIndex(t, sharedRunSeq,
		[]format.IndexObjectRecord{{ContentID: objB, Kind: format.ObjectKindChunk}}, []uint64{22},
		[]format.IndexPrereqRecord{{ContentID: prereqOnly, DiscUUID: discA}})
	discsB := encodeTestDiscs(t, []format.DiscsRow{{RunSeq: sharedRunSeq, DiscUUID: discB}})
	if err := c.WriteDisc(discB, idxB, encodeTestRefs(t), discsB); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		id   object.ID
		disc [16]byte
		size uint64
	}{
		{"objects row of disc A", objA, discA, 11},
		{"objects row of disc B", objB, discB, 22},
		{"prereqs row of disc B names disc A", prereqOnly, discA, 0},
	}
	for _, tc := range cases {
		loc, found := c.LocateObject(tc.id)
		if !found {
			t.Fatalf("%s: LocateObject found nothing", tc.name)
		}
		if loc.DiscUUID != tc.disc {
			t.Fatalf("%s: disc = %v, want %v", tc.name, loc.DiscUUID, tc.disc)
		}
		if loc.ByteLen != tc.size {
			t.Fatalf("%s: ByteLen = %d, want %d", tc.name, loc.ByteLen, tc.size)
		}
	}
}

// encodeTestIndex builds a minimal, valid INDEX holding the given
// Objects and Prereqs rows, encoded ready for catalog.WriteDisc.
func encodeTestIndex(t *testing.T, runSeq uint64, objects []format.IndexObjectRecord, objectByteLens []uint64, prereqs []format.IndexPrereqRecord) []byte {
	t.Helper()
	// The j-th role 13 Files row describes the file of Objects row j,
	// so the two tables must stay the same length.
	files := make([]format.IndexFileRecord, len(objects))
	for i := range objects {
		files[i] = format.IndexFileRecord{Role: format.FileRoleObject, ByteLen: objectByteLens[i]}
	}
	idx := &format.Index{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex, VersionMajor: 1, HeaderLen: format.IndexHeaderLen},
		RunSeq:      runSeq,
		FileCount:   uint32(len(files)),
		ObjectCount: uint32(len(objects)),
		PrereqCount: uint32(len(prereqs)),
		Files:       files,
		Objects:     objects,
		Prereqs:     prereqs,
	}
	buf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// encodeTestDiscs builds a minimal, valid DISCS table holding rows.
func encodeTestDiscs(t *testing.T, rows []format.DiscsRow) []byte {
	t.Helper()
	discs := &format.DiscsTable{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs, VersionMajor: 1, HeaderLen: format.DiscsHeaderLen},
		RecordCount: uint64(len(rows)),
		Rows:        rows,
	}
	buf := make([]byte, format.DiscsHeaderLen+len(rows)*format.DiscsRowLen)
	if _, err := discs.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// encodeTestRefs builds a minimal, valid empty REFS table.
func encodeTestRefs(t *testing.T) []byte {
	t.Helper()
	refs := &format.RefsTable{
		Header: format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs, VersionMajor: 1, HeaderLen: format.RefsHeaderLen},
	}
	buf := make([]byte, format.RefsHeaderLen)
	if _, err := refs.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// objectFile finishes the object header of the object that encode
// writes, and returns the object file and its content id.
func objectFile(t *testing.T, kind format.ObjectKind, encode func(oh format.ObjectHeader) []byte) ([]byte, object.ID) {
	t.Helper()
	const headLen = format.CommonHeaderLen + format.ObjectHeaderLen
	oh := format.ObjectHeader{Kind: kind, HashAlgo: format.HashAlgoSHA256}
	payloadLen := uint64(len(encode(oh)) - headLen)
	oh.PayloadLen, oh.StoredLen = payloadLen, payloadLen
	raw := encode(oh)
	return raw, object.ComputeID(kind, raw[headLen:])
}

// encodeTestTree builds a valid tree object file with entries, and
// returns it with its content id.
func encodeTestTree(t *testing.T, entries ...format.TreeEntry) ([]byte, object.ID) {
	t.Helper()
	return objectFile(t, format.ObjectKindTree, func(oh format.ObjectHeader) []byte {
		tree := &format.Tree{
			Header:       format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicTree, VersionMajor: 1, HeaderLen: format.TreeHeaderLen},
			ObjectHeader: oh,
			EntryCount:   uint32(len(entries)),
			Entries:      entries,
		}
		buf := make([]byte, tree.EncodedLen())
		if _, err := tree.Encode(buf); err != nil {
			t.Fatal(err)
		}
		return buf
	})
}

// encodeTestBlob builds a valid blob object file with entries, and
// returns it with its content id.
func encodeTestBlob(t *testing.T, entries ...format.BlobEntry) ([]byte, object.ID) {
	t.Helper()
	return objectFile(t, format.ObjectKindBlob, func(oh format.ObjectHeader) []byte {
		blob := &format.Blob{
			Header:       format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicBlob, VersionMajor: 1, HeaderLen: format.BlobHeaderLen},
			ObjectHeader: oh,
			EntryCount:   uint64(len(entries)),
			Entries:      entries,
		}
		buf := make([]byte, blob.EncodedLen())
		if _, err := blob.Encode(buf); err != nil {
			t.Fatal(err)
		}
		return buf
	})
}

// encodeTestSnapshot builds a valid snapshot object file that points at
// rootTree, and returns it with its content id.
func encodeTestSnapshot(t *testing.T, rootTree object.ID) ([]byte, object.ID) {
	t.Helper()
	return objectFile(t, format.ObjectKindSnapshot, func(oh format.ObjectHeader) []byte {
		snap := &format.Snapshot{
			Common:   format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicSnapshot, VersionMajor: 1, HeaderLen: format.SnapshotHeaderLen},
			Object:   oh,
			RootTree: [32]byte(rootTree),
		}
		buf := make([]byte, snap.EncodedLen())
		if _, err := snap.Encode(buf); err != nil {
			t.Fatal(err)
		}
		return buf
	})
}

// TestNewestCatalogDiscBreaksATie writes two discs that one second holds
// both of: the newest is the one whose own DISCS table has more rows,
// whatever the uuid order. A tie on the row count too takes the higher
// uuid, so the answer never changes between runs.
func TestNewestCatalogDiscBreaksATie(t *testing.T) {
	const sameSecond = 1_700_000_000

	low := [16]byte{0x11}
	high := [16]byte{0xee}

	t.Run("more rows wins over a higher uuid", func(t *testing.T) {
		c, err := catalog.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		writeTieDisc(t, c, high, sameSecond, []format.DiscsRow{{DiscSeq: 0, DiscUUID: high, CreatedSec: sameSecond}})
		writeTieDisc(t, c, low, sameSecond, []format.DiscsRow{
			{DiscSeq: 0, DiscUUID: high, CreatedSec: sameSecond},
			{DiscSeq: 1, DiscUUID: low, CreatedSec: sameSecond},
		})
		discs, err := c.Discs()
		if err != nil {
			t.Fatal(err)
		}
		if len(discs.Rows) != 2 {
			t.Fatalf("Discs returned %d row(s), want the 2-row table of the later pack", len(discs.Rows))
		}
	})

	t.Run("the higher uuid is the last rule", func(t *testing.T) {
		c, err := catalog.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		writeTieDisc(t, c, low, sameSecond, []format.DiscsRow{{DiscSeq: 3, DiscUUID: low, CreatedSec: sameSecond}})
		writeTieDisc(t, c, high, sameSecond, []format.DiscsRow{{DiscSeq: 4, DiscUUID: high, CreatedSec: sameSecond}})
		discs, err := c.Discs()
		if err != nil {
			t.Fatal(err)
		}
		if len(discs.Rows) != 1 || discs.Rows[0].DiscUUID != high {
			t.Fatalf("Discs returned %v, want the table of the higher uuid", discs.Rows)
		}
	})
}

// writeTieDisc writes the tables of one disc whose own DISCS table holds rows.
func writeTieDisc(t *testing.T, c *catalog.Catalog, uuid [16]byte, runSeq uint64, rows []format.DiscsRow) {
	t.Helper()
	idx := encodeTestIndex(t, runSeq, nil, nil, nil)
	if err := c.WriteDisc(uuid, idx, encodeTestRefs(t), encodeTestDiscs(t, rows)); err != nil {
		t.Fatal(err)
	}
}
