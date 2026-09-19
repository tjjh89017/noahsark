//go:build unix

package restore

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
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

	type record struct {
		path      string
		entryType uint8
	}
	var got []record
	_, _, err := Restore(treeDir, snapID, outDir, WithUnsupportedEntry(func(path string, entryType uint8) {
		got = append(got, record{path, entryType})
	}))
	if err != nil {
		t.Fatalf("Restore: %v", err)
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
	if len(got) != 1 {
		t.Fatalf("unsupported records = %v, want exactly one", got)
	}
	if got[0].entryType != format.EntryTypeFIFO {
		t.Fatalf("entry type = %d, want %d", got[0].entryType, format.EntryTypeFIFO)
	}
	if got[0].path != filepath.Join(root, "pipe") {
		t.Fatalf("path = %q, want %q", got[0].path, filepath.Join(root, "pipe"))
	}
}
