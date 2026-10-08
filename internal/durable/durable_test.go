package durable

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteFileReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := WriteFile(path, []byte("old"), 0o644, Replace); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o600, Replace); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	checkOnlyFile(t, dir, "f")
}

func TestWriteFileKeepEqualKeepsTheSameBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := WriteFile(path, []byte("data"), 0o644, KeepEqual); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1_000_000, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("data"), 0o644, KeepEqual); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Fatal("KeepEqual wrote a file that holds the same bytes")
	}
	if err := WriteFile(path, []byte("other"), 0o644, KeepEqual); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "other" {
		t.Fatalf("content = %q, want other", got)
	}
	checkOnlyFile(t, dir, "f")
}

func TestWriteFileErrorLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("data"), 0o644, Replace); err == nil {
		t.Fatal("WriteFile over a directory that is not empty returned no error")
	}
	checkOnlyFile(t, dir, "d")
}

func checkOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("directory holds %v, want only %s", entries, name)
	}
}
