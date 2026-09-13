package object

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
)

var errUnexpectedType = errors.New("object: unexpected decoded type")

// buildFixture writes a small, deterministic directory tree under dir:
// two files at the root, a subdirectory with two more files, and a
// symlink.
func buildFixture(t *testing.T, dir string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "a.txt"), "content of a")
	mustMkdir(t, filepath.Join(dir, "sub"))
	mustWrite(t, filepath.Join(dir, "sub", "b.txt"), "content of b")
	mustWrite(t, filepath.Join(dir, "sub", "c.txt"), "content of c")
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// fixedClock is the snapshot clock every test uses, so two commits of
// identical content produce byte-identical snapshot objects.
func fixedClock() time.Time {
	return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
}

// listFiles returns every regular file under dir, as paths relative to
// dir, sorted.
func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestCommitTwiceIsByteIdentical(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)

	staging1 := t.TempDir()
	staging2 := t.TempDir()

	w1 := NewWriter(staging1)
	w1.Now = fixedClock
	id1, sum1, err := w1.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	w2 := NewWriter(staging2)
	w2.Now = fixedClock
	id2, sum2, err := w2.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	if id1 != id2 {
		t.Fatalf("snapshot ids differ: %s vs %s", id1.TextForm(), id2.TextForm())
	}
	if sum1.NewObjects != sum2.NewObjects || sum1.ExistingObjects != sum2.ExistingObjects ||
		len(sum1.Unstable) != len(sum2.Unstable) || len(sum1.Skipped) != len(sum2.Skipped) {
		t.Fatalf("summaries differ: %+v vs %+v", sum1, sum2)
	}

	files1 := listFiles(t, staging1)
	files2 := listFiles(t, staging2)
	if len(files1) != len(files2) {
		t.Fatalf("file count differs: %d vs %d", len(files1), len(files2))
	}
	for i := range files1 {
		if files1[i] != files2[i] {
			t.Fatalf("path %d differs: %s vs %s", i, files1[i], files2[i])
		}
		b1, err := os.ReadFile(filepath.Join(staging1, files1[i]))
		if err != nil {
			t.Fatal(err)
		}
		b2, err := os.ReadFile(filepath.Join(staging2, files2[i]))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("object %s differs between the two commits", files1[i])
		}
	}
}

func TestCommitAgainAfterOneFileChanges(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	if _, _, err := w.Commit(src); err != nil {
		t.Fatal(err)
	}

	before := fileSet(t, staging)

	// Advance the clock so the new snapshot gets a different, but still
	// fixed, id even if a hash collision were somehow otherwise possible.
	w.Now = func() time.Time { return fixedClock().Add(time.Hour) }
	mustWrite(t, filepath.Join(src, "sub", "b.txt"), "changed content of b")
	if _, _, err := w.Commit(src); err != nil {
		t.Fatal(err)
	}

	after := fileSet(t, staging)

	var newPaths []string
	for p := range after {
		if !before[p] {
			newPaths = append(newPaths, p)
		}
	}
	sort.Strings(newPaths)

	// Expected: one new chunk (b.txt's new content), one new blob (over
	// that chunk), the sub/ tree, the root directory's tree, the
	// synthetic root tree, and the new snapshot. That is six files, one
	// under snapshots/ and five under objects/.
	var snapshots, objects int
	for _, p := range newPaths {
		switch {
		case strings.HasPrefix(p, "snapshots"+string(filepath.Separator)):
			snapshots++
		case strings.HasPrefix(p, "objects"+string(filepath.Separator)):
			objects++
		default:
			t.Fatalf("unexpected new path outside objects/ and snapshots/: %s", p)
		}
	}
	if snapshots != 1 {
		t.Fatalf("new snapshot files = %d, want 1 (new paths: %v)", snapshots, newPaths)
	}
	if objects != 5 {
		t.Fatalf("new object files = %d, want 5: 1 chunk, 1 blob, 3 trees (new paths: %v)", objects, newPaths)
	}
}

// fileSet returns every regular file under dir, as paths relative to dir.
func fileSet(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := make(map[string]bool)
	for _, p := range listFiles(t, dir) {
		out[p] = true
	}
	return out
}

func TestEveryObjectDecodesAndItsIDMatchesItsFileName(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	if _, _, err := w.Commit(src); err != nil {
		t.Fatal(err)
	}

	verifyAllObjectsValid(t, staging)
}

// verifyAllObjectsValid decodes every object and snapshot file under
// staging through format.Dispatch and checks that recomputing its
// content id from the decoded value reproduces the file name. It fails
// the test on the first object that does not decode or whose id does not
// match.
func verifyAllObjectsValid(t *testing.T, staging string) {
	t.Helper()
	for _, rel := range listFiles(t, staging) {
		path := filepath.Join(staging, rel)
		buf, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		v, _, err := format.Dispatch(buf)
		if err != nil {
			t.Fatalf("%s: Dispatch: %v", rel, err)
		}
		got, err := recomputeID(v)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		want := filepath.Base(rel)
		if got.TextForm() != want {
			t.Fatalf("%s: recomputed id %s, want %s", rel, got.TextForm(), want)
		}
	}
}

// checkNoTempFiles fails the test if any writeObjectFile temp file
// (".tmp-*") is left under staging. A crash or an aborted commit must
// never leave one behind.
func checkNoTempFiles(t *testing.T, staging string) {
	t.Helper()
	err := filepath.WalkDir(staging, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("leftover temp file: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// loadTree decodes the tree object id from staging.
func loadTree(t *testing.T, staging string, id ID) *format.Tree {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join(staging, "objects", id.FanoutByte(), id.TextForm()))
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := format.Dispatch(buf)
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := v.(*format.Tree)
	if !ok {
		t.Fatalf("object %s is not a tree", id.TextForm())
	}
	return tr
}

// loadSnapshot decodes the snapshot object id from staging.
func loadSnapshot(t *testing.T, staging string, id ID) *format.Snapshot {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join(staging, "snapshots", id.TextForm()))
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := format.Dispatch(buf)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(*format.Snapshot)
	if !ok {
		t.Fatalf("object %s is not a snapshot", id.TextForm())
	}
	return s
}

// findEntry returns the entry named name in tr, failing the test if it is
// absent.
func findEntry(t *testing.T, tr *format.Tree, name string) format.TreeEntry {
	t.Helper()
	for _, e := range tr.Entries {
		if string(e.Name) == name {
			return e
		}
	}
	t.Fatalf("no entry named %q", name)
	return format.TreeEntry{}
}

// fakeStableInfo wraps a real os.FileInfo but reports a caller-chosen size
// and mtime instead of the real ones, and no Sys(), so the in-flight
// change detection's stat seam can be made to disagree with itself
// without touching the real filesystem clock.
type fakeStatInfo struct {
	os.FileInfo
	size  int64
	mtime time.Time
}

func (f fakeStatInfo) Size() int64        { return f.size }
func (f fakeStatInfo) ModTime() time.Time { return f.mtime }
func (f fakeStatInfo) Sys() any           { return nil }

// TestUnstableFileIsFlaggedAndReported uses the stat seam to make every
// restat of one file disagree with the one before it, so the in-flight
// change detection never sees two matching stats and exhausts its
// retries. It asserts the tree entry carries UNSTABLE and the commit
// summary lists the path under the "flagged" branch.
func TestUnstableFileIsFlaggedAndReported(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "content of a")
	target, err := filepath.Abs(filepath.Join(src, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock

	var calls int
	w.Stat = func(path string) (os.FileInfo, error) {
		real, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if path != target {
			return real, nil
		}
		calls++
		return fakeStatInfo{FileInfo: real, size: real.Size() + int64(calls), mtime: real.ModTime()}, nil
	}

	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	if len(sum.Unstable) != 1 {
		t.Fatalf("Summary.Unstable = %v, want exactly one entry", sum.Unstable)
	}
	if sum.Unstable[0].Path != "a.txt" || sum.Unstable[0].Branch != "flagged" {
		t.Fatalf("Summary.Unstable[0] = %+v, want path a.txt branch flagged", sum.Unstable[0])
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	sourceRootEntry := rootTree.Entries[0]
	dirTree := loadTree(t, staging, ID(sourceRootEntry.ContentID))
	entry := findEntry(t, dirTree, "a.txt")
	if entry.EntryFlags&format.EntryFlagUnstable == 0 {
		t.Fatalf("a.txt entry flags = %#x, want UNSTABLE set", entry.EntryFlags)
	}
}

// TestStableTreeGetsNoUnstableFlags commits an ordinary, unmodified
// source tree twice and checks that neither commit flags any entry
// UNSTABLE and that the two commits produce byte-identical staging
// trees. It reuses TestCommitTwiceIsByteIdentical's fixture and byte
// comparison, adding the UNSTABLE check.
func TestStableTreeGetsNoUnstableFlags(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)

	staging1 := t.TempDir()
	w1 := NewWriter(staging1)
	w1.Now = fixedClock
	_, sum1, err := w1.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum1.Unstable) != 0 {
		t.Fatalf("first commit: Summary.Unstable = %v, want none", sum1.Unstable)
	}

	staging2 := t.TempDir()
	w2 := NewWriter(staging2)
	w2.Now = fixedClock
	_, sum2, err := w2.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum2.Unstable) != 0 {
		t.Fatalf("second commit: Summary.Unstable = %v, want none", sum2.Unstable)
	}

	files1 := listFiles(t, staging1)
	files2 := listFiles(t, staging2)
	if len(files1) != len(files2) {
		t.Fatalf("file count differs: %d vs %d", len(files1), len(files2))
	}
	for i := range files1 {
		if files1[i] != files2[i] {
			t.Fatalf("path %d differs: %s vs %s", i, files1[i], files2[i])
		}
		b1, err := os.ReadFile(filepath.Join(staging1, files1[i]))
		if err != nil {
			t.Fatal(err)
		}
		b2, err := os.ReadFile(filepath.Join(staging2, files2[i]))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("object %s differs between the two commits", files1[i])
		}
	}
}

// TestConcurrentMutationDuringCommit runs Commit against a several-MiB
// file while a goroutine rewrites its bytes and bumps its mtime in a
// tight loop, stopping only once Commit returns. Run with -race. The
// mutation is not guaranteed to land inside the narrow window between
// the writer's two stats, so the test only asserts the entry is flagged
// UNSTABLE when the race actually triggered; otherwise it logs that and
// passes.
func TestConcurrentMutationDuringCommit(t *testing.T) {
	src := t.TempDir()
	target := filepath.Join(src, "big.bin")
	const fileSize = 8 << 20
	initial := bytes.Repeat([]byte{0xAB}, fileSize)
	mustWriteBytes(t, target, initial)

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := bytes.Repeat([]byte{0xCD}, fileSize)
		mtime := time.Now()
		for {
			select {
			case <-stop:
				return
			default:
			}
			f, err := os.OpenFile(target, os.O_WRONLY, 0o644)
			if err == nil {
				_, _ = f.WriteAt(buf[:1<<20], 0)
				_ = f.Close()
			}
			mtime = mtime.Add(time.Second)
			_ = os.Chtimes(target, mtime, mtime)
		}
	}()

	snapID, sum, err := w.Commit(src)
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}

	if len(sum.Unstable) == 0 {
		t.Logf("the mutation goroutine did not land inside the detection window on this run; nothing to assert")
		return
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	dirTree := loadTree(t, staging, ID(rootTree.Entries[0].ContentID))
	entry := findEntry(t, dirTree, "big.bin")
	if entry.EntryFlags&format.EntryFlagUnstable == 0 {
		t.Fatalf("big.bin entry flags = %#x, want UNSTABLE set since Summary reported it unstable", entry.EntryFlags)
	}
}

func mustWriteBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// recomputeID rebuilds the content id of a decoded object the same way
// the writer computed it: the hash of the bytes after the common header
// and the object header.
func recomputeID(v any) (ID, error) {
	switch t := v.(type) {
	case *format.Chunk:
		payload, err := Decompress(t.Payload, t.ObjectHeader.Compression, t.ObjectHeader.PayloadLen)
		if err != nil {
			return ID{}, err
		}
		return ComputeID(payload), nil
	case *format.Blob:
		buf := make([]byte, t.EncodedLen())
		if _, err := t.Encode(buf); err != nil {
			return ID{}, err
		}
		return ComputeID(buf[format.CommonHeaderLen+format.ObjectHeaderLen:]), nil
	case *format.Tree:
		buf := make([]byte, t.EncodedLen())
		if _, err := t.Encode(buf); err != nil {
			return ID{}, err
		}
		return ComputeID(buf[format.CommonHeaderLen+format.ObjectHeaderLen:]), nil
	case *format.Snapshot:
		buf := make([]byte, t.EncodedLen())
		if _, err := t.Encode(buf); err != nil {
			return ID{}, err
		}
		return ComputeID(buf[format.CommonHeaderLen+format.ObjectHeaderLen:]), nil
	default:
		return ID{}, errUnexpectedType
	}
}
