package restore

import (
	"os"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
)

// chmodFn, chtimesFn, chownFn and lchownFn are the system calls
// applyMetadata and applySymlinkOwner use, held in variables so a test
// can inject a failure without needing root or a broken filesystem.
var (
	chmodFn   = os.Chmod
	chtimesFn = os.Chtimes
	chownFn   = os.Chown
	lchownFn  = os.Lchown
	// privileged reports whether this restore can chown to an owner
	// other than the invoking user. OPERATIONS.md's metadata restore
	// policy treats a non-root restore as --no-owner: ownership is not
	// attempted at all, so a restore run by an ordinary user never
	// produces a metadata_not_applied event for owner. A test
	// overrides this to exercise the privileged path without root.
	privileged = func() bool { return os.Geteuid() == 0 }
)

// applyMetadata sets owner, mode and mtime from e, in that order: a
// chown clears the setuid and the setgid bits, thus the chmod must come
// after it. Each field is applied independently: a failure on one field
// is recorded and does not stop the other fields from being tried. When
// this restore is not privileged, ownership is skipped outright rather
// than attempted and reported, matching the implied --no-owner rule for
// a non-root restore.
func applyMetadata(dest string, e format.TreeEntry, wp *writePolicy) {
	if privileged() {
		if err := chownFn(dest, int(e.UID), int(e.GID)); err != nil {
			wp.metadataFailed(dest, "owner", err)
		}
	}
	if err := chmodFn(dest, modeFromEntry(e.Mode)); err != nil {
		wp.metadataFailed(dest, "mode", err)
	}
	mtime := time.Unix(e.MtimeSec, int64(e.MtimeNsec))
	if err := chtimesFn(dest, mtime, mtime); err != nil {
		wp.metadataFailed(dest, "times", err)
	}
}

// modeFromEntry converts a tree entry's stored permission bits to an
// os.FileMode. Go holds setuid, setgid and sticky in high bits of its
// own, thus a direct conversion of the stored bits drops all three.
func modeFromEntry(mode uint32) os.FileMode {
	m := os.FileMode(mode & 0o777)
	if mode&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if mode&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if mode&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m
}

// applySymlinkOwner sets a symlink's own owner with a no-follow chown,
// when this restore is privileged. A symlink has no mode of its own to
// restore on Linux, and this build has no no-follow time call without
// adding a new dependency (golang.org/x/sys is only an indirect
// dependency today), so a symlink entry gets owner only; its mode and
// times are left at whatever os.Symlink set.
func applySymlinkOwner(path string, e format.TreeEntry, wp *writePolicy) {
	if !privileged() {
		return
	}
	if err := lchownFn(path, int(e.UID), int(e.GID)); err != nil {
		wp.metadataFailed(path, "owner", err)
	}
}
