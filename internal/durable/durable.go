// Package durable writes a file so that a crash leaves the old file or
// the new file, never a part of one.
package durable

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
)

// Rule says what WriteFile does when a file exists at the path.
type Rule int

const (
	// Replace writes the new file over the file that exists.
	Replace Rule = iota
	// KeepEqual keeps a file that holds exactly the data, and replaces a
	// file that holds other bytes.
	KeepEqual
)

// WriteFile writes data to path with mode perm. It writes a temporary
// file in the directory of path, syncs it, renames it over path, and
// syncs the directory. The directory must exist. With KeepEqual, it does
// nothing when path already holds exactly data.
func WriteFile(path string, data []byte, perm fs.FileMode, rule Rule) error {
	if rule == KeepEqual {
		existing, err := os.ReadFile(path)
		if err == nil && bytes.Equal(existing, data) {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(perm)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpName, path)
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return SyncDir(dir)
}

// SyncDir syncs the directory dir, so that a new name in it is durable.
func SyncDir(dir string) error {
	return Sync(dir)
}

// Sync opens the file or the directory at path and syncs it.
func Sync(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}
