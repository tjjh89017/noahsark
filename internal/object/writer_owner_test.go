package object

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// ownedInfo wraps a real os.FileInfo and reports a caller-chosen uid and
// gid, so a test can commit a tree owned by an id this host does not
// have, without root.
type ownedInfo struct {
	os.FileInfo
	st syscall.Stat_t
}

func (o *ownedInfo) Sys() any { return &o.st }

// ownerStatSeam returns a Stat seam that reports uid and gid for every
// path, and keeps every other stat field of the real file.
func ownerStatSeam(t *testing.T, uid, gid uint32) func(string) (os.FileInfo, error) {
	t.Helper()
	return func(path string) (os.FileInfo, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("this platform reports no syscall.Stat_t")
		}
		owned := &ownedInfo{FileInfo: info, st: *st}
		owned.st.Uid, owned.st.Gid = uid, gid
		return owned, nil
	}
}

// tlvPayload returns the payload of the TLV of type typ, and whether the
// entry carries one.
func tlvPayload(e format.TreeEntry, typ uint16) (string, bool) {
	for _, tlv := range e.TLVs {
		if tlv.Type == typ {
			return string(tlv.Payload), true
		}
	}
	return "", false
}

// TestCommitRecordsUIDAndGID commits one file through a stat seam that
// reports an owner this host does not know: the tree entry must carry
// that uid and gid, and no name TLV, because the lookup fails.
func TestCommitRecordsUIDAndGID(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "hello")

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	w.Stat = ownerStatSeam(t, 4242, 4343)
	snapID, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	dirTree := loadTree(t, staging, ID(rootTree.Entries[0].ContentID))
	e := findEntry(t, dirTree, "a.txt")
	if e.UID != 4242 || e.GID != 4343 {
		t.Fatalf("entry owner = %d:%d, want 4242:4343", e.UID, e.GID)
	}
	if name, ok := tlvPayload(e, format.TLVTypeUserName); ok {
		t.Fatalf("unknown uid got a user name TLV %q", name)
	}
	if name, ok := tlvPayload(e, format.TLVTypeGroupName); ok {
		t.Fatalf("unknown gid got a group name TLV %q", name)
	}
}

// TestCommitRecordsOwnerNames commits one file owned by the invoking
// user: the entry must carry that uid and the matching user name TLV.
func TestCommitRecordsOwnerNames(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("this host names no current user: %v", err)
	}
	uid, err := strconv.ParseUint(me.Uid, 10, 32)
	if err != nil {
		t.Skipf("this host has no numeric uid: %v", err)
	}

	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "hello")

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	snapID, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	dirTree := loadTree(t, staging, ID(rootTree.Entries[0].ContentID))
	e := findEntry(t, dirTree, "a.txt")
	if e.UID != uint32(uid) {
		t.Fatalf("entry uid = %d, want %d", e.UID, uid)
	}
	name, ok := tlvPayload(e, format.TLVTypeUserName)
	if !ok {
		t.Fatalf("entry carries no user name TLV for uid %d", uid)
	}
	if name != me.Username {
		t.Fatalf("user name TLV = %q, want %q", name, me.Username)
	}
}

// TestCommitKeepsTLVAreaSorted checks that a symlink entry's TLVs stay
// in ascending type order once the owner names are added: the format
// sorts a TLV area by type.
func TestCommitKeepsTLVAreaSorted(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "hello")
	if err := os.Symlink("a.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	snapID, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	checkSortedTLVs(t, rootTree.Entries[0])
	dirTree := loadTree(t, staging, ID(rootTree.Entries[0].ContentID))
	checkSortedTLVs(t, findEntry(t, dirTree, "link"))
}

func checkSortedTLVs(t *testing.T, e format.TreeEntry) {
	t.Helper()
	for i := 1; i < len(e.TLVs); i++ {
		if e.TLVs[i-1].Type > e.TLVs[i].Type {
			t.Fatalf("entry %q: TLV type %d comes after %d", e.Name, e.TLVs[i].Type, e.TLVs[i-1].Type)
		}
	}
}
