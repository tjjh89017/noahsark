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

// MetadataFailure is one metadata field a restore could not apply to a
// path it had already written: Chmod, Chtimes or a privileged Chown or
// Lchown returned an error. Field is "mode", "times" or "owner".
type MetadataFailure struct {
	Path  string
	Field string
	Err   error
}

// recordMetadataFailure notes one metadata_not_applied event. The walk
// continues: a metadata field that cannot be applied is a recorded
// event, never a hard error.
func (wp *writePolicy) recordMetadataFailure(path, field string, err error) {
	if wp == nil {
		return
	}
	f := MetadataFailure{Path: path, Field: field, Err: err}
	wp.metadataFailures = append(wp.metadataFailures, f)
	if wp.onMetadataFailure != nil {
		wp.onMetadataFailure(f)
	}
}

// applyMetadata sets mode, mtime and, when this restore is privileged,
// owner from e. Each field is applied independently: a failure on one
// field is recorded and does not stop the other fields from being
// tried. When this restore is not privileged, ownership is skipped
// outright rather than attempted and reported, matching the implied
// --no-owner rule for a non-root restore.
func applyMetadata(dest string, e format.TreeEntry, wp *writePolicy) {
	if err := chmodFn(dest, os.FileMode(e.Mode&0o7777)); err != nil {
		wp.recordMetadataFailure(dest, "mode", err)
	}
	mtime := time.Unix(e.MtimeSec, int64(e.MtimeNsec))
	if err := chtimesFn(dest, mtime, mtime); err != nil {
		wp.recordMetadataFailure(dest, "times", err)
	}
	if privileged() {
		if err := chownFn(dest, int(e.UID), int(e.GID)); err != nil {
			wp.recordMetadataFailure(dest, "owner", err)
		}
	}
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
		wp.recordMetadataFailure(path, "owner", err)
	}
}
