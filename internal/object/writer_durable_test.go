package object

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// fsEvent is one file system step of a commit: "sync" of a temporary
// file, "rename" of it to its object name, or "dirsync" of a directory.
type fsEvent struct {
	op   string
	path string
	to   string
}

// recordFSEvents replaces the file sync, the rename and the directory
// sync of w with recorders that also do the real step.
func recordFSEvents(t *testing.T, w *Writer) *[]fsEvent {
	t.Helper()
	var events []fsEvent
	oldSync, oldRename := syncFile, renameFile
	t.Cleanup(func() { syncFile, renameFile = oldSync, oldRename })
	syncFile = func(f *os.File) error {
		events = append(events, fsEvent{op: "sync", path: f.Name()})
		return f.Sync()
	}
	renameFile = func(from, to string) error {
		events = append(events, fsEvent{op: "rename", path: from, to: to})
		return os.Rename(from, to)
	}
	w.SyncDir = func(dir string) error {
		events = append(events, fsEvent{op: "dirsync", path: dir})
		return SyncDir(dir)
	}
	return &events
}

// TestCommitSyncsEachObjectBeforeItsRenameAndEachDirectoryOnce checks the
// order of the durable steps: each object file is synced before its
// rename, and each directory that got a new name is synced one time,
// after the last rename and before Commit returns.
func TestCommitSyncsEachObjectBeforeItsRenameAndEachDirectoryOnce(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)
	staging := t.TempDir()
	w := testWriter(staging)
	w.Now = fixedClock
	events := recordFSEvents(t, w)

	if _, _, err := w.Commit(src); err != nil {
		t.Fatal(err)
	}

	synced := map[string]bool{}
	renamedDirs := map[string]bool{}
	lastRename := -1
	for i, e := range *events {
		switch e.op {
		case "sync":
			synced[e.path] = true
		case "rename":
			if !synced[e.path] {
				t.Fatalf("rename of %s to %s before a sync of the file", e.path, e.to)
			}
			renamedDirs[filepath.Dir(e.to)] = true
			lastRename = i
		}
	}
	if lastRename < 0 {
		t.Fatal("commit renamed no object file")
	}
	dirSyncs := map[string]int{}
	for i, e := range *events {
		if e.op != "dirsync" {
			continue
		}
		if i < lastRename {
			t.Fatalf("directory %s synced before the last rename", e.path)
		}
		dirSyncs[e.path]++
	}
	for dir := range renamedDirs {
		if dirSyncs[dir] != 1 {
			t.Errorf("directory %s synced %d time(s), want 1", dir, dirSyncs[dir])
		}
		// Each directory that commit created is a new name in its parent.
		if parent := filepath.Dir(dir); dirSyncs[parent] != 1 {
			t.Errorf("parent %s of new directory %s synced %d time(s), want 1", parent, dir, dirSyncs[parent])
		}
	}
}

// TestCommitAgainSyncsNoDirectory commits the same source two times. The
// second commit renames no file, thus it syncs no directory.
func TestCommitAgainSyncsNoDirectory(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)
	staging := t.TempDir()
	w := testWriter(staging)
	w.Now = fixedClock
	if _, _, err := w.Commit(src); err != nil {
		t.Fatal(err)
	}
	events := recordFSEvents(t, w)
	if _, _, err := w.Commit(src); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 0 {
		t.Fatalf("second commit of the same source did %v, want nothing", *events)
	}
}

// TestCommitFailsWhenADirectorySyncFails checks that a failed directory
// sync is an error of Commit, thus the caller writes no state log record.
func TestCommitFailsWhenADirectorySyncFails(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)
	w := testWriter(t.TempDir())
	errSync := errors.New("injected directory sync failure")
	w.SyncDir = func(string) error { return errSync }
	if _, _, err := w.Commit(src); !errors.Is(err, errSync) {
		t.Fatalf("Commit error = %v, want the directory sync failure", err)
	}
}

// chunkFileIDs returns the text form of each chunk file below staging.
func chunkFileIDs(t *testing.T, staging string) []string {
	t.Helper()
	var ids []string
	for _, rel := range listFiles(t, filepath.Join(staging, "chunks")) {
		ids = append(ids, filepath.Base(rel))
	}
	slices.Sort(ids)
	return ids
}

// reachableIDs returns the text form of each id of sum.Reachable.
func reachableIDs(sum Summary) map[string]bool {
	out := map[string]bool{}
	for _, id := range sum.Reachable {
		out[id.TextForm()] = true
	}
	return out
}

func randomBytes(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// TestUnstableFirstReadAddsNothingToTheSnapshot gives an unstable file
// other content on its first read. The chunk of the first read must not
// be in Summary.Reachable, and its chunk file must be gone.
func TestUnstableFirstReadAddsNothingToTheSnapshot(t *testing.T) {
	src := t.TempDir()
	target := filepath.Join(src, "f.bin")
	mustWriteBytes(t, target, randomBytes(7, 600_000))
	firstRead := randomBytes(8, 600_000)

	staging := t.TempDir()
	w := testWriter(staging)
	w.Now = fixedClock
	opens, stats := 0, 0
	w.Open = func(path string) (io.ReadCloser, error) {
		if path == target {
			opens++
			if opens == 1 {
				return io.NopCloser(bytes.NewReader(firstRead)), nil
			}
		}
		return os.Open(path)
	}
	w.Stat = func(path string) (os.FileInfo, error) {
		real, err := os.Lstat(path)
		if err != nil || path != target {
			return real, err
		}
		stats++
		if stats >= 2 {
			return fakeStatInfo{FileInfo: real, size: real.Size() + 1, mtime: real.ModTime()}, nil
		}
		return real, nil
	}

	_, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Unstable) != 0 {
		t.Fatalf("Summary.Unstable = %v, want none: the second read was stable", sum.Unstable)
	}
	dropped := ComputeID(format.ObjectKindChunk, firstRead)
	reach := reachableIDs(sum)
	if reach[dropped.TextForm()] {
		t.Fatal("Summary.Reachable holds the chunk of the dropped first read")
	}
	for _, id := range chunkFileIDs(t, staging) {
		if !reach[id] {
			t.Errorf("chunk file %s stays, but the snapshot does not reach it", id)
		}
	}
	if len(chunkFileIDs(t, staging)) != 1 {
		t.Fatalf("chunk files = %v, want the one chunk of the second read", chunkFileIDs(t, staging))
	}
	// Summary.Reachable holds the chunk, the blob, the two trees; the
	// snapshot is not in it.
	if len(sum.Reachable) != 4 {
		t.Fatalf("len(Summary.Reachable) = %d, want 4", len(sum.Reachable))
	}
}

// TestFailedReadRemovesItsChunkFiles fails a read of a file after some
// chunks were written. The chunk files of that read must be gone.
func TestFailedReadRemovesItsChunkFiles(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "content of a")
	bad := filepath.Join(src, "b.bin")
	mustWrite(t, bad, "placeholder")

	staging := t.TempDir()
	w := testWriter(staging)
	w.Now = fixedClock
	w.Open = func(path string) (io.ReadCloser, error) {
		if path == bad {
			return &failAfterNReader{remaining: randomBytes(9, 24<<20)}, nil
		}
		return os.Open(path)
	}
	written := 0
	oldRename := renameFile
	t.Cleanup(func() { renameFile = oldRename })
	renameFile = func(from, to string) error {
		if strings.Contains(to, string(filepath.Separator)+"chunks"+string(filepath.Separator)) {
			written++
		}
		return os.Rename(from, to)
	}

	_, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Skipped) != 1 {
		t.Fatalf("Summary.Skipped = %v, want b.bin", sum.Skipped)
	}
	if written < 2 {
		t.Fatalf("the failed read wrote %d chunk file(s), want at least 2", written)
	}
	aChunk := ComputeID(format.ObjectKindChunk, []byte("content of a")).TextForm()
	if got := chunkFileIDs(t, staging); !slices.Equal(got, []string{aChunk}) {
		t.Fatalf("chunk files = %v, want only the chunk of a.txt", got)
	}
	if len(sum.Reachable) != 4 {
		t.Fatalf("len(Summary.Reachable) = %d, want 4: a chunk, a blob, two trees", len(sum.Reachable))
	}
}

// TestDroppedChunkWithARecordKeepsItsFile drops a read whose chunk the
// state log knows. The chunk file stays: a record names it.
func TestDroppedChunkWithARecordKeepsItsFile(t *testing.T) {
	src := t.TempDir()
	bad := filepath.Join(src, "b.bin")
	mustWrite(t, bad, "placeholder")

	staging := t.TempDir()
	w := testWriter(staging)
	w.Now = fixedClock
	w.Open = func(path string) (io.ReadCloser, error) {
		if path == bad {
			return &failAfterNReader{remaining: randomBytes(9, 24<<20)}, nil
		}
		return os.Open(path)
	}
	w.HasRecord = func(ID) bool { return true }
	if _, _, err := w.Commit(src); err != nil {
		t.Fatal(err)
	}
	if len(chunkFileIDs(t, staging)) == 0 {
		t.Fatal("commit removed a chunk file that the state log names")
	}
}

// TestOwnDirIsLeftOutByDeviceAndInode names the repository directory by
// a path through a symlink. The walk must still leave it out.
func TestOwnDirIsLeftOutByDeviceAndInode(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "content of a")
	mustMkdir(t, filepath.Join(src, "repo", "state"))
	mustWrite(t, filepath.Join(src, "repo", "state", "x"), "state")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	w := testWriter(staging)
	w.Now = fixedClock
	w.OwnDirs = []OwnDir{
		{Path: filepath.Join(alias, "repo"), What: "the repository"},
		{Path: filepath.Join(alias, "absent"), What: "the staging store"},
	}
	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.OwnDirs) != 1 || sum.OwnDirs[0] != (OwnDir{Path: "repo", What: "the repository"}) {
		t.Fatalf("Summary.OwnDirs = %+v, want repo", sum.OwnDirs)
	}
	root := loadTree(t, staging, ID(loadSnapshot(t, staging, snapID).RootTree))
	dir := loadTree(t, staging, ID(root.Entries[0].ContentID))
	if len(dir.Entries) != 1 || string(dir.Entries[0].Name) != "a.txt" {
		t.Fatalf("tree entries = %d, want only a.txt", len(dir.Entries))
	}
}

// TestSymlinkSourceRootIsRefusedWithItsTarget checks the refusal of a
// source root that is a symlink: it names the link and its target.
func TestSymlinkSourceRootIsRefusedWithItsTarget(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := testWriter(t.TempDir()).Commit(link)
	if err == nil {
		t.Fatal("Commit of a symlink root succeeded")
	}
	msg := err.Error()
	if !strings.Contains(msg, "is a symlink") || !strings.Contains(msg, real) {
		t.Fatalf("error %q does not say symlink and name %s", msg, real)
	}
}
