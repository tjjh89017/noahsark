package main

import (
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestGCApplyStagingObjectsSkipsAlreadyGoneFile checks that a gcObj
// whose staged file does not exist frees no bytes and is not counted
// deleted: only an object gc actually removed counts, even though the
// state log still moves it on to ON-DISC.
func TestGCApplyStagingObjectsSkipsAlreadyGoneFile(t *testing.T) {
	dir := t.TempDir()
	l, err := stage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	id := object.ComputeID([]byte("gone"))
	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}

	objs := []gcObj{{
		id:          id,
		path:        filepath.Join(dir, "objects", "no", "such-file"),
		size:        1234,
		needsRecord: true,
	}}

	deleted, bytesFreed, failures := gcApplyStagingObjects(l, objs, false)
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0: an already-gone file frees nothing this run", deleted)
	}
	if bytesFreed != 0 {
		t.Fatalf("bytesFreed = %d, want 0", bytesFreed)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none: an already-gone file is not a failure", failures)
	}

	rec, ok := l.Get(id)
	if !ok || rec.State != stage.OnDiscOnly {
		t.Fatalf("got %+v, %v, want OnDiscOnly: the state machine still moves on", rec, ok)
	}
}

// TestGCApplyStagingObjectsCountsRealDelete is the control: a gcObj
// whose file exists is removed, counted, and its bytes freed.
func TestGCApplyStagingObjectsCountsRealDelete(t *testing.T) {
	dir := t.TempDir()
	l, err := stage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	id := object.ComputeID([]byte("present"))
	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "objects", "present")
	if err := writeFile(path, "payload"); err != nil {
		t.Fatal(err)
	}

	objs := []gcObj{{id: id, path: path, size: 7, needsRecord: true}}

	deleted, bytesFreed, failures := gcApplyStagingObjects(l, objs, false)
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if bytesFreed != 7 {
		t.Fatalf("bytesFreed = %d, want 7", bytesFreed)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
}
