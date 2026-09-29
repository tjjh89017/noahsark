package image

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tjjh89017/noahsark/internal/progress"
)

// errInterrupted is the error of a populate that a signal stopped.
var errInterrupted = errors.New("interrupted by a signal")

// copyBufferBytes is the size of the buffer of one file copy.
const copyBufferBytes = 1 << 20

// names returns the names of the entries of d, sorted. It reads the
// directory from its start, thus a second call lists it again.
func (d *Dir) names() ([]string, error) {
	if _, err := d.f.Seek(0, io.SeekStart); err != nil {
		return nil, &os.PathError{Op: "seek", Path: d.Path, Err: err}
	}
	names, err := d.f.Readdirnames(-1)
	if err != nil {
		return nil, &os.PathError{Op: "readdir", Path: d.Path, Err: err}
	}
	slices.Sort(names)
	return names, nil
}

// visitTree calls file for each regular file below d, and enter for
// each directory before its entries. It opens each entry through the
// descriptor of its directory with the checks of Dir.openEntry, and
// refuses each entry that is not a regular file or a directory. file
// gets the open file and its fstat; visitTree closes the file.
func visitTree(d *Dir, rel string, enter func(rel string, st *syscall.Stat_t) error, file func(rel string, f *os.File, st *syscall.Stat_t) error) error {
	names, err := d.names()
	if err != nil {
		return err
	}
	for _, name := range names {
		fd, st, err := d.openEntry(name)
		if err != nil {
			return err
		}
		path := filepath.Join(d.Path, name)
		relName := filepath.Join(rel, name)
		switch st.Mode & syscall.S_IFMT {
		case syscall.S_IFDIR:
			sub, err := newDir(fd, path)
			if err != nil {
				return err
			}
			err = enter(relName, &st)
			if err == nil {
				err = visitTree(sub, relName, enter, file)
			}
			_ = sub.Close()
			if err != nil {
				return err
			}
		case syscall.S_IFREG:
			f := os.NewFile(uintptr(fd), path)
			err := file(relName, f, &st)
			_ = f.Close()
			if err != nil {
				return err
			}
		default:
			_ = syscall.Close(fd)
			return notRegular(path, st.Mode)
		}
	}
	return nil
}

// treeBytes sums the size of each regular file below d. It feeds a
// progress total, thus an error gives the sum up to that point.
func treeBytes(d *Dir) int64 {
	var total int64
	_ = visitTree(d, "",
		func(string, *syscall.Stat_t) error { return nil },
		func(_ string, _ *os.File, st *syscall.Stat_t) error {
			total += st.Size
			return nil
		})
	return total
}

// copyTreeInto copies the entries below src into the directory dest. A
// directory keeps its permission bits, and a regular file keeps its
// permission bits and its modification time. dest is on the image that
// root mounted in a directory that only root can change, thus dest is
// safe to name by path. The source is only read through descriptors.
// prog gets the size of each file when its copy ends. The copy stops
// with errInterrupted when stop becomes true.
func copyTreeInto(src *Dir, dest string, prog *progress.Reporter, stop *atomic.Bool) error {
	buf := make([]byte, copyBufferBytes)
	return visitTree(src, "",
		func(rel string, st *syscall.Stat_t) error {
			if stop.Load() {
				return errInterrupted
			}
			path := filepath.Join(dest, rel)
			if err := os.Mkdir(path, os.FileMode(st.Mode&0o777)); err != nil {
				return err
			}
			return os.Chmod(path, os.FileMode(st.Mode&0o777))
		},
		func(rel string, in *os.File, st *syscall.Stat_t) error {
			if err := copyFileInto(in, st, filepath.Join(dest, rel), buf, stop); err != nil {
				return err
			}
			prog.Add(st.Size)
			return nil
		})
}

// copyFileInto copies the open file in to the new file dest, then gives
// dest the permission bits and the modification time of st.
func copyFileInto(in *os.File, st *syscall.Stat_t, dest string, buf []byte, stop *atomic.Bool) error {
	perm := os.FileMode(st.Mode & 0o777)
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, perm)
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(struct{ io.Writer }{out}, stopReader{in, stop}, buf); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	mtime := time.Unix(st.Mtim.Sec, st.Mtim.Nsec)
	return os.Chtimes(dest, mtime, mtime)
}

// stopReader reads from r until stop becomes true.
type stopReader struct {
	r    io.Reader
	stop *atomic.Bool
}

func (s stopReader) Read(p []byte) (int, error) {
	if s.stop.Load() {
		return 0, errInterrupted
	}
	return s.r.Read(p)
}
