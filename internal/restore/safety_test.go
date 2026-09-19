package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// restoredRootOf is the directory a restore of srcDir writes under
// outDir: the source's own absolute path, joined below outDir.
func restoredRootOf(outDir, srcDir string) string {
	return filepath.Join(outDir, srcDir)
}

// TestRestoreKeepsFileAtSymlinkPath asserts that a plain file at the
// path of a symlink entry survives a restore without WithOverwrite, and
// counts as skipped, and that WithOverwrite replaces it with the
// symlink.
func TestRestoreKeepsFileAtSymlinkPath(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	linkPath := filepath.Join(restoredRootOf(outDir, srcDir), "link-to-small")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkPath, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, skipped, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	got, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "mine" {
		t.Fatalf("existing file at the symlink path was replaced: %q", got)
	}

	if _, _, err := Restore(treeDir, snapID, outDir, WithOverwrite(true)); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("WithOverwrite did not create the symlink: %v", err)
	}
	if target != "small.txt" {
		t.Fatalf("symlink target = %q, want %q", target, "small.txt")
	}
}

// TestRestoreKeepsDirectoryAtSymlinkPath asserts that a directory tree
// at the path of a symlink entry is never deleted: without
// WithOverwrite it counts as skipped, and with WithOverwrite the
// restore still leaves it alone, reports it through
// WithOverwriteBlocked, and does not stop the walk.
func TestRestoreKeepsDirectoryAtSymlinkPath(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	linkPath := filepath.Join(restoredRootOf(outDir, srcDir), "link-to-small")
	inside := filepath.Join(linkPath, "keep.txt")
	if err := os.MkdirAll(linkPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, skipped, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("the existing directory was deleted: %v", err)
	}

	var got []OverwriteBlockedEntry
	_, skipped, err = Restore(treeDir, snapID, outDir, WithOverwrite(true), WithOverwriteBlocked(func(e OverwriteBlockedEntry) {
		got = append(got, e)
	}))
	if err != nil {
		t.Fatalf("WithOverwrite: want no error for a non-empty directory in the way, got %v", err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(got) != 1 {
		t.Fatalf("overwrite-blocked records = %v, want exactly one", got)
	}
	if got[0].Path != linkPath {
		t.Fatalf("path = %q, want %q", got[0].Path, linkPath)
	}
	if got[0].Kind != "symlink" {
		t.Fatalf("kind = %q, want %q", got[0].Kind, "symlink")
	}
	if !strings.Contains(got[0].Reason, "not empty") {
		t.Fatalf("reason = %q, want it to name the non-empty directory", got[0].Reason)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("WithOverwrite deleted the existing directory: %v", err)
	}
	// A later entry in the walk still lands: the conflict must not stop
	// the restore.
	if _, err := os.Stat(filepath.Join(restoredRootOf(outDir, srcDir), "small.txt")); err != nil {
		t.Fatalf("a later entry was not restored past the conflict: %v", err)
	}
}

// TestRestoreKeepsDirectoryAtFilePath is TestRestoreKeepsDirectoryAtSymlinkPath
// for a regular-file entry: a non-empty directory at that path survives
// --overwrite too, and the walk carries on to a later entry.
func TestRestoreKeepsDirectoryAtFilePath(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	filePath := filepath.Join(restoredRootOf(outDir, srcDir), "small.txt")
	inside := filepath.Join(filePath, "keep.txt")
	if err := os.MkdirAll(filePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	var got []OverwriteBlockedEntry
	_, skipped, err := Restore(treeDir, snapID, outDir, WithOverwrite(true), WithOverwriteBlocked(func(e OverwriteBlockedEntry) {
		got = append(got, e)
	}))
	if err != nil {
		t.Fatalf("WithOverwrite: want no error for a non-empty directory in the way, got %v", err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(got) != 1 {
		t.Fatalf("overwrite-blocked records = %v, want exactly one", got)
	}
	if got[0].Path != filePath {
		t.Fatalf("path = %q, want %q", got[0].Path, filePath)
	}
	if got[0].Kind != "file" {
		t.Fatalf("kind = %q, want %q", got[0].Kind, "file")
	}
	if !strings.Contains(got[0].Reason, "not empty") {
		t.Fatalf("reason = %q, want it to name the non-empty directory", got[0].Reason)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("WithOverwrite deleted the existing directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoredRootOf(outDir, srcDir), "link-to-small")); err != nil {
		t.Fatalf("a later entry was not restored past the conflict: %v", err)
	}
}

// TestEnsureDirLeavesUnremovableBlockerInPlace covers ensureDir's own
// unlink failure: a directory entry blocked by a non-directory that
// cannot be unlinked leaves the path exactly as found, counts it as
// skipped, and reports it, instead of failing the whole call.
func TestEnsureDirLeavesUnremovableBlockerInPlace(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can unlink a file in a read-only directory")
	}
	parent := t.TempDir()
	blocker := filepath.Join(parent, "sub")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	wp := &writePolicy{overwrite: true}
	_, ok, err := ensureDir(parent, []string{"sub"}, wp)
	if err != nil {
		t.Fatalf("ensureDir: want no error when the blocker cannot be unlinked, got %v", err)
	}
	if ok {
		t.Fatal("ensureDir: want ok false, the blocker was not removed")
	}
	if wp.skipped != 1 {
		t.Fatalf("skipped = %d, want 1", wp.skipped)
	}
	if len(wp.overwriteBlocked) != 1 {
		t.Fatalf("overwrite-blocked records = %v, want exactly one", wp.overwriteBlocked)
	}
	if wp.overwriteBlocked[0].Kind != "directory" {
		t.Fatalf("kind = %q, want %q", wp.overwriteBlocked[0].Kind, "directory")
	}
	if fi, err := os.Lstat(blocker); err != nil || fi.IsDir() {
		t.Fatalf("the blocking file was removed or replaced: %v", err)
	}
}

// TestRestoreResumesMatchingSymlink asserts that a symlink that already
// has the entry's own target counts as resumed, not as skipped.
func TestRestoreResumesMatchingSymlink(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	linkPath := filepath.Join(restoredRootOf(outDir, srcDir), "link-to-small")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("small.txt", linkPath); err != nil {
		t.Fatal(err)
	}

	resumed, skipped, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if resumed != 1 {
		t.Fatalf("resumed = %d, want 1", resumed)
	}
}

// TestRestoreDoesNotFollowSymlinkedDirectory asserts that a symlink to
// a directory outside OUT-DIR, at the path of a directory entry, never
// receives restored files.
func TestRestoreDoesNotFollowSymlinkedDirectory(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	outside := t.TempDir()
	subPath := filepath.Join(restoredRootOf(outDir, srcDir), "sub")
	if err := os.MkdirAll(filepath.Dir(subPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, subPath); err != nil {
		t.Fatal(err)
	}

	_, skipped, err := Restore(treeDir, snapID, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	assertEmptyDir(t, outside)
	if fi, err := os.Lstat(subPath); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced without WithOverwrite: %v", err)
	}

	if _, _, err := Restore(treeDir, snapID, outDir, WithOverwrite(true)); err != nil {
		t.Fatal(err)
	}
	assertEmptyDir(t, outside)
	if _, err := os.Stat(filepath.Join(subPath, "big2.bin")); err != nil {
		t.Fatalf("WithOverwrite did not restore into a real directory: %v", err)
	}
}

// TestRestoreDoesNotFollowSymlinkedRootComponent is the same check for
// an intermediate component of the root path a restore recreates under
// OUT-DIR.
func TestRestoreDoesNotFollowSymlinkedRootComponent(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	outDir := t.TempDir()
	outside := t.TempDir()
	parent := filepath.Join(outDir, filepath.Dir(srcDir))
	if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatal(err)
	}
	assertEmptyDir(t, outside)
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s: %d entries, want none: the restore followed a symlink out of OUT-DIR", dir, len(entries))
	}
}

// TestWriteChunksReportsCloseError asserts that the shared chunk writer
// reports the file's close error instead of dropping it.
func TestWriteChunksReportsCloseError(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "f"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = writeChunks(f, nil, nil, func(object.ID) ([]byte, bool, error) { return nil, false, nil })
	if err == nil {
		t.Fatal("writeChunks: want the close error")
	}
}
