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
	if sum1 != sum2 {
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
