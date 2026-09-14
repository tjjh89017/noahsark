package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildIncludeFixtureSrc writes a source tree with nested directories, a
// symlink and an empty directory, so --include has something to match at
// every level: a lone file, a subtree, a sibling that must stay
// untouched, and a directory with nothing in it.
func buildIncludeFixtureSrc(t *testing.T) string {
	t.Helper()
	srcDir := t.TempDir()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(srcDir, "sub", "nested"), 0o755))
	must(os.WriteFile(filepath.Join(srcDir, "sub", "leaf.txt"), []byte("leaf content"), 0o644))
	must(os.WriteFile(filepath.Join(srcDir, "sub", "nested", "deep.txt"), []byte("deep content"), 0o644))
	must(os.Symlink("deep.txt", filepath.Join(srcDir, "sub", "nested", "link")))
	must(os.MkdirAll(filepath.Join(srcDir, "other"), 0o755))
	must(os.WriteFile(filepath.Join(srcDir, "other", "leaf2.txt"), []byte("other content"), 0o644))
	must(os.MkdirAll(filepath.Join(srcDir, "empty"), 0o755))
	return srcDir
}

// includePath joins srcDir's own segments (with its leading slash
// stripped, as --include expects) with the given snapshot-relative
// suffix.
func includePath(srcDir string, suffix string) string {
	return strings.TrimPrefix(srcDir, "/") + "/" + suffix
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("expected %s to not exist", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func TestRestoreIncludeSingleFile(t *testing.T) {
	srcDir := buildIncludeFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	inc := includePath(srcDir, "sub/leaf.txt")
	if _, err := Restore(treeDir, snapID, outDir, WithInclude([]string{inc})); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(outDir, srcDir)
	mustExist(t, filepath.Join(root, "sub", "leaf.txt"))
	mustNotExist(t, filepath.Join(root, "sub", "nested"))
	mustNotExist(t, filepath.Join(root, "other"))
	mustNotExist(t, filepath.Join(root, "empty"))
}

func TestRestoreIncludeSubtree(t *testing.T) {
	srcDir := buildIncludeFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	inc := includePath(srcDir, "sub")
	if _, err := Restore(treeDir, snapID, outDir, WithInclude([]string{inc})); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(outDir, srcDir)
	mustExist(t, filepath.Join(root, "sub", "leaf.txt"))
	mustExist(t, filepath.Join(root, "sub", "nested", "deep.txt"))
	mustExist(t, filepath.Join(root, "sub", "nested", "link"))
	target, err := os.Readlink(filepath.Join(root, "sub", "nested", "link"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "deep.txt" {
		t.Fatalf("symlink target = %q, want %q", target, "deep.txt")
	}
	mustNotExist(t, filepath.Join(root, "other"))
	mustNotExist(t, filepath.Join(root, "empty"))
}

func TestRestoreIncludeTwoIncludesUnion(t *testing.T) {
	srcDir := buildIncludeFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	incs := []string{
		includePath(srcDir, "sub/leaf.txt"),
		includePath(srcDir, "other/leaf2.txt"),
	}
	if _, err := Restore(treeDir, snapID, outDir, WithInclude(incs)); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(outDir, srcDir)
	mustExist(t, filepath.Join(root, "sub", "leaf.txt"))
	mustExist(t, filepath.Join(root, "other", "leaf2.txt"))
	mustNotExist(t, filepath.Join(root, "sub", "nested"))
	mustNotExist(t, filepath.Join(root, "empty"))
}

func TestRestoreIncludeEmptyDirectory(t *testing.T) {
	srcDir := buildIncludeFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	inc := includePath(srcDir, "empty")
	if _, err := Restore(treeDir, snapID, outDir, WithInclude([]string{inc})); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(outDir, srcDir)
	st, err := os.Stat(filepath.Join(root, "empty"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatalf("expected %s to be a directory", filepath.Join(root, "empty"))
	}
	mustNotExist(t, filepath.Join(root, "sub"))
	mustNotExist(t, filepath.Join(root, "other"))
}

func TestRestoreIncludeUnmatchedFailsBeforeWriting(t *testing.T) {
	srcDir := buildIncludeFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	badInclude := includePath(srcDir, "does/not/exist")
	_, err := Restore(treeDir, snapID, outDir, WithInclude([]string{badInclude}))
	if err == nil {
		t.Fatal("expected an unmatched-include error")
	}
	unmatched, ok := err.(*UnmatchedIncludeError)
	if !ok {
		t.Fatalf("expected *UnmatchedIncludeError, got %T: %v", err, err)
	}
	if len(unmatched.Paths) != 1 || unmatched.Paths[0] != badInclude {
		t.Fatalf("unmatched paths = %v, want [%s]", unmatched.Paths, badInclude)
	}

	root := filepath.Join(outDir, srcDir)
	mustNotExist(t, filepath.Join(root, "sub"))
	mustNotExist(t, filepath.Join(root, "other"))
	mustNotExist(t, filepath.Join(root, "empty"))
}

func TestRestoreIncludeLeadingSlashStripped(t *testing.T) {
	srcDir := buildIncludeFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	inc := "/" + includePath(srcDir, "sub/leaf.txt")
	if _, err := Restore(treeDir, snapID, outDir, WithInclude([]string{inc})); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(outDir, srcDir, "sub", "leaf.txt"))
}
