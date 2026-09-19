//go:build unix

package restore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withChmodError overrides chmodFn for the duration of the test, always
// returning err, and restores the real os.Chmod afterward.
func withChmodError(t *testing.T, err error) {
	t.Helper()
	orig := chmodFn
	chmodFn = func(name string, mode os.FileMode) error { return err }
	t.Cleanup(func() { chmodFn = orig })
}

// withPrivileged overrides privileged for the duration of the test.
func withPrivileged(t *testing.T, v bool) {
	t.Helper()
	orig := privileged
	privileged = func() bool { return v }
	t.Cleanup(func() { privileged = orig })
}

// withChownError overrides chownFn and lchownFn for the duration of the
// test, always returning err. Both are overridden together: chownFn
// applies to a directory or a regular file, lchownFn to a symlink
// entry, and a fixture tree built by buildFixtureSrc has both kinds.
func withChownError(t *testing.T, err error) {
	t.Helper()
	origChown, origLchown := chownFn, lchownFn
	chownFn = func(name string, uid, gid int) error { return err }
	lchownFn = func(name string, uid, gid int) error { return err }
	t.Cleanup(func() {
		chownFn = origChown
		lchownFn = origLchown
	})
}

// TestApplyMetadataRecordsFailure asserts that a Chmod failure is
// recorded as a metadata_not_applied event, with the path, the field
// name and the underlying error, and that the walk still writes the
// file's content and finishes without a hard error.
func TestApplyMetadataRecordsFailure(t *testing.T) {
	injected := errors.New("injected chmod failure")
	withChmodError(t, injected)

	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	outDir := t.TempDir()

	var got []MetadataFailure
	_, _, err := Restore(treeDir, snapID, outDir, WithMetadataFailure(func(f MetadataFailure) {
		got = append(got, f)
	}))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no metadata failure recorded, want at least one")
	}
	for _, f := range got {
		if f.Field != "mode" {
			t.Fatalf("field = %q, want %q", f.Field, "mode")
		}
		if !errors.Is(f.Err, injected) {
			t.Fatalf("err = %v, want %v", f.Err, injected)
		}
		if f.Path == "" {
			t.Fatal("empty path in a metadata failure")
		}
	}

	// The file itself was still written, despite the metadata failure.
	if _, err := os.Stat(filepath.Join(outDir, srcDir, "small.txt")); err != nil {
		t.Fatalf("small.txt: %v", err)
	}
}

// TestApplyMetadataSkipsOwnerWhenUnprivileged asserts the design's rule
// for a non-root restore: ownership is not attempted at all, so it
// never produces a metadata_not_applied event, matching the implied
// --no-owner behaviour of OPERATIONS.md's metadata restore policy. The
// test runs as whatever user the test suite runs as; it forces the
// unprivileged branch through the privileged seam so it holds
// regardless of who runs the test.
func TestApplyMetadataSkipsOwnerWhenUnprivileged(t *testing.T) {
	withPrivileged(t, false)
	withChownError(t, errors.New("chown must not be called"))

	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	outDir := t.TempDir()

	var got []MetadataFailure
	_, _, err := Restore(treeDir, snapID, outDir, WithMetadataFailure(func(f MetadataFailure) {
		got = append(got, f)
	}))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	for _, f := range got {
		if f.Field == "owner" {
			t.Fatalf("an owner failure was reported although the restore is unprivileged: %+v", f)
		}
	}
}

// TestApplyMetadataReportsOwnerFailureWhenPrivileged asserts the other
// side of the rule: when the restore is privileged and Chown genuinely
// fails, that is a real failure and is reported like any other field.
func TestApplyMetadataReportsOwnerFailureWhenPrivileged(t *testing.T) {
	withPrivileged(t, true)
	injected := errors.New("injected chown failure")
	withChownError(t, injected)

	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	outDir := t.TempDir()

	var got []MetadataFailure
	_, _, err := Restore(treeDir, snapID, outDir, WithMetadataFailure(func(f MetadataFailure) {
		got = append(got, f)
	}))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	found := false
	for _, f := range got {
		if f.Field == "owner" {
			found = true
			if !errors.Is(f.Err, injected) {
				t.Fatalf("err = %v, want %v", f.Err, injected)
			}
		}
	}
	if !found {
		t.Fatal("no owner failure reported although chown was forced to fail while privileged")
	}
}

// TestSymlinkMetadataNeverFollowsLink asserts that restoring a symlink
// entry never applies mode or times to the link's target: the target's
// own mode and mtime, as restored from its own tree entry, are
// unaffected by the symlink entry that points at it.
func TestSymlinkMetadataNeverFollowsLink(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	outDir := t.TempDir()

	if _, _, err := Restore(treeDir, snapID, outDir); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	root := filepath.Join(outDir, srcDir)
	target := filepath.Join(root, "small.txt")
	link := filepath.Join(root, "link-to-small")

	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("link-to-small: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link-to-small is not a symlink")
	}

	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("small.txt: %v", err)
	}
	// small.txt was committed with mode 0644. A symlink's own lstat
	// mode is 0777 on Linux; if restoreSymlink ever called Chmod(link,
	// ...) with the symlink entry's own mode, Chmod would follow the
	// link and leave small.txt at 0777 instead. Seeing 0644 here shows
	// the symlink entry never reached the target through a follow.
	if targetInfo.Mode().Perm() != 0o644 {
		t.Fatalf("small.txt mode = %o, want 0644 (a symlink Chmod may have followed the link)", targetInfo.Mode().Perm())
	}
}
