// Command ci-tree-compare is CI-only tooling, not a NoahsArk command
// surface. It compares a restored tree with its source tree, entry by
// entry, with no-follow reads, so that any byte in a name is safe.
//
// Usage: ci-tree-compare SRC DEST
//
// For a regular file it compares the type, the content (SHA-256), the
// mode bits with the setuid, setgid and sticky bits, and the
// modification time. For a directory it compares the type, the mode bits
// and the modification time. For a symlink it compares the target. When
// the process runs as root, it also compares the uid and the gid, as a
// restore as root applies them. A FIFO, a socket or a device node of SRC
// must not exist in DEST: restore does not create one. DEST must hold no
// entry that SRC does not hold, and no part file. The roots themselves
// are not compared. It prints one line for each difference, and exits 1
// when there is one.
package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const permBits = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

func main() {
	if len(os.Args) != 3 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-tree-compare SRC DEST")
		os.Exit(2)
	}
	src, dest := os.Args[1], os.Args[2]
	c := &comparer{src: src, dest: dest, owner: os.Geteuid() == 0}
	if err := filepath.WalkDir(src, c.source); err != nil {
		c.differ("walk %s: %v", src, err)
	}
	if err := filepath.WalkDir(dest, c.extra); err != nil {
		c.differ("walk %s: %v", dest, err)
	}
	fmt.Printf("compared %d files, %d directories, %d symlinks; %d special files not restored; %d difference(s)\n",
		c.files, c.dirs, c.links, c.special, c.diffs)
	if c.diffs > 0 {
		os.Exit(1)
	}
}

type comparer struct {
	src, dest                          string
	owner                              bool
	files, dirs, links, special, diffs int
}

func (c *comparer) differ(format string, args ...any) {
	c.diffs++
	if c.diffs <= 50 {
		fmt.Printf("differ: "+format+"\n", args...)
	}
}

// source compares one entry of SRC with the entry of the same path in
// DEST.
func (c *comparer) source(path string, _ fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(c.src, path)
	if err != nil || rel == "." {
		return err
	}
	want, err := os.Lstat(path)
	if err != nil {
		return err
	}
	destPath := filepath.Join(c.dest, rel)
	got, err := os.Lstat(destPath)
	if want.Mode()&(fs.ModeNamedPipe|fs.ModeSocket|fs.ModeDevice|fs.ModeCharDevice) != 0 {
		c.special++
		if !errors.Is(err, fs.ErrNotExist) {
			c.differ("%q: a special file of the source exists in the restored tree", rel)
		}
		return nil
	}
	if err != nil {
		c.differ("%q: %v", rel, err)
		if want.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if want.Mode().Type() != got.Mode().Type() {
		c.differ("%q: type %v, want %v", rel, got.Mode().Type(), want.Mode().Type())
		if want.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if c.owner {
		ws, gs := want.Sys().(*syscall.Stat_t), got.Sys().(*syscall.Stat_t)
		if ws.Uid != gs.Uid || ws.Gid != gs.Gid {
			c.differ("%q: owner %d:%d, want %d:%d", rel, gs.Uid, gs.Gid, ws.Uid, ws.Gid)
		}
	}
	switch {
	case want.Mode()&fs.ModeSymlink != 0:
		c.links++
		wt, err1 := os.Readlink(path)
		gt, err2 := os.Readlink(destPath)
		if err1 != nil || err2 != nil || wt != gt {
			c.differ("%q: symlink target %q, want %q", rel, gt, wt)
		}
		return nil
	case want.IsDir():
		c.dirs++
	default:
		c.files++
		if want.Size() != got.Size() {
			c.differ("%q: size %d, want %d", rel, got.Size(), want.Size())
		} else if ws, gs := sum(path), sum(destPath); ws != gs {
			c.differ("%q: content %s, want %s", rel, gs, ws)
		}
	}
	if want.Mode()&permBits != got.Mode()&permBits {
		c.differ("%q: mode %v, want %v", rel, got.Mode()&permBits, want.Mode()&permBits)
	}
	if !want.ModTime().Equal(got.ModTime()) {
		c.differ("%q: mtime %v, want %v", rel, got.ModTime().UTC(), want.ModTime().UTC())
	}
	return nil
}

// extra reports each entry of DEST that SRC does not hold.
func (c *comparer) extra(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(c.dest, path)
	if err != nil || rel == "." {
		return err
	}
	if strings.HasSuffix(d.Name(), ".noahsark-part") {
		c.differ("%q: a part file stays", rel)
	}
	if _, err := os.Lstat(filepath.Join(c.src, rel)); err != nil {
		c.differ("%q: the source does not hold it", rel)
		if d.IsDir() {
			return filepath.SkipDir
		}
	}
	return nil
}

// sum returns the SHA-256 of the file at path, or the read error.
func sum(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
