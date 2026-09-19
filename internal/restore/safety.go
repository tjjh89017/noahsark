package restore

import (
	"errors"
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// ensureDir makes every component under parent, one level at a time,
// and returns the final directory. It never follows a symlink on the
// way: each component is checked with Lstat before it is created or
// entered, so a symlink planted in the output directory cannot redirect
// a restore outside it.
//
// A component that exists and is not a real directory is replaced only
// when overwrite is requested, and only by an unlink; without
// overwrite, ensureDir reports ok false, counts the path as skipped and
// leaves it exactly as found. The caller then leaves that whole subtree
// alone.
func ensureDir(parent string, components []string, wp *writePolicy) (path string, ok bool, err error) {
	path = parent
	for _, name := range components {
		child, err := joinSafe(path, name)
		if err != nil {
			return "", false, err
		}
		fi, err := os.Lstat(child)
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return "", false, err
		case fi.IsDir():
			path = child
			continue
		default:
			if wp == nil || !wp.overwrite {
				if wp != nil {
					wp.skipped++
				}
				return "", false, nil
			}
			if err := unlinkExisting("directory", child); err != nil {
				return "", false, err
			}
		}
		if err := os.Mkdir(child, 0o755); err != nil && !os.IsExist(err) {
			return "", false, err
		}
		path = child
	}
	return path, true, nil
}

// restoreSymlink creates a symlink at path with target. An existing
// symlink that already has the same target counts as resumed. Any other
// existing path stays as found and counts as skipped, unless overwrite
// is requested: then the path is unlinked first, which fails on a
// directory that still holds entries. The existing tree is never
// deleted recursively.
func restoreSymlink(path, target string, wp *writePolicy) error {
	fi, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return err
	default:
		if fi.Mode()&os.ModeSymlink != 0 {
			if current, err := os.Readlink(path); err == nil && current == target {
				if wp != nil {
					wp.resumed++
				}
				return nil
			}
		}
		if wp == nil || !wp.overwrite {
			if wp != nil {
				wp.skipped++
			}
			return nil
		}
		if err := unlinkExisting("symlink", path); err != nil {
			return err
		}
	}
	return os.Symlink(target, path)
}

// unlinkExisting removes one path that stands where kind must be
// created. It removes the path itself only, never a tree below it: a
// directory that still holds entries stays, and the caller reports the
// conflict. kind names what the restore wanted to create there.
func unlinkExisting(kind, path string) error {
	err := os.Remove(path)
	if err == nil {
		return nil
	}
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		err = pe.Err
	}
	return fmt.Errorf("%s %s: %w", kind, path, err)
}

// symlinkTarget returns the target an entry's symlink TLV carries.
func symlinkTarget(e format.TreeEntry) (string, error) {
	for _, t := range e.TLVs {
		if t.Type == format.TLVTypeSymlinkTarget {
			return string(t.Payload), nil
		}
	}
	return "", fmt.Errorf("symlink %q has no target TLV", e.Name)
}

// writeChunks writes every blob entry's payload into f at the entry's
// own file offset, then closes f. It reports the close error on the
// success path, because a write can fail at close alone.
//
// fetch reads one chunk payload. It reports ok false for a payload no
// provided disc holds; writeChunks then leaves that part of the file
// unwritten and reports complete false, so a caller that restores
// across discs can carry on.
func writeChunks(f *os.File, entries []format.BlobEntry, prog *progress.Reporter, fetch func(object.ID) (payload []byte, ok bool, err error)) (complete bool, err error) {
	complete = true
	for _, be := range entries {
		id := object.ID(be.ContentID)
		payload, ok, err := fetch(id)
		if err != nil {
			_ = f.Close()
			return false, err
		}
		if !ok {
			complete = false
			continue
		}
		if uint64(len(payload)) != be.Length {
			_ = f.Close()
			return false, fmt.Errorf("chunk %s: length %d, blob entry says %d", id.TextForm(), len(payload), be.Length)
		}
		if _, err := f.WriteAt(payload, int64(be.FileOffset)); err != nil {
			_ = f.Close()
			return false, err
		}
		prog.Add(int64(len(payload)))
	}
	if err := f.Close(); err != nil {
		return complete, err
	}
	return complete, nil
}
