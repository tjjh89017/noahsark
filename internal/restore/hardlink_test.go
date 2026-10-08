//go:build unix

package restore

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestRestoreHardlinkedPathsAsIndependentFiles commits two paths that are
// one inode. It restores the snapshot and checks that each path is its own
// file with its own inode and the same content.
func TestRestoreHardlinkedPathsAsIndependentFiles(t *testing.T) {
	srcDir := t.TempDir()
	content := make([]byte, 1_400_000)
	rand.New(rand.NewSource(3)).Read(content)
	first := filepath.Join(srcDir, "first.bin")
	if err := os.WriteFile(first, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(srcDir, "sub", "second.bin")
	if err := os.Link(first, second); err != nil {
		t.Skipf("this filesystem has no hard link: %v", err)
	}
	assertSameInode(t, first, second, true)

	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	outDir := t.TempDir()
	rep, err := restoreTree(t, treeDir, snapID, outDir, false)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rep.Failed() {
		t.Fatalf("restore failed: %v", rep)
	}

	gotFirst := filepath.Join(outDir, "first.bin")
	gotSecond := filepath.Join(outDir, "sub", "second.bin")
	assertSameInode(t, gotFirst, gotSecond, false)
	for _, p := range []string{gotFirst, gotSecond} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("%s: restored content differs from the source", p)
		}
	}
}

func assertSameInode(t *testing.T, a, b string, want bool) {
	t.Helper()
	ia, err := os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	ib, err := os.Lstat(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.SameFile(ia, ib); got != want {
		t.Fatalf("%s and %s share one inode = %v, want %v", a, b, got, want)
	}
}
