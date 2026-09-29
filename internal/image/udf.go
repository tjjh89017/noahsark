package image

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/tjjh89017/noahsark/internal/progress"
)

// MinUDFToolsMajor and MinUDFToolsMinor are the pinned minimum udftools
// version. A build refuses an older or unpatched mkudffs.
const (
	MinUDFToolsMajor = 2
	MinUDFToolsMinor = 3
)

// udfToolsVersionRe matches the "mkudffs from udftools X.Y" banner
// mkudffs prints to stderr on every invocation, including an invalid one.
// mkudffs has no --version option, so CheckTools reads this banner
// instead.
var udfToolsVersionRe = regexp.MustCompile(`udftools (\d+)\.(\d+)`)

// toolDirs are the directories where image build looks for mkudffs, in
// this order. image build runs as root, thus it never searches PATH.
var toolDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin", "/usr/local/sbin", "/usr/local/bin"}

// findTool returns the path of the executable file name in the first
// directory of toolDirs that has it.
func findTool(name string) (string, error) {
	for _, dir := range toolDirs {
		path := filepath.Join(dir, name)
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: not found in %s", name, strings.Join(toolDirs, ", "))
}

// toolCommand returns the command that runs the tool name with args. The
// tool gets a fixed environment: PATH is toolDirs, and the locale is C.
func toolCommand(name string, args ...string) (*exec.Cmd, error) {
	path, err := findTool(name)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, args...)
	cmd.Env = []string{"PATH=" + strings.Join(toolDirs, ":"), "LC_ALL=C"}
	return cmd, nil
}

// CheckTools runs mkudffs and parses its reported udftools version from
// its startup banner. It refuses a version below
// MinUDFToolsMajor.MinUDFToolsMinor.
func CheckTools() (string, error) {
	cmd, err := toolCommand("mkudffs")
	if err != nil {
		return "", err
	}
	// mkudffs prints its banner and a usage message, then exits nonzero,
	// when it is run with no device argument. That is the only reliable
	// way to read its version: it has no --version option.
	out, runErr := cmd.CombinedOutput()
	m := udfToolsVersionRe.FindStringSubmatch(string(out))
	if m == nil {
		if runErr != nil && len(out) == 0 {
			return "", fmt.Errorf("mkudffs: %w", runErr)
		}
		return "", fmt.Errorf("could not parse mkudffs version from: %s", strings.TrimSpace(string(out)))
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < MinUDFToolsMajor || (major == MinUDFToolsMajor && minor < MinUDFToolsMinor) {
		return m[0], fmt.Errorf("mkudffs is from %s; image build needs udftools %d.%d or newer", m[0], MinUDFToolsMajor, MinUDFToolsMinor)
	}
	return m[0], nil
}

// ciEnvVar, when set to a nonempty value, tells the package's own
// real-mkudffs tests that root is available, so they need not skip
// themselves. MakeImage does not read it: populating the image is not
// optional.
const ciEnvVar = "NOAHSARK_CI"

// ErrPopulateNeedsRoot is returned by MakeImage and BuildImage when
// populating the UDF image needs root for the loop mount, and the
// calling process is not root. noahsark never elevates itself through
// sudo; the caller must be run as root instead.
var ErrPopulateNeedsRoot = errors.New("populating the UDF image needs root for the loop mount")

// MakeImage builds the image at imagePath from the disc root at
// treeDir. Both must be in the same directory, as in a plan directory.
// It opens them with OpenPlan and runs BuildImage.
func MakeImage(treeDir, imagePath string, sectors uint64, prog *progress.Reporter) error {
	if filepath.Dir(treeDir) != filepath.Dir(imagePath) {
		return fmt.Errorf("the disc root %s and the image %s are not in the same directory", treeDir, imagePath)
	}
	p, err := OpenPlan(filepath.Dir(imagePath), filepath.Base(treeDir), filepath.Base(imagePath))
	if err != nil {
		return err
	}
	defer func() { _ = p.Close() }()
	return BuildImage(p, sectors, prog)
}

// BuildImage builds a UDF 2.01 image of length sectors in the plan
// directory of p and populates it with the NOAHSARK tree of the disc
// root of p. mkudffs makes only an empty filesystem; populating it needs
// a loop mount, which needs root. BuildImage refuses with
// ErrPopulateNeedsRoot when the calling process is not root. The caller
// checks the mkudffs version with CheckTools first.
//
// image build runs as root in a directory of a user with no privilege.
// BuildImage thus creates the image file one time, where no symlink and
// no file is, and does each later step through its descriptor: mkudffs
// gets the descriptor, the loop device gets the descriptor, and the
// owner and the mode go through the descriptor. The mount point is a new
// directory that only root can change. BuildImage removes the image when
// a step fails. The finished image gets the owner of the plan directory,
// and mode 0644. prog reports bytes copied during populate; a nil prog
// reports nothing. A SIGINT, SIGTERM or SIGHUP stops the populate, and
// BuildImage then unmounts and removes the image.
//
// docs/decisions.md, "Image build", records why this is the chosen path
// over a from-scratch Go UDF writer.
func BuildImage(p *Plan, sectors uint64, prog *progress.Reporter) (err error) {
	if sectors == 0 {
		return fmt.Errorf("sectors must not be zero")
	}
	// The root check comes before the image file exists, so a build that
	// cannot finish never first pays for laying out a full sparse file
	// and formatting it.
	if geteuid() != 0 {
		return ErrPopulateNeedsRoot
	}
	parent, err := privateTempParent()
	if err != nil {
		return err
	}

	img, err := p.CreateImage()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			p.DiscardImage(img)
		}
		_ = img.Close()
	}()

	var stop atomic.Bool
	defer watchSignals(&stop)()

	if err := img.Truncate(int64(sectors * SectorSize)); err != nil {
		return err
	}
	if err := udfHost.mkudffs(img); err != nil {
		return err
	}
	if err := populate(p, img, parent, sectors, prog, &stop); err != nil {
		return err
	}
	if stop.Load() {
		return errInterrupted
	}
	return p.FinishImage(img)
}

// watchSignals sets stop on SIGINT, SIGTERM or SIGHUP, so that the
// populate stops and unmounts. The returned function ends the watch.
func watchSignals(stop *atomic.Bool) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			stop.Store(true)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// geteuid reports the calling process's effective user id. It is a
// variable so a test can pretend to be root without needing real root,
// to exercise the mount step past the root check.
var geteuid = os.Geteuid

// udfHost holds the host calls of BuildImage that need root or
// mkudffs. A test replaces them: it runs with no root, no mkudffs and
// no loop device.
var udfHost = struct {
	// mkudffs formats the open image file.
	mkudffs func(img *os.File) error
	// attachLoop attaches the open image file to a loop device.
	attachLoop func(img *os.File, name string) (dev string, detach func() error, err error)
	// mount mounts the UDF volume of the loop device dev on mnt.
	mount func(dev, mnt string) error
	// unmount unmounts mnt.
	unmount func(mnt string) error
}{
	mkudffs:    runMkudffs,
	attachLoop: attachLoop,
	mount: func(dev, mnt string) error {
		return syscall.Mount(dev, mnt, "udf", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "")
	},
	unmount: func(mnt string) error { return syscall.Unmount(mnt, 0) },
}

// runMkudffs formats the open image file img. mkudffs opens the image
// through /dev/fd/3, a copy of the descriptor of img, and never through
// the path in the plan directory.
func runMkudffs(img *os.File) error {
	cmd, err := toolCommand("mkudffs",
		"--utf8",
		"--media-type=hd",
		"--blocksize=2048",
		"--udfrev=2.01",
		"--uid=0",
		"--gid=0",
		"--mode=0555",
		"--bootarea=erase",
		"/dev/fd/3",
	)
	if err != nil {
		return err
	}
	cmd.ExtraFiles = []*os.File{img}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkudffs: %w: %s", err, out)
	}
	return nil
}

// privateTempParent returns the directory that holds the mount point:
// the system temporary directory. For root it first checks that no user
// but root can rename or replace an element of its path. A process of
// another user gains nothing from a change of the path, thus it skips
// the check.
func privateTempParent() (string, error) {
	dir, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", err
	}
	if os.Geteuid() != 0 {
		return dir, nil
	}
	return dir, checkRootOnlyPath(dir)
}

// checkRootOnlyPath checks each element of the path dir: it must be a
// directory of root, and a directory that others can write must have
// the sticky bit.
func checkRootOnlyPath(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || !fi.IsDir() || st.Uid != 0 || (st.Mode&0o022 != 0 && st.Mode&syscall.S_ISVTX == 0) {
			return fmt.Errorf("the temporary directory %s is not safe for the mount point, because a user other than root can change %s; set TMPDIR to a directory that only root can change", dir, p)
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}

// PrivateTempDir creates a new directory with mode 0700 in the system
// temporary directory, after the check of that directory that the mount
// point also gets. pattern is as for os.MkdirTemp.
func PrivateTempDir(pattern string) (string, error) {
	parent, err := privateTempParent()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, pattern)
}

// MountLeftError reports a populate that could not unmount the image.
// The mount point and the loop device stay. The kernel detaches the loop
// device after the unmount, because it has the autoclear flag.
type MountLeftError struct {
	Mount, Device string
	// Err is the error of the unmount.
	Err error
	// Cause is the error of the copy before the unmount, or nil.
	Cause error
}

func (e *MountLeftError) Error() string {
	msg := fmt.Sprintf("populate: unmount %s: %v; the image stays mounted at %s on the loop device %s, and image build removes the image file; to clean up, run: sudo umount %s && sudo rmdir %s",
		e.Mount, e.Err, e.Mount, e.Device, e.Mount, e.Mount)
	if e.Cause != nil {
		msg = e.Cause.Error() + "; " + msg
	}
	return msg
}

func (e *MountLeftError) Unwrap() []error { return []error{e.Err, e.Cause} }

// populate loop-mounts the image file img on a new mount point below
// parent, copies the NOAHSARK tree of p onto it, unmounts it and
// detaches the loop device. Each exit path unmounts and detaches; an
// unmount that fails gives a MountLeftError. sectors is the image's own
// capacity, named in the error a full volume reports.
func populate(p *Plan, img *os.File, parent string, sectors uint64, prog *progress.Reporter, stop *atomic.Bool) error {
	mnt, err := os.MkdirTemp(parent, "noahsark-udf-mount-*")
	if err != nil {
		return fmt.Errorf("populate: %w", err)
	}
	dev, detach, err := udfHost.attachLoop(img, p.ImagePath)
	if err != nil {
		_ = os.Remove(mnt)
		return fmt.Errorf("populate: loop device: %w", err)
	}
	if err := udfHost.mount(dev, mnt); err != nil {
		detachErr := detach()
		_ = os.Remove(mnt)
		return errors.Join(fmt.Errorf("populate: mount %s on %s: %w", dev, mnt, err), detachErr)
	}

	copyErr := copyIntoImage(p, mnt, sectors, prog, stop)
	if err := udfHost.unmount(mnt); err != nil {
		_ = detach()
		return &MountLeftError{Mount: mnt, Device: dev, Err: err, Cause: copyErr}
	}
	detachErr := detach()
	rmErr := os.Remove(mnt)
	switch {
	case copyErr != nil:
		return copyErr
	case detachErr != nil:
		return fmt.Errorf("populate: %w; to clean up, run: sudo losetup -d %s", detachErr, dev)
	case rmErr != nil:
		return fmt.Errorf("populate: remove the mount point: %w", rmErr)
	}
	return nil
}

// copyIntoImage copies the NOAHSARK tree of p into the image mounted at
// mnt.
func copyIntoImage(p *Plan, mnt string, sectors uint64, prog *progress.Reporter, stop *atomic.Bool) error {
	dest := filepath.Join(mnt, discRootDirName)
	if err := os.Mkdir(dest, p.noahsark.Perm); err != nil {
		return fmt.Errorf("populate: %w", err)
	}
	if err := os.Chmod(dest, p.noahsark.Perm); err != nil {
		return fmt.Errorf("populate: %w", err)
	}
	prog.Start("image build: populate", treeBytes(p.noahsark))
	if err := copyTreeInto(p.noahsark, dest, prog, stop); err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			// The bare error names the temp mount point, a path with
			// no meaning to the user; report the volume's own capacity
			// and the fix instead.
			return fmt.Errorf("populate: the tree is larger than the image capacity of %d sectors (%d bytes), which DISC.bin holds; pack again with a larger capacity", sectors, sectors*SectorSize)
		}
		return fmt.Errorf("populate: %w", err)
	}
	prog.Done()
	return nil
}
