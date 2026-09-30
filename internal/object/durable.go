package object

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// syncFile and renameFile are the steps of writeSyncRename and of
// fileBatch.flush that a test replaces to record their order.
var (
	syncFile   = (*os.File).Sync
	renameFile = os.Rename
)

// ReplaceFile writes data to path with mode 0644 through a temporary file
// in the same directory: it syncs the file, renames it over path, and
// syncs the directory. A crash leaves the old file or the new file, never
// a part of one.
func ReplaceFile(path string, data []byte) error {
	if err := writeSyncRename(path, data); err != nil {
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

// batchMaxFiles and batchMaxBytes bound one batch of the object writer.
// The batch holds an open file for each temporary file.
const (
	batchMaxFiles = 64
	batchMaxBytes = 64 << 20
)

// pendingFile is a temporary file of a fileBatch and the path that it
// gets at the flush.
type pendingFile struct {
	tmp  *os.File
	path string
}

// fileBatch writes files through temporary files, as writeSyncRename
// does, but it syncs and renames them in groups. A flush syncs every
// temporary file of a group at the same time, and then renames each one.
// Thus each file is synced before its rename, and the file system can
// write the syncs of one group in one journal commit. start runs the
// flush of a full group in the background, while the caller writes the
// next group. At most one background flush runs. The caller syncs the
// directories.
type fileBatch struct {
	maxFiles int
	maxBytes int64
	files    []pendingFile
	paths    map[string]bool
	bytes    int64
	// flushing holds the paths of the background flush, and done gives
	// its result. done is nil when no background flush runs.
	flushing map[string]bool
	done     chan error
}

func newFileBatch(maxFiles int, maxBytes int64) *fileBatch {
	return &fileBatch{maxFiles: maxFiles, maxBytes: maxBytes, paths: make(map[string]bool)}
}

// has reports whether path is a file of the batch that is not renamed
// yet, or that the background flush can still be renaming.
func (b *fileBatch) has(path string) bool {
	return b.paths[path] || b.flushing[path]
}

// full reports whether the group holds its limit of files or bytes.
func (b *fileBatch) full() bool {
	return len(b.files) >= b.maxFiles || b.bytes >= b.maxBytes
}

// add writes data to a temporary file in the directory of path. The
// file gets the name path at the flush of its group.
func (b *fileBatch) add(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	b.files = append(b.files, pendingFile{tmp: tmp, path: path})
	b.paths[path] = true
	b.bytes += int64(len(data))
	return nil
}

// start waits for the background flush, then starts the flush of the
// group in the background. It returns the error of the earlier flush.
func (b *fileBatch) start() error {
	if err := b.wait(); err != nil {
		return err
	}
	files := b.files
	b.flushing = b.paths
	b.reset()
	done := make(chan error, 1)
	b.done = done
	go func() { done <- syncAndRename(files) }()
	return nil
}

// flush waits for the background flush, then syncs and renames each file
// of the group. After a nil error, every file of the batch has its name.
func (b *fileBatch) flush() error {
	if err := b.wait(); err != nil {
		return err
	}
	files := b.files
	b.reset()
	return syncAndRename(files)
}

// wait waits for the background flush and returns its error.
func (b *fileBatch) wait() error {
	if b.done == nil {
		return nil
	}
	err := <-b.done
	b.done = nil
	b.flushing = nil
	return err
}

// abort waits for the background flush, then closes and removes each
// temporary file of the group, and renames none.
func (b *fileBatch) abort() {
	_ = b.wait()
	for _, f := range b.files {
		_ = f.tmp.Close()
	}
	removeTemps(b.files)
	b.reset()
}

func (b *fileBatch) reset() {
	b.files = nil
	b.paths = make(map[string]bool)
	b.bytes = 0
}

// syncAndRename syncs and closes each temporary file of files at the same
// time, then renames each one to its path. On an error, it removes each
// temporary file that is not renamed.
func syncAndRename(files []pendingFile) error {
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	for i, f := range files {
		wg.Go(func() {
			err := syncFile(f.tmp)
			if closeErr := f.tmp.Close(); err == nil {
				err = closeErr
			}
			errs[i] = err
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		removeTemps(files)
		return err
	}
	for i, f := range files {
		if err := renameFile(f.tmp.Name(), f.path); err != nil {
			removeTemps(files[i:])
			return err
		}
	}
	return nil
}

func removeTemps(files []pendingFile) {
	for _, f := range files {
		_ = os.Remove(f.tmp.Name())
	}
}

// writeSyncRename writes data to a temporary file in the directory of
// path with mode 0644, syncs it, and renames it over path. The caller
// syncs the directory.
func writeSyncRename(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(0o644)
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
