package cache_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// TestResolveDirDefaultsToXDGCacheHome checks the default path and the
// override, matching OPERATIONS.md's local cache layout rules.
func TestResolveDirDefaultsToXDGCacheHome(t *testing.T) {
	repoUUID := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

	t.Setenv("XDG_CACHE_HOME", "/xdg-home")
	dir, err := cache.ResolveDir(repoUUID, "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/xdg-home", "noahsark", "01020304-0506-0708-090a-0b0c0d0e0f10")
	if dir != want {
		t.Fatalf("ResolveDir = %q, want %q", dir, want)
	}

	dir, err = cache.ResolveDir(repoUUID, "/explicit/cache")
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/explicit/cache" {
		t.Fatalf("ResolveDir override = %q, want /explicit/cache", dir)
	}
}

// TestWriteFromRootPopulatesCache packs one run and copies it into the
// cache, then checks every read method resolves the same content.
func TestWriteFromRootPopulatesCache(t *testing.T) {
	runRoot, snapID := buildFixtureRun(t)

	rr, err := image.Read(runRoot)
	if err != nil {
		t.Fatal(err)
	}

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.WriteFromRoot(c, runRoot); err != nil {
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
		t.Fatalf("cached INDEX object count = %d, want %d", len(idx.Objects), len(rr.Index.Objects))
	}

	refs, err := c.Refs()
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if len(refs.Records) != len(rr.Refs.Records) {
		t.Fatalf("cached REFS record count = %d, want %d", len(refs.Records), len(rr.Refs.Records))
	}

	discs, err := c.Discs()
	if err != nil {
		t.Fatalf("Discs: %v", err)
	}
	if len(discs.Rows) != len(rr.Discs.Rows) {
		t.Fatalf("cached DISCS row count = %d, want %d", len(discs.Rows), len(rr.Discs.Rows))
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

// TestWriteFromRootIsIdempotent runs WriteFromRoot twice over the same
// run and checks the cache ends up with the same content, not
// duplicated or corrupted entries.
func TestWriteFromRootIsIdempotent(t *testing.T) {
	runRoot, snapID := buildFixtureRun(t)
	dir := t.TempDir()

	c, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.WriteFromRoot(c, runRoot); err != nil {
		t.Fatal(err)
	}
	treesBefore, err := os.ReadDir(filepath.Join(dir, "trees"))
	if err != nil {
		t.Fatal(err)
	}

	c2, err := cache.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.WriteFromRoot(c2, runRoot); err != nil {
		t.Fatal(err)
	}
	treesAfter, err := os.ReadDir(filepath.Join(dir, "trees"))
	if err != nil {
		t.Fatal(err)
	}
	if len(treesAfter) != len(treesBefore) {
		t.Fatalf("tree count after second write = %d, want %d", len(treesAfter), len(treesBefore))
	}
	if !c2.Complete(snapID) {
		t.Fatal("Complete = false after second write, want true")
	}
}

// TestCheckCompleteReportsMissingTree builds a cache holding a
// snapshot whose root tree references a child tree the cache never
// received, and checks CheckComplete reports an *IncompleteError
// naming that child.
func TestCheckCompleteReportsMissingTree(t *testing.T) {
	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	missingID := object.ComputeID([]byte("a tree that is never written to the cache"))
	rootTree := encodeTestTree(t, format.TreeEntry{
		EntryType: format.EntryTypeDirectory,
		Name:      []byte("child"),
		ContentID: missingID,
	})
	rootID := object.ComputeID(rootTree)
	if err := c.WriteTree(rootID, rootTree); err != nil {
		t.Fatal(err)
	}

	snapID := object.ComputeID([]byte("snapshot payload"))
	snapBuf := encodeTestSnapshot(t, rootID)
	if err := c.WriteSnapshot(snapID, snapBuf); err != nil {
		t.Fatal(err)
	}

	err = c.CheckComplete(snapID)
	if err == nil {
		t.Fatal("CheckComplete = nil, want an *IncompleteError")
	}
	incomplete, ok := err.(*cache.IncompleteError)
	if !ok {
		t.Fatalf("CheckComplete error type = %T, want *cache.IncompleteError", err)
	}
	if incomplete.Snapshot != snapID {
		t.Fatalf("IncompleteError.Snapshot = %s, want %s", incomplete.Snapshot.TextForm(), snapID.TextForm())
	}
	if incomplete.MissingTree != missingID {
		t.Fatalf("IncompleteError.MissingTree = %s, want %s", incomplete.MissingTree.TextForm(), missingID.TextForm())
	}
	if incomplete.HasDiscUUID {
		t.Fatalf("IncompleteError resolved a disc it should not have: %+v", incomplete)
	}
	if c.Complete(snapID) {
		t.Fatal("Complete = true, want false")
	}
}

// TestCheckCompleteResolvesDisc plants one cached disc whose INDEX
// Prereqs table and whose own DISCS table together name the disc that
// holds a missing tree, and checks CheckComplete resolves that disc.
func TestCheckCompleteResolvesDisc(t *testing.T) {
	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	missingID := object.ComputeID([]byte("a tree only another disc stores"))
	rootTree := encodeTestTree(t, format.TreeEntry{
		EntryType: format.EntryTypeDirectory,
		Name:      []byte("child"),
		ContentID: missingID,
	})
	rootID := object.ComputeID(rootTree)
	if err := c.WriteTree(rootID, rootTree); err != nil {
		t.Fatal(err)
	}
	snapID := object.ComputeID([]byte("snapshot payload 2"))
	if err := c.WriteSnapshot(snapID, encodeTestSnapshot(t, rootID)); err != nil {
		t.Fatal(err)
	}

	cachedDisc := [16]byte{3, 3, 3, 3}
	holderDisc := [16]byte{9, 9, 9, 9}
	idxBuf := encodeTestIndex(t, 3, nil, []format.IndexPrereqRecord{{ContentID: missingID, RunSeq: 7}})
	discsBuf := encodeTestDiscs(t, []format.DiscsRow{
		{RunSeq: 3, DiscUUID: cachedDisc, RunStatus: 1, Health: 1},
		{RunSeq: 7, DiscUUID: holderDisc, RunStatus: 1, Health: 1},
	})
	if err := c.WriteDisc(cachedDisc, idxBuf, encodeTestRefs(t), discsBuf); err != nil {
		t.Fatal(err)
	}

	err = c.CheckComplete(snapID)
	incomplete, ok := err.(*cache.IncompleteError)
	if !ok {
		t.Fatalf("CheckComplete error type = %T, want *cache.IncompleteError", err)
	}
	if !incomplete.HasDiscUUID || incomplete.DiscUUID != holderDisc {
		t.Fatalf("IncompleteError disc = %v (has=%v), want %v", incomplete.DiscUUID, incomplete.HasDiscUUID, holderDisc)
	}
}

// TestLocateObjectKeysByDiscNotRunSeq caches two discs that carry the
// same run_seq, as two discs do after a lost repository. Each object
// must resolve to the disc that really holds it. The Prereqs row of one
// disc must resolve through that disc's own DISCS table, never through
// the other disc's table.
func TestLocateObjectKeysByDiscNotRunSeq(t *testing.T) {
	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	discA := [16]byte{0xaa}
	discB := [16]byte{0xbb}
	objA := object.ComputeID([]byte("object stored on disc A"))
	objB := object.ComputeID([]byte("object stored on disc B"))
	prereqOnly := object.ComputeID([]byte("object only a prereq row names"))
	const sharedRunSeq = 5

	idxA := encodeTestIndex(t, sharedRunSeq,
		[]format.IndexObjectRecord{{ContentID: objA, PayloadLen: 11, Kind: format.ObjectKindChunk}}, nil)
	discsA := encodeTestDiscs(t, []format.DiscsRow{{RunSeq: sharedRunSeq, DiscUUID: discA, RunStatus: 1, Health: 1}})
	if err := c.WriteDisc(discA, idxA, encodeTestRefs(t), discsA); err != nil {
		t.Fatal(err)
	}

	idxB := encodeTestIndex(t, sharedRunSeq,
		[]format.IndexObjectRecord{{ContentID: objB, PayloadLen: 22, Kind: format.ObjectKindChunk}},
		[]format.IndexPrereqRecord{{ContentID: prereqOnly, RunSeq: sharedRunSeq}})
	discsB := encodeTestDiscs(t, []format.DiscsRow{{RunSeq: sharedRunSeq, DiscUUID: discB, RunStatus: 1, Health: 1}})
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
		{"prereqs row of disc B", prereqOnly, discB, 0},
	}
	for _, tc := range cases {
		loc, found := c.LocateObject(tc.id)
		if !found {
			t.Fatalf("%s: LocateObject found nothing", tc.name)
		}
		if loc.DiscUUID != tc.disc {
			t.Fatalf("%s: disc = %v, want %v", tc.name, loc.DiscUUID, tc.disc)
		}
		if loc.PayloadLen != tc.size {
			t.Fatalf("%s: PayloadLen = %d, want %d", tc.name, loc.PayloadLen, tc.size)
		}
	}
}

// encodeTestIndex builds a minimal, valid INDEX holding the given
// Objects and Prereqs rows, encoded ready for cache.WriteDisc.
func encodeTestIndex(t *testing.T, runSeq uint64, objects []format.IndexObjectRecord, prereqs []format.IndexPrereqRecord) []byte {
	t.Helper()
	idx := &format.Index{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex, VersionMajor: 1},
		RunSeq:      runSeq,
		ObjectCount: uint32(len(objects)),
		PrereqCount: uint32(len(prereqs)),
		HashAlgo:    format.HashAlgoSHA256,
		DigestLen:   32,
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
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs, VersionMajor: 1},
		HashAlgo:    format.HashAlgoSHA256,
		DigestLen:   32,
		RecordCount: uint64(len(rows)),
		RecordSize:  format.DiscsRowLen,
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
		Header:     format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs, VersionMajor: 1},
		HashAlgo:   format.HashAlgoSHA256,
		DigestLen:  32,
		RecordSize: format.RefRecordLen,
	}
	buf := make([]byte, format.RefsHeaderLen)
	if _, err := refs.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// encodeTestTree builds a minimal, valid Tree object with one entry,
// encoded ready for cache.WriteTree.
func encodeTestTree(t *testing.T, entry format.TreeEntry) []byte {
	t.Helper()
	tree := &format.Tree{
		Header:     format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicTree, VersionMajor: 1},
		EntryCount: 1,
		Entries:    []format.TreeEntry{entry},
	}
	buf := make([]byte, tree.EncodedLen())
	if _, err := tree.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// encodeTestSnapshot builds a minimal, valid Snapshot object pointing
// at rootTree, encoded ready for cache.WriteSnapshot.
func encodeTestSnapshot(t *testing.T, rootTree object.ID) []byte {
	t.Helper()
	snap := &format.Snapshot{
		Common:   format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicSnapshot, VersionMajor: 1},
		RootTree: [32]byte(rootTree),
	}
	buf := make([]byte, snap.EncodedLen())
	if _, err := snap.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}
