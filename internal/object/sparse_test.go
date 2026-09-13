package object

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
)

// buildSparseContent returns a deterministic byte slice with real content
// at the start and the end and zero bytes in between, the shape a sparse
// file and its dense twin both carry.
func buildSparseContent() []byte {
	const size = 3 << 20 // 3 MiB: bigger than DefaultProfile.Min so the
	// chunker's real cut logic runs, not the short-input path.
	data := make([]byte, size)
	rand.New(rand.NewSource(11)).Read(data[:1<<20])
	rand.New(rand.NewSource(12)).Read(data[2<<20:])
	// data[1<<20 : 2<<20] stays zero: the hole region in the sparse twin.
	return data
}

// writeSparseFile creates path as a sparse file of len(data) bytes: it
// writes only the non-zero regions of data and leaves the all-zero middle
// region as an unwritten hole, so the file reads back byte-identical to
// data.
func writeSparseFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(data[:1<<20], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(data[2<<20:], 2<<20); err != nil {
		t.Fatal(err)
	}
}

// commitOneFileFixture commits a source tree holding exactly one file
// named "data.bin" at fixedMtime, and returns the staging directory and
// the snapshot id.
func commitOneFileFixture(t *testing.T, write func(path string, data []byte), data []byte, fixedMtime time.Time) (stagingDir string, snapID ID) {
	t.Helper()
	src := t.TempDir()
	path := filepath.Join(src, "data.bin")
	write(path, data)
	if err := os.Chtimes(path, fixedMtime, fixedMtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir = t.TempDir()
	w := NewWriter(stagingDir)
	w.Now = fixedClock
	id, _, err := w.Commit(src)
	if err != nil {
		t.Fatal(err)
	}
	return stagingDir, id
}

// decodeObject reads and decodes the object file for id from staging.
func decodeObject(t *testing.T, staging string, id ID, snapshot bool) any {
	t.Helper()
	var path string
	if snapshot {
		path = filepath.Join(staging, "snapshots", id.TextForm())
	} else {
		path = filepath.Join(staging, "objects", id.FanoutByte(), id.TextForm())
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := format.Dispatch(buf)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// fileEntry decodes staging's snapshot at snapID down to the single
// "data.bin" tree entry it holds.
func fileEntry(t *testing.T, staging string, snapID ID) format.TreeEntry {
	t.Helper()
	snap := decodeObject(t, staging, snapID, true).(*format.Snapshot)
	rootTree := decodeObject(t, staging, ID(snap.RootTree), false).(*format.Tree)
	if len(rootTree.Entries) != 1 {
		t.Fatalf("root tree has %d entries, want 1", len(rootTree.Entries))
	}
	dirTree := decodeObject(t, staging, ID(rootTree.Entries[0].ContentID), false).(*format.Tree)
	for _, e := range dirTree.Entries {
		if string(e.Name) == "data.bin" {
			return e
		}
	}
	t.Fatal("data.bin entry not found")
	return format.TreeEntry{}
}

// TestSparseAndDenseCommitsAgree commits the same bytes once as a dense
// file and once as a sparse file with a hole in the middle. Whether or
// not SEEK_HOLE works on this host, the two commits must reference the
// same blob, the same chunks, and byte-identical chunk and blob object
// files; the tree entries may differ only in the SPARSE bit.
func TestSparseAndDenseCommitsAgree(t *testing.T) {
	data := buildSparseContent()
	fixedMtime := fixedClock()

	denseStaging, denseSnap := commitOneFileFixture(t, func(path string, d []byte) {
		mustWrite(t, path, string(d))
	}, data, fixedMtime)

	sparseStaging, sparseSnap := commitOneFileFixture(t, func(path string, d []byte) {
		writeSparseFile(t, path, d)
	}, data, fixedMtime)

	denseEntry := fileEntry(t, denseStaging, denseSnap)
	sparseEntry := fileEntry(t, sparseStaging, sparseSnap)

	if denseEntry.EntryFlags&format.EntryFlagSparse != 0 {
		t.Fatalf("dense entry carries SPARSE")
	}

	// The blob id (ContentID of a regular entry) covers only the chunk
	// ids, lengths and offsets, none of which depend on the hole, so it
	// must match regardless of whether SEEK_HOLE worked.
	if denseEntry.ContentID != sparseEntry.ContentID {
		t.Fatalf("blob ids differ: dense %s, sparse %s",
			ID(denseEntry.ContentID).TextForm(), ID(sparseEntry.ContentID).TextForm())
	}

	// Every other field must match; only EntryFlags may differ, and only
	// in the SPARSE bit.
	if denseEntry.Size != sparseEntry.Size {
		t.Fatalf("size differs: %d vs %d", denseEntry.Size, sparseEntry.Size)
	}
	if denseEntry.MtimeSec != sparseEntry.MtimeSec || denseEntry.MtimeNsec != sparseEntry.MtimeNsec {
		t.Fatalf("mtime differs")
	}
	if denseEntry.Mode != sparseEntry.Mode {
		t.Fatalf("mode differs: %o vs %o", denseEntry.Mode, sparseEntry.Mode)
	}
	if denseEntry.EntryFlags&^format.EntryFlagSparse != sparseEntry.EntryFlags&^format.EntryFlagSparse {
		t.Fatalf("entry flags differ outside the SPARSE bit: dense %#x, sparse %#x",
			denseEntry.EntryFlags, sparseEntry.EntryFlags)
	}

	seekHoleWorked := sparseEntry.EntryFlags&format.EntryFlagSparse != 0
	t.Logf("SEEK_HOLE worked on this host: %v", seekHoleWorked)

	// The blob object file itself must be byte-identical between the two
	// commits, and so must every chunk it references.
	blob := decodeObject(t, denseStaging, ID(denseEntry.ContentID), false).(*format.Blob)
	compareObjectBytes(t, denseStaging, sparseStaging, ID(denseEntry.ContentID), false)
	for _, be := range blob.Entries {
		compareObjectBytes(t, denseStaging, sparseStaging, ID(be.ContentID), false)
	}
}

// compareObjectBytes reads id's object file from both staging directories
// and asserts they are byte-identical.
func compareObjectBytes(t *testing.T, stagingA, stagingB string, id ID, snapshot bool) {
	t.Helper()
	pathFor := func(staging string) string {
		if snapshot {
			return filepath.Join(staging, "snapshots", id.TextForm())
		}
		return filepath.Join(staging, "objects", id.FanoutByte(), id.TextForm())
	}
	a, err := os.ReadFile(pathFor(stagingA))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pathFor(stagingB))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("object %s differs between dense and sparse commits", id.TextForm())
	}
}
