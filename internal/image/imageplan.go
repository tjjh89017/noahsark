package image

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tjjh89017/noahsark/internal/format"
)

// Plan is the open plan directory of one disc: the directory that holds
// the disc root and the image file. Plan holds a descriptor of the plan
// directory and of the NOAHSARK directory of the disc root. image build
// creates, checks and removes the image only through these descriptors.
type Plan struct {
	dir       *Dir
	noahsark  *Dir
	imageName string
	// TreePath and ImagePath name the disc root and the image file in
	// messages.
	TreePath, ImagePath string
}

// OpenPlan opens the plan directory planDir and the NOAHSARK directory
// of its disc root treeName. The plan directory must not be a symlink.
// The disc root can be a symlink that pack --out made; OpenPlan follows
// it one time and holds the directory it names. The disc root and its
// NOAHSARK directory must belong to the owner of the plan directory. A
// missing plan directory, disc root or NOAHSARK directory gives an
// error that wraps os.ErrNotExist.
func OpenPlan(planDir, treeName, imageName string) (*Plan, error) {
	dir, err := OpenDirNoFollow(planDir)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		dir: dir, imageName: imageName,
		TreePath:  filepath.Join(planDir, treeName),
		ImagePath: filepath.Join(planDir, imageName),
	}
	tree, err := p.openTree(treeName)
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	p.noahsark, err = tree.Subdir(discRootDirName)
	_ = tree.Close()
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	return p, nil
}

// discRootDirName is the one directory at the top of a disc root.
const discRootDirName = "NOAHSARK"

// openTree opens the disc root name of the plan directory. It follows a
// symlink there one time.
func (p *Plan) openTree(name string) (*Dir, error) {
	fd, err := syscall.Openat(p.dir.fd(), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p.TreePath, Err: err}
	}
	tree, err := newDir(fd, p.TreePath)
	if err != nil {
		return nil, err
	}
	if err := p.dir.checkOwner(p.TreePath, tree.UID); err != nil {
		_ = tree.Close()
		return nil, err
	}
	return tree, nil
}

// Close closes the descriptors of p.
func (p *Plan) Close() error {
	return errors.Join(p.noahsark.Close(), p.dir.Close())
}

// Owner returns the uid and the gid of the plan directory.
func (p *Plan) Owner() (uid, gid uint32) {
	return p.dir.UID, p.dir.GID
}

// ImageExists reports whether the plan directory has an entry with the
// name of the image, a symlink included.
func (p *Plan) ImageExists() (bool, error) {
	return p.dir.Exists(p.imageName)
}

// RemoveImage removes the entry with the name of the image. It removes
// a symlink, never the file that the symlink names.
func (p *Plan) RemoveImage() error {
	return p.dir.Remove(p.imageName)
}

// ReadDisc reads and decodes NOAHSARK/DISC.bin of the disc root.
func (p *Plan) ReadDisc() (format.Disc, error) {
	buf, err := p.noahsark.ReadFile("DISC.bin")
	if err != nil {
		return format.Disc{}, err
	}
	var disc format.Disc
	if err := disc.Decode(buf); err != nil {
		return format.Disc{}, fmt.Errorf("%s: %w", filepath.Join(p.noahsark.Path, "DISC.bin"), err)
	}
	return disc, nil
}

// CreateImage creates the image file with mode 0600. The open never
// follows a symlink and never opens a file that exists, so a symlink or
// a file that the user put there makes it fail. The caller does each
// later step through the returned descriptor.
func (p *Plan) CreateImage() (*os.File, error) {
	fd, err := syscall.Openat(p.dir.fd(), p.imageName,
		syscall.O_RDWR|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: p.ImagePath, Err: err}
	}
	return os.NewFile(uintptr(fd), p.ImagePath), nil
}

// fchown gives the open file f the owner uid and gid. It is a variable
// so that a test can record the call, because a test cannot give a file
// to another user.
var fchown = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }

// FinishImage writes the image file img to the medium, then gives it
// the owner of the plan directory and mode 0644. Each step goes through
// the descriptor, thus a symlink that now has the name of the image
// changes nothing.
func (p *Plan) FinishImage(img *os.File) error {
	if err := img.Sync(); err != nil {
		return err
	}
	uid, gid := p.Owner()
	if err := fchown(img, int(uid), int(gid)); err != nil {
		return err
	}
	return img.Chmod(0o644)
}

// DiscardImage removes the name of the image when that name still is
// the image file img. A name that the user put there stays.
func (p *Plan) DiscardImage(img *os.File) {
	var want, got syscall.Stat_t
	if err := syscall.Fstat(int(img.Fd()), &want); err != nil {
		return
	}
	fd, err := syscall.Openat(p.dir.fd(), p.imageName, openFlags, 0)
	if err != nil {
		return
	}
	err = syscall.Fstat(fd, &got)
	_ = syscall.Close(fd)
	if err == nil && got.Dev == want.Dev && got.Ino == want.Ino {
		_ = p.dir.Remove(p.imageName)
	}
}
