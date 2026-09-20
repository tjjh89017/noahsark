//go:build unix

package restore

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestRestoreContinuesPastUnsupportedEntry asserts that a FIFO entry
// does not stop the walk: the restore reports the entry and still
// writes every later file.
func TestRestoreContinuesPastUnsupportedEntry(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(srcDir, "pipe"), 0o644); err != nil {
		t.Skipf("this platform has no FIFO: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "z.txt"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	outDir := t.TempDir()

	rep, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rep.Failed() {
		t.Fatal("an unsupported entry alone must not fail the restore")
	}

	root := filepath.Join(outDir, srcDir)
	for _, name := range []string{"a.txt", "z.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "pipe")); !os.IsNotExist(err) {
		t.Fatalf("the FIFO path exists after restore: %v", err)
	}
	got := problemsOf(rep, KindUnsupported)
	if len(got) != 1 {
		t.Fatalf("unsupported records = %v, want exactly one", got)
	}
	if !strings.Contains(got[0].Err.Error(), "FIFO") {
		t.Fatalf("problem = %q, want it to name the FIFO", got[0].Err)
	}
	if got[0].Path != filepath.Join(root, "pipe") {
		t.Fatalf("path = %q, want %q", got[0].Path, filepath.Join(root, "pipe"))
	}
}
