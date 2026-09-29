package object

import (
	"os"
	"path/filepath"
)

// syncFile and renameFile are the steps of writeSyncRename that a test
// replaces to record their order.
var (
	syncFile   = (*os.File).Sync
	renameFile = os.Rename
)

// ReplaceFile writes data to path with mode 0644 through a temporary file
// in the same directory: it syncs the file, renames it over path, and
// syncs the directory. A crash leaves the old file or the new file, never
// a part of one.
func ReplaceFile(path string, data []byte) error {
	if err := writeSyncRename(path, data, 0o644); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(path))
}

// SyncDir syncs the directory dir, so that a new name in it is durable.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if closeErr := d.Close(); err == nil {
		err = closeErr
	}
	return err
}

// writeSyncRename writes data to a temporary file in the directory of
// path, syncs it, and renames it over path. A perm of 0 keeps the mode
// of os.CreateTemp. The caller syncs the directory.
func writeSyncRename(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil && perm != 0 {
		err = tmp.Chmod(perm)
	}
	if err == nil {
		err = syncFile(tmp)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = renameFile(tmpName, path)
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
