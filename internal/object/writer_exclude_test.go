package object

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// rootTreeOf decodes the source root's own directory tree from a
// snapshot: the one entry of the synthetic root tree, followed one level
// in.
func rootTreeOf(t *testing.T, staging string, snapID ID) *format.Tree {
	t.Helper()
	snap := loadSnapshot(t, staging, snapID)
	rootWrapper := loadTree(t, staging, ID(snap.RootTree))
	if len(rootWrapper.Entries) != 1 {
		t.Fatalf("root wrapper tree has %d entries, want 1", len(rootWrapper.Entries))
	}
	return loadTree(t, staging, ID(rootWrapper.Entries[0].ContentID))
}

func TestExcludeKeepsMatchedFileOutOfTree(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "keep")
	mustWrite(t, filepath.Join(src, "a.tmp"), "drop")

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	pat, err := ParsePattern("*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	w.Exclude = NewMatcher([]Pattern{pat})
	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Excluded != 1 {
		t.Fatalf("Excluded = %d, want 1", sum.Excluded)
	}
	tr := rootTreeOf(t, staging, snapID)
	if len(tr.Entries) != 1 || string(tr.Entries[0].Name) != "a.txt" {
		t.Fatalf("tree entries = %v, want only a.txt", tr.Entries)
	}
}

func TestExcludeKeepsMatchedDirectoryOutOfTreeAndUnwalked(t *testing.T) {
	src := t.TempDir()
	mustMkdir(t, filepath.Join(src, "node_modules"))
	mustWrite(t, filepath.Join(src, "node_modules", "x.js"), "content")
	mustWrite(t, filepath.Join(src, "keep.txt"), "keep")

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	pat, err := ParsePattern("node_modules/")
	if err != nil {
		t.Fatal(err)
	}
	w.Exclude = NewMatcher([]Pattern{pat})
	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Excluded != 1 {
		t.Fatalf("Excluded = %d, want 1", sum.Excluded)
	}
	tr := rootTreeOf(t, staging, snapID)
	if len(tr.Entries) != 1 || string(tr.Entries[0].Name) != "keep.txt" {
		t.Fatalf("tree entries = %v, want only keep.txt", tr.Entries)
	}
	// The excluded directory's own content was never staged as an
	// object: no chunk or blob was written for x.js.
	verifyAllObjectsValid(t, staging)
}

func TestNilExcludeExcludesNothing(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)
	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	_, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Excluded != 0 {
		t.Fatalf("Excluded = %d, want 0", sum.Excluded)
	}
}

func TestOneFileSystemRecordsMountPointAsEmptyDirectory(t *testing.T) {
	src := t.TempDir()
	mustMkdir(t, filepath.Join(src, "mnt"))
	mustWrite(t, filepath.Join(src, "mnt", "other-fs-file.txt"), "elsewhere")
	mustWrite(t, filepath.Join(src, "keep.txt"), "keep")

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	w.OneFileSystem = true
	// The seam: the root and keep.txt report device 1; mnt and
	// everything under it reports device 2, simulating a mount point
	// with no real mount.
	w.DeviceID = func(info os.FileInfo) (uint64, bool) {
		if info.Name() == "mnt" {
			return 2, true
		}
		return 1, true
	}
	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.MountPoints) != 1 || sum.MountPoints[0] != "mnt" {
		t.Fatalf("MountPoints = %v, want [mnt]", sum.MountPoints)
	}
	tr := rootTreeOf(t, staging, snapID)
	mnt := findEntry(t, tr, "mnt")
	if mnt.EntryType != format.EntryTypeDirectory {
		t.Fatalf("mnt entry type = %v, want directory", mnt.EntryType)
	}
	mntTree := loadTree(t, staging, ID(mnt.ContentID))
	if len(mntTree.Entries) != 0 {
		t.Fatalf("mnt tree has %d entries, want 0 (not walked)", len(mntTree.Entries))
	}
}

func TestOneFileSystemDoesNotCrossRootItself(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)
	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	w.OneFileSystem = true
	w.DeviceID = func(info os.FileInfo) (uint64, bool) { return 1, true }
	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.MountPoints) != 0 {
		t.Fatalf("MountPoints = %v, want none", sum.MountPoints)
	}
	tr := rootTreeOf(t, staging, snapID)
	sub := findEntry(t, tr, "sub")
	subTree := loadTree(t, staging, ID(sub.ContentID))
	if len(subTree.Entries) != 2 {
		t.Fatalf("sub tree has %d entries, want 2 (walked normally)", len(subTree.Entries))
	}
}

func TestOneFileSystemRefusedWhenDeviceIDUnavailable(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)
	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	w.OneFileSystem = true
	w.DeviceID = func(info os.FileInfo) (uint64, bool) { return 0, false }
	if _, _, err := w.Commit(src); err == nil {
		t.Fatal("expected an error when the platform reports no device id")
	}
}
