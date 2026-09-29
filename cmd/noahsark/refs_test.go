package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteRefsReplacesTheFileByRename checks that writeRefs never writes
// refs.txt in place. A second name of the old file keeps the old bytes:
// the new file is a new inode that a rename put in place. A crash during
// the write thus leaves the old file or the new file, never a torn one.
func TestWriteRefsReplacesTheFileByRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "refs.txt")
	const old = "a sha256-old\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "old-link")
	if err := os.Link(path, keep); err != nil {
		t.Fatal(err)
	}

	if err := writeRefs(path, map[string]string{"b": "sha256-2", "a": "sha256-1"}); err != nil {
		t.Fatal(err)
	}

	kept, err := os.ReadFile(keep)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != old {
		t.Fatalf("the old inode holds %q, want %q: writeRefs wrote the file in place", kept, old)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a sha256-1\nb sha256-2\n" {
		t.Fatalf("refs.txt = %q", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("refs.txt mode = %v, want 0644", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("writeRefs left the temporary file %s", e.Name())
		}
	}
}

// TestCommitKeepsEachRefWhenItMovesOne checks that commit moves its ref
// and keeps the lines of the other refs.
func TestCommitKeepsEachRefWhenItMovesOne(t *testing.T) {
	repo, src := initAndCommit(t)
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=other", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	refs, err := readRefs(testLayout(t, repo).refsFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs["other"] == "" {
		t.Fatalf("refs = %v, want the date ref and other", refs)
	}
}
