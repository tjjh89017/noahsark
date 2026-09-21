package object

import (
	"bytes"
	"errors"
	"io"
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

// decodeObject reads the CommonHeader at the start of buf and decodes the
// chunk, blob, tree, or snapshot it names. It is a test-only stand-in for
// scanning an object file of unknown kind.
func decodeObject(buf []byte) (any, error) {
	var h format.CommonHeader
	if err := h.Decode(buf); err != nil {
		return nil, err
	}
	switch h.MagicKind {
	case format.MagicChunk:
		var v format.Chunk
		if _, err := v.Decode(buf); err != nil {
			return nil, err
		}
		return &v, nil
	case format.MagicBlob:
		var v format.Blob
		if _, err := v.Decode(buf); err != nil {
			return nil, err
		}
		return &v, nil
	case format.MagicTree:
		var v format.Tree
		if _, err := v.Decode(buf); err != nil {
			return nil, err
		}
		return &v, nil
	case format.MagicSnapshot:
		var v format.Snapshot
		if _, err := v.Decode(buf); err != nil {
			return nil, err
		}
		return &v, nil
	default:
		return nil, format.ErrBadMagic
	}
}

// verifyAllObjectsValid decodes every object and snapshot file under
// staging and checks that recomputing its content id from the decoded
// value reproduces the file name. It fails the test on the first object
// that does not decode or whose id does not match.
func verifyAllObjectsValid(t *testing.T, staging string) {
	t.Helper()
	for _, rel := range listFiles(t, staging) {
		path := filepath.Join(staging, rel)
		buf, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		v, err := decodeObject(buf)
		if err != nil {
			t.Fatalf("%s: decode: %v", rel, err)
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
	v, err := decodeObject(buf)
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
	v, err := decodeObject(buf)
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
// the writer computed it: the hash of the object's kind byte and the
// bytes after the common header and the object header.
func recomputeID(v any) (ID, error) {
	switch t := v.(type) {
	case *format.Chunk:
		payload, err := Decompress(t.Payload, t.ObjectHeader.Compression, t.ObjectHeader.PayloadLen)
		if err != nil {
			return ID{}, err
		}
		return ComputeID(format.ObjectKindChunk, payload), nil
	case *format.Blob:
		buf := make([]byte, t.EncodedLen())
		if _, err := t.Encode(buf); err != nil {
			return ID{}, err
		}
		return ComputeID(format.ObjectKindBlob, buf[format.CommonHeaderLen+format.ObjectHeaderLen:]), nil
	case *format.Tree:
		buf := make([]byte, t.EncodedLen())
		if _, err := t.Encode(buf); err != nil {
			return ID{}, err
		}
		return ComputeID(format.ObjectKindTree, buf[format.CommonHeaderLen+format.ObjectHeaderLen:]), nil
	case *format.Snapshot:
		buf := make([]byte, t.EncodedLen())
		if _, err := t.Encode(buf); err != nil {
			return ID{}, err
		}
		return ComputeID(format.ObjectKindSnapshot, buf[format.CommonHeaderLen+format.ObjectHeaderLen:]), nil
	default:
		return ID{}, errUnexpectedType
	}
}

// TestCommitMessageStoredAsSnapshotMeta asserts that Writer.Message is
// written as the snapshot's SnapshotMetaMessage TLV, and round-trips
// through Decode; and that an empty Message writes no TLV at all.
func TestCommitMessageStoredAsSnapshotMeta(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	w.Message = "a test commit message"
	id, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(staging, "snapshots", id.TextForm()))
	if err != nil {
		t.Fatal(err)
	}
	var snap format.Snapshot
	if _, err := snap.Decode(raw); err != nil {
		t.Fatal(err)
	}
	if snap.MetaCount != 1 || len(snap.Meta) != 1 {
		t.Fatalf("meta count = %d, want 1", snap.MetaCount)
	}
	if snap.Meta[0].Tag != format.SnapshotMetaMessage {
		t.Fatalf("meta tag = %d, want SnapshotMetaMessage", snap.Meta[0].Tag)
	}
	if got := string(snap.Meta[0].Value); got != w.Message {
		t.Fatalf("meta value = %q, want %q", got, w.Message)
	}
}

// TestCommitNoMessageWritesNoMeta asserts that an empty Message writes
// no metadata TLV.
func TestCommitNoMessageWritesNoMeta(t *testing.T) {
	src := t.TempDir()
	buildFixture(t, src)

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	id, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(staging, "snapshots", id.TextForm()))
	if err != nil {
		t.Fatal(err)
	}
	var snap format.Snapshot
	if _, err := snap.Decode(raw); err != nil {
		t.Fatal(err)
	}
	if snap.MetaCount != 0 || len(snap.Meta) != 0 {
		t.Fatalf("meta count = %d, want 0", snap.MetaCount)
	}
}

// TestCommitRewritesATruncatedExistingObject pre-places a 0-byte file
// under the exact name a chunk's content id would use, standing in for
// a staged object a prior crash left truncated. Commit must not accept
// it as an existing valid object: a size mismatch alone must make it
// rewrite the file with the real encoded bytes.
func TestCommitRewritesATruncatedExistingObject(t *testing.T) {
	src := t.TempDir()
	content := "content of a"
	mustWrite(t, filepath.Join(src, "a.txt"), content)

	staging := t.TempDir()
	id := ComputeID(format.ObjectKindChunk, []byte(content))
	objPath := filepath.Join(staging, "objects", id.FanoutByte(), id.TextForm())
	if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	w := NewWriter(staging)
	w.Now = fixedClock
	_, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(objPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() == 0 {
		t.Fatalf("object file at %s is still 0 bytes after commit", objPath)
	}
	verifyAllObjectsValid(t, staging)
}

// skipIfRoot skips a test that depends on mode 0000 blocking a read: the
// root user ignores file permission bits, so the fixture would not
// reproduce an unreadable path.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 does not block a read")
	}
}

// TestCommitSkipsOneUnreadableFileAndKeepsTheRest commits a source with
// one readable file and one file with no read permission. The commit
// must still produce a snapshot, must still include the readable file,
// must not include the unreadable one, and must report it skipped. One
// unreadable file must never stop the whole commit.
func TestCommitSkipsOneUnreadableFileAndKeepsTheRest(t *testing.T) {
	skipIfRoot(t)

	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "content of a")
	bPath := filepath.Join(src, "b.txt")
	mustWrite(t, bPath, "content of b")
	if err := os.Chmod(bPath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(bPath, 0o644) }()

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock

	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatalf("Commit returned a hard error, want the unreadable file skipped: %v", err)
	}

	if len(sum.Skipped) != 1 || sum.Skipped[0].Path != "b.txt" {
		t.Fatalf("Summary.Skipped = %+v, want exactly one entry for b.txt", sum.Skipped)
	}
	if sum.Skipped[0].Reason == "" {
		t.Fatalf("Summary.Skipped[0].Reason is empty, want the open error text")
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	dirTree := loadTree(t, staging, ID(rootTree.Entries[0].ContentID))
	for _, e := range dirTree.Entries {
		if string(e.Name) == "b.txt" {
			t.Fatalf("dirTree holds b.txt, want it dropped from the tree")
		}
	}
	findEntry(t, dirTree, "a.txt")
}

// TestCommitSkipsOneUnreadableSubdirectory commits a source with one
// readable subdirectory and one subdirectory with no read permission.
// The unreadable subdirectory is skipped and reported; the rest of the
// tree still commits.
func TestCommitSkipsOneUnreadableSubdirectory(t *testing.T) {
	skipIfRoot(t)

	src := t.TempDir()
	mustMkdir(t, filepath.Join(src, "good"))
	mustWrite(t, filepath.Join(src, "good", "a.txt"), "content of a")
	badDir := filepath.Join(src, "bad")
	mustMkdir(t, badDir)
	mustWrite(t, filepath.Join(badDir, "hidden.txt"), "content of hidden")
	if err := os.Chmod(badDir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(badDir, 0o755) }()

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock

	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatalf("Commit returned a hard error, want the unreadable directory skipped: %v", err)
	}

	if len(sum.Skipped) != 1 || sum.Skipped[0].Path != "bad" {
		t.Fatalf("Summary.Skipped = %+v, want exactly one entry for bad", sum.Skipped)
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	dirTree := loadTree(t, staging, ID(rootTree.Entries[0].ContentID))
	for _, e := range dirTree.Entries {
		if string(e.Name) == "bad" {
			t.Fatalf("dirTree holds bad, want it dropped from the tree")
		}
	}
	goodEntry := findEntry(t, dirTree, "good")
	goodTree := loadTree(t, staging, ID(goodEntry.ContentID))
	findEntry(t, goodTree, "a.txt")
}

// errInjectedMidRead is the error a fake reader returns partway through
// a file, standing in for an EIO the real filesystem could return.
var errInjectedMidRead = errors.New("object: injected mid-read failure")

// failAfterNReader returns n bytes of content, then errInjectedMidRead
// on every further read.
type failAfterNReader struct {
	remaining []byte
}

func (r *failAfterNReader) Read(p []byte) (int, error) {
	if len(r.remaining) == 0 {
		return 0, errInjectedMidRead
	}
	n := copy(p, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}

func (r *failAfterNReader) Close() error { return nil }

// TestCommitDropsEntryOnMidFileReadError injects a read error partway
// through one file's content, through the Writer's Open seam. The entry
// for that file must be dropped from the tree, not staged with
// truncated content, and the path must be reported skipped. A sibling
// file must still commit normally.
func TestCommitDropsEntryOnMidFileReadError(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), "content of a")
	badPath := filepath.Join(src, "b.txt")
	mustWriteBytes(t, badPath, bytes.Repeat([]byte{0x42}, 1<<20))

	badAbs, err := filepath.Abs(badPath)
	if err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock
	realOpen := w.Open
	w.Open = func(path string) (io.ReadCloser, error) {
		if path == badAbs {
			return &failAfterNReader{remaining: bytes.Repeat([]byte{0x42}, 4096)}, nil
		}
		return realOpen(path)
	}

	snapID, sum, err := w.Commit(src)
	if err != nil {
		t.Fatalf("Commit returned a hard error, want the mid-read failure skipped: %v", err)
	}

	if len(sum.Skipped) != 1 || sum.Skipped[0].Path != "b.txt" {
		t.Fatalf("Summary.Skipped = %+v, want exactly one entry for b.txt", sum.Skipped)
	}

	snap := loadSnapshot(t, staging, snapID)
	rootTree := loadTree(t, staging, ID(snap.RootTree))
	dirTree := loadTree(t, staging, ID(rootTree.Entries[0].ContentID))
	for _, e := range dirTree.Entries {
		if string(e.Name) == "b.txt" {
			t.Fatalf("dirTree holds b.txt, want it dropped after the mid-read failure")
		}
	}
	findEntry(t, dirTree, "a.txt")
}
