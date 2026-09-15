package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestGCApplyStagingObjectsSkipsAlreadyGoneFile checks that a gcObj
// whose staged file does not exist frees no bytes and is not counted
// deleted: only an object gc actually removed counts, even though the
// state log still moves it on to Deleted.
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
		id:   id,
		path: filepath.Join(dir, "objects", "no", "such-file"),
		size: 1234,
	}}

	var out bytes.Buffer
	deleted, bytesFreed := gcApplyStagingObjects(l, objs, false, &out)
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0: an already-gone file frees nothing this run", deleted)
	}
	if bytesFreed != 0 {
		t.Fatalf("bytesFreed = %d, want 0", bytesFreed)
	}

	rec, ok := l.Get(id)
	if !ok || rec.State != stage.Deleted {
		t.Fatalf("got %+v, %v, want Deleted: the state machine still moves on", rec, ok)
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

	objs := []gcObj{{id: id, path: path, size: 7}}

	var out bytes.Buffer
	deleted, bytesFreed := gcApplyStagingObjects(l, objs, false, &out)
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if bytesFreed != 7 {
		t.Fatalf("bytesFreed = %d, want 7", bytesFreed)
	}
}
