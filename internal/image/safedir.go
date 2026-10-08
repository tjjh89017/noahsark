package image

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Dir is a directory that image build holds open. image build runs as
// root on a repository of a user with no privilege, and that user can
// rename and replace each entry while the build runs. Each later open
// goes through the descriptor of Dir, never through a path again.
type Dir struct {
	f *os.File
	// Path names the directory in messages. It is not opened again.
	Path string
	// UID and GID are the owner of the directory that fstat reports.
	UID, GID uint32
	// Perm holds the permission bits of the directory.
	Perm os.FileMode
}

// openFlags are the flags of each open below a Dir: never follow a
// final symlink, never block on a FIFO, never take a terminal.
const openFlags = syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_NOCTTY | syscall.O_CLOEXEC

// OpenDir opens the directory at path. It follows a symlink in the
// path, because the caller names the path. It never reads a file of
// the directory yet.
func OpenDir(path string) (*Dir, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return newDir(fd, path)
}

// OpenDirNoFollow opens the directory at path, and refuses it when the
// last element of path is a symlink.
func OpenDirNoFollow(path string) (*Dir, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, openDirError(path, err)
	}
	return newDir(fd, path)
}

// Subdir opens the directory name of d. It refuses a symlink, and a
// directory that does not belong to the owner of d.
func (d *Dir) Subdir(name string) (*Dir, error) {
	path := filepath.Join(d.Path, name)
	fd, err := syscall.Openat(d.fd(), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, openDirError(path, err)
	}
	sub, err := newDir(fd, path)
	if err != nil {
		return nil, err
	}
	if err := d.checkOwner(path, sub.UID); err != nil {
		_ = sub.Close()
		return nil, err
	}
	return sub, nil
}

// ReadFile reads the regular file name of d. It refuses a symlink, an
// entry that is not a regular file, and a file that does not belong to
// the owner of d. A hard link to a file of another user thus never
// gives its content to the owner of d. No error holds file content.
func (d *Dir) ReadFile(name string) ([]byte, error) {
	f, err := d.openFile(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, &os.PathError{Op: "read", Path: filepath.Join(d.Path, name), Err: err}
	}
	return data, nil
}

// openFile opens the regular file name of d with the checks of
// ReadFile.
func (d *Dir) openFile(name string) (*os.File, error) {
	path := filepath.Join(d.Path, name)
	fd, st, err := d.openEntry(name)
	if err != nil {
		return nil, err
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		_ = syscall.Close(fd)
		return nil, notRegular(path, st.Mode)
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openEntry opens the entry name of d for reading, of any type but a
// symlink or a socket, and returns its descriptor and its fstat. It
// refuses an entry that does not belong to the owner of d. The open
// does not block on a FIFO.
func (d *Dir) openEntry(name string) (int, syscall.Stat_t, error) {
	path := filepath.Join(d.Path, name)
	var st syscall.Stat_t
	fd, err := syscall.Openat(d.fd(), name, openFlags, 0)
	if err != nil {
		return -1, st, openError(path, err)
	}
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = syscall.Close(fd)
		return -1, st, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if err := d.checkOwner(path, st.Uid); err != nil {
		_ = syscall.Close(fd)
		return -1, st, err
	}
	return fd, st, nil
}

// notRegular is the refusal of an entry at path of type mode that is
// neither a regular file nor, where the caller takes one, a directory.
func notRegular(path string, mode uint32) error {
	return fmt.Errorf("%s is %s, not a regular file; image build refuses it", path, fileKind(mode))
}

// Exists reports whether d has an entry name of any kind, a symlink
// included. It never follows the entry.
func (d *Dir) Exists(name string) (bool, error) {
	fd, err := syscall.Openat(d.fd(), name, openFlags, 0)
	switch {
	case err == nil:
		_ = syscall.Close(fd)
		return true, nil
	case errors.Is(err, syscall.ENOENT):
		return false, nil
	case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENXIO):
		// ELOOP is a symlink, ENXIO a socket: both are entries.
		return true, nil
	default:
		return false, &os.PathError{Op: "open", Path: filepath.Join(d.Path, name), Err: err}
	}
}

// Remove removes the entry name of d. It removes a symlink, never the
// file that the symlink names.
func (d *Dir) Remove(name string) error {
	if err := syscall.Unlinkat(d.fd(), name); err != nil {
		return &os.PathError{Op: "remove", Path: filepath.Join(d.Path, name), Err: err}
	}
	return nil
}

// Close closes the descriptor of d.
func (d *Dir) Close() error {
	return d.f.Close()
}

func (d *Dir) fd() int {
	return int(d.f.Fd())
}

// checkOwner refuses an entry at path of owner uid when uid is not the
// owner of d.
func (d *Dir) checkOwner(path string, uid uint32) error {
	if uid != d.UID {
		return fmt.Errorf("%s belongs to uid %d, not to uid %d, the owner of %s; image build refuses it", path, uid, d.UID, d.Path)
	}
	return nil
}

func newDir(fd int, path string) (*Dir, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = syscall.Close(fd)
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	return &Dir{
		f: os.NewFile(uintptr(fd), path), Path: path,
		UID: st.Uid, GID: st.Gid, Perm: os.FileMode(st.Mode & 0o777),
	}, nil
}

// openError names the refusal of an open with O_NOFOLLOW: ELOOP is a
// symlink at the last element of path.
func openError(path string, err error) error {
	if errors.Is(err, syscall.ELOOP) {
		return symlinkRefusal(path)
	}
	if errors.Is(err, syscall.ENXIO) {
		return fmt.Errorf("%s is a socket, not a regular file; image build refuses it", path)
	}
	return &os.PathError{Op: "open", Path: path, Err: err}
}

// openDirError names the refusal of an open with O_DIRECTORY and
// O_NOFOLLOW. Linux gives ENOTDIR, not ELOOP, for a symlink there; the
// lstat only picks the message.
func openDirError(path string, err error) error {
	if errors.Is(err, syscall.ENOTDIR) {
		if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return symlinkRefusal(path)
		}
	}
	return openError(path, err)
}

// fileKind names the file type of mode for a refusal.
func fileKind(mode uint32) string {
	switch mode & syscall.S_IFMT {
	case syscall.S_IFDIR:
		return "a directory"
	case syscall.S_IFLNK:
		return "a symbolic link"
	case syscall.S_IFIFO:
		return "a FIFO"
	case syscall.S_IFSOCK:
		return "a socket"
	case syscall.S_IFCHR:
		return "a character device"
	case syscall.S_IFBLK:
		return "a block device"
	default:
		return "a special file"
	}
}

// symlinkRefusal refuses a symbolic link in a tree that image build
// reads.
func symlinkRefusal(path string) error {
	return fmt.Errorf("%s is a symbolic link; image build does not follow it", path)
}
