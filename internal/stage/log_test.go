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

func TestEnsurePackedIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))
	var discUUID [16]byte
	discUUID[0] = 0xCD

	if err := l.EnsurePacked(id, 5, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsurePacked(id, 5, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsurePacked(id, 5, discUUID); err != nil {
		t.Fatal(err)
	}

	rec, ok := l.Get(id)
	if !ok || rec.State != Packed || rec.RunSeq != 5 || rec.DiscUUID != discUUID || rec.Sequence != 1 {
		t.Fatalf("got %+v, %v, want a single Packed record at sequence 1", rec, ok)
	}

	// A different run or disc still appends: EnsurePacked only skips a
	// write that would record exactly what is already current.
	discUUID2 := discUUID
	discUUID2[1] = 0xEF
	if err := l.EnsurePacked(id, 6, discUUID2); err != nil {
		t.Fatal(err)
	}
	rec, ok = l.Get(id)
	if !ok || rec.RunSeq != 6 || rec.DiscUUID != discUUID2 || rec.Sequence != 2 {
		t.Fatalf("got %+v, %v, want run 6 at sequence 2", rec, ok)
	}
}

func TestCountState(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))
	id3 := object.ComputeID([]byte("three"))

	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id2, 1, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id3, 1, [16]byte{1}); err != nil {
		t.Fatal(err)
	}

	if n := l.CountState(Staged); n != 1 {
		t.Fatalf("CountState(Staged) = %d, want 1", n)
	}
	if n := l.CountState(Packed); n != 2 {
		t.Fatalf("CountState(Packed) = %d, want 2", n)
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
