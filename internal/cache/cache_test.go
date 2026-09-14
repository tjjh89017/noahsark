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
// override, matching OPERATIONS.md "2.4 Local cache layout".
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

	idx, err := c.IndexForRun(rr.Index.RunSeq)
	if err != nil {
		t.Fatalf("IndexForRun: %v", err)
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
	if incomplete.RunSeq != 0 || incomplete.HasDiscUUID {
		t.Fatalf("IncompleteError resolved a run/disc it should not have: %+v", incomplete)
	}
	if c.Complete(snapID) {
		t.Fatal("Complete = true, want false")
	}
}

// TestCheckCompleteResolvesRunAndDisc plants a cached run whose INDEX
// Prereqs table and whose DISCS table together name the run and disc
// that hold a missing tree, and checks CheckComplete resolves both.
func TestCheckCompleteResolvesRunAndDisc(t *testing.T) {
	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	missingID := object.ComputeID([]byte("a tree only run 7 stores"))
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

	const knownRunSeq = 3
	idx := &format.Index{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex, VersionMajor: 1},
		RunSeq:      knownRunSeq,
		PrereqCount: 1,
		HashAlgo:    format.HashAlgoSHA256,
		DigestLen:   32,
		Prereqs: []format.IndexPrereqRecord{
			{ContentID: missingID, RunSeq: 7},
		},
	}
	idxBuf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(idxBuf); err != nil {
		t.Fatal(err)
	}

	discUUID := [16]byte{9, 9, 9, 9}
	discs := &format.DiscsTable{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs, VersionMajor: 1},
		HashAlgo:    format.HashAlgoSHA256,
		DigestLen:   32,
		RecordCount: 1,
		RecordSize:  format.DiscsRowLen,
		Rows: []format.DiscsRow{
			{RunSeq: 7, DiscUUID: discUUID, RunStatus: 1, Health: 1},
		},
	}
	discsBuf := make([]byte, format.DiscsHeaderLen+len(discs.Rows)*format.DiscsRowLen)
	if _, err := discs.Encode(discsBuf); err != nil {
		t.Fatal(err)
	}

	refs := &format.RefsTable{
		Header:     format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs, VersionMajor: 1},
		HashAlgo:   format.HashAlgoSHA256,
		DigestLen:  32,
		RecordSize: format.RefRecordLen,
	}
	refsBuf := make([]byte, format.RefsHeaderLen)
	if _, err := refs.Encode(refsBuf); err != nil {
		t.Fatal(err)
	}

	if err := c.WriteRun(knownRunSeq, idxBuf, refsBuf, discsBuf); err != nil {
		t.Fatal(err)
	}

	err = c.CheckComplete(snapID)
	incomplete, ok := err.(*cache.IncompleteError)
	if !ok {
		t.Fatalf("CheckComplete error type = %T, want *cache.IncompleteError", err)
	}
	if incomplete.RunSeq != 7 {
		t.Fatalf("IncompleteError.RunSeq = %d, want 7", incomplete.RunSeq)
	}
	if !incomplete.HasDiscUUID || incomplete.DiscUUID != discUUID {
		t.Fatalf("IncompleteError disc = %v (has=%v), want %v", incomplete.DiscUUID, incomplete.HasDiscUUID, discUUID)
	}
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
