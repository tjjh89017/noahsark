package image

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

// CheckTools runs mkudffs and parses its reported udftools version from
// its startup banner. It refuses a version below
// MinUDFToolsMajor.MinUDFToolsMinor.
func CheckTools() (string, error) {
	// mkudffs prints its banner and a usage message, then exits nonzero,
	// when it is run with no device argument. That is the only reliable
	// way to read its version: it has no --version option.
	out, runErr := exec.Command("mkudffs").CombinedOutput()
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
		return m[0], fmt.Errorf("udftools %s is older than the required %d.%d", m[0], MinUDFToolsMajor, MinUDFToolsMinor)
	}
	return m[0], nil
}

// ciEnvVar, when set to a nonempty value, tells the package's own
// real-mkudffs tests that root is available, so they need not skip
// themselves. MakeImage no longer reads it: populating the image is
// not optional.
const ciEnvVar = "NOAHSARK_CI"

// ErrPopulateNeedsRoot is returned by MakeImage when populating the
// UDF image needs root for the loop mount, and the calling process is
// not root. noahsark never elevates itself through sudo; the caller
// must be run as root instead.
var ErrPopulateNeedsRoot = errors.New("populating the UDF image needs root for the loop mount")

// MakeImage builds a UDF 2.01 image of length sectors at imagePath and
// populates it with the tree at dir. mkudffs makes only an empty
// filesystem; populating it needs a loop mount, which needs root.
// MakeImage always runs that populate step: it never leaves an image
// that mkudffs built but nothing filled in. When the calling process
// is not root, MakeImage removes the partial image and returns
// ErrPopulateNeedsRoot rather than shelling out to sudo itself. prog
// reports bytes copied during populate; a nil prog reports nothing.
//
// docs/decisions.md, "Image build", records why this is the chosen path
// over a from-scratch Go UDF writer.
func MakeImage(dir, imagePath string, sectors uint64, prog *progress.Reporter) error {
	if _, err := CheckTools(); err != nil {
		return err
	}
	if sectors == 0 {
		return fmt.Errorf("sectors must not be zero")
	}
	// The root check and the tree-path check both run before mkudffs
	// touches the image file, so a build that cannot finish never first
	// pays for laying out a full sparse file and formatting it.
	if geteuid() != 0 {
		return ErrPopulateNeedsRoot
	}
	treeRoot := filepath.Join(dir, "NOAHSARK")
	if info, err := os.Stat(treeRoot); err != nil {
		return fmt.Errorf("tree directory %s: %w", treeRoot, err)
	} else if !info.IsDir() {
		return fmt.Errorf("tree directory %s: not a directory", treeRoot)
	}

	size := sectors * SectorSize
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(imagePath)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(size)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	cmd := exec.Command("mkudffs",
		"--utf8",
		"--media-type=hd",
		"--blocksize=2048",
		"--udfrev=2.01",
		"--uid=0",
		"--gid=0",
		"--mode=0555",
		"--bootarea=erase",
		imagePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkudffs: %w: %s", err, out)
	}

	if err := populateImage(dir, imagePath, sectors, prog); err != nil {
		_ = os.Remove(imagePath)
		return err
	}
	return nil
}

// geteuid reports the calling process's effective user id. It is a
// variable so a test can pretend to be root without needing real root,
// to exercise the mount step past the root check.
var geteuid = os.Geteuid

// newMountCmd and newUmountCmd build the loop-mount and unmount
// commands for the populate step. They are variables so a test can
// substitute a command that fails deterministically, without a real
// loop mount.
var (
	newMountCmd = func(imagePath, mnt string) *exec.Cmd {
		return exec.Command("mount", "-o", "loop", "-t", "udf", imagePath, mnt)
	}
	newUmountCmd = func(mnt string) *exec.Cmd {
		return exec.Command("umount", mnt)
	}
)

// populateImage mounts imagePath with a loop-mounted UDF driver, copies
// dir's NOAHSARK tree onto it file by file, and unmounts. It needs root
// for the loop mount; MakeImage refuses to run it otherwise rather than
// call sudo itself. prog reports bytes copied as each regular file
// finishes copying; the total is the tree's own regular-file byte sum,
// computed before the copy starts. sectors is the image's own capacity,
// named in the error a full volume reports.
func populateImage(dir, imagePath string, sectors uint64, prog *progress.Reporter) error {
	mnt, err := os.MkdirTemp("", "noahsark-udf-mount-*")
	if err != nil {
		return fmt.Errorf("populate: %w", err)
	}
	defer func() { _ = os.Remove(mnt) }()

	if out, err := newMountCmd(imagePath, mnt).CombinedOutput(); err != nil {
		return fmt.Errorf("populate: mount: %w: %s", err, out)
	}
	defer func() {
		_ = newUmountCmd(mnt).Run()
	}()

	src := filepath.Join(dir, "NOAHSARK")
	dest := filepath.Join(mnt, "NOAHSARK")
	prog.Start("image build: populate", regularFileBytesUnder(src))
	if err := copyTree(src, dest, prog); err != nil {
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

// copyTree copies src onto dest, directory by directory, file by file
// and symlink by symlink, the way populate.sh used to with `cp -p` and
// `cp -P`: a directory's mode is preserved, a regular file's mode and
// modification time are preserved, and a symlink is recreated verbatim.
// filepath.WalkDir visits a directory before its contents, so a single
// pass already gives every file and symlink an existing parent.
// prog.Add reports each regular file's size as it finishes, the same
// per-file granularity populate.sh's "COPIED <bytes>" line gave.
func copyTree(src, dest string, prog *progress.Reporter) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dest, rel)
		if rel == "." {
			destPath = dest
		}

		switch {
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(destPath, info.Mode().Perm())
		case d.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, destPath)
		default:
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := copyFile(path, destPath, info); err != nil {
				return err
			}
			prog.Add(info.Size())
			return nil
		}
	})
}

// copyFile copies one regular file from src to dest, then applies src's
// permission bits and modification time, matching `cp -p`.
func copyFile(src, dest string, info os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dest, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(dest, info.ModTime(), info.ModTime())
}

// regularFileBytesUnder sums the size of every regular file under root.
// A missing or unreadable root sums to zero rather than failing the
// caller: it feeds a progress total, not a correctness check.
func regularFileBytesUnder(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
