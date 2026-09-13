package stage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

func TestEnsureStagedThenMarkPacked(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))

	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(id)
	if !ok || rec.State != Staged {
		t.Fatalf("got %+v, %v, want Staged", rec, ok)
	}

	var discUUID [16]byte
	discUUID[0] = 0xAB
	if err := l.MarkPacked(id, 3, discUUID); err != nil {
		t.Fatal(err)
	}
	rec, ok = l.Get(id)
	if !ok || rec.State != Packed || rec.RunSeq != 3 || rec.DiscUUID != discUUID {
		t.Fatalf("got %+v, %v, want Packed run 3", rec, ok)
	}

	// EnsureStaged after Packed must not resurrect the object to Staged.
	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	rec, ok = l.Get(id)
	if !ok || rec.State != Packed {
		t.Fatalf("EnsureStaged reset a Packed object: %+v", rec)
	}
}

func TestOpenReplaysAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id1, 1, [16]byte{1}); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec1, ok := l2.Get(id1)
	if !ok || rec1.State != Packed || rec1.RunSeq != 1 {
		t.Fatalf("id1: got %+v, %v", rec1, ok)
	}
	rec2, ok := l2.Get(id2)
	if !ok || rec2.State != Staged {
		t.Fatalf("id2: got %+v, %v", rec2, ok)
	}
}

func TestTruncatedTailStopsReplay(t *testing.T) {
	dir := t.TempDir()
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}

	// Corrupt the last record's CRC, simulating a crash during an
	// append.
	path := filepath.Join(dir, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l2.Get(id1); !ok {
		t.Fatal("id1 should have replayed before the truncated record")
	}
	if _, ok := l2.Get(id2); ok {
		t.Fatal("id2's record was corrupted and must not replay")
	}
}
