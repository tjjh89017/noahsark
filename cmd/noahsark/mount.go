package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// mountVerdict tells whether a disc root is a counted mount, and if not,
// why not.
type mountVerdict int

const (
	mountCounted mountVerdict = iota
	mountNotMountPoint
	mountReadWrite
	mountInsideRepo
	mountInsideStaging
	mountPackedTree
	mountHidden
	mountBindSubdir
	mountNotDiscFS
	mountRepoDevice
	mountStagingDevice
)

func (v mountVerdict) String() string {
	switch v {
	case mountCounted:
		return "counted mount"
	case mountNotMountPoint:
		return "not a mount point"
	case mountReadWrite:
		return "not a read-only mount"
	case mountInsideRepo:
		return "inside the repository"
	case mountInsideStaging:
		return "inside the staging store"
	case mountPackedTree:
		return "the packed tree of a disc"
	case mountHidden:
		return "hidden by another mount"
	case mountBindSubdir:
		return "a bind mount of a directory"
	case mountNotDiscFS:
		return "not a disc filesystem"
	case mountRepoDevice:
		return "on the device of the repository"
	case mountStagingDevice:
		return "on the device of the staging store"
	}
	return fmt.Sprintf("mountVerdict(%d)", int(v))
}

// devNum is a device number: the major:minor field of a mount table, and
// the st_dev of a file.
type devNum struct {
	major, minor uint32
}

func (d devNum) String() string { return fmt.Sprintf("%d:%d", d.major, d.minor) }

// statDevice returns the device of the file at path. It decodes st_dev as
// the Linux kernel encodes a dev_t.
func statDevice(path string) (devNum, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return devNum{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return devNum{}, fmt.Errorf("%s: no device number", path)
	}
	dev := uint64(st.Dev)
	return devNum{
		major: uint32((dev>>8)&0xfff | (dev>>32)&^0xfff),
		minor: uint32(dev&0xff | (dev>>12)&^0xff),
	}, nil
}

// mountEntry is one line of a mount table in the format of
// /proc/self/mountinfo.
type mountEntry struct {
	device     devNum
	root       string
	mountPoint string
	readOnly   bool
	fsType     string
}

// notDiscFS holds the filesystem types that never hold a disc or the
// image of a disc: union and overlay filesystems, memory filesystems,
// network filesystems, and kernel pseudo filesystems.
var notDiscFS = map[string]bool{
	"overlay": true, "aufs": true, "unionfs": true,
	"fuse.fuse-overlayfs": true, "fuse.mergerfs": true, "fuse.unionfs": true, "fuse.unionfs-fuse": true, "fuse.bindfs": true,
	"tmpfs": true, "ramfs": true, "devtmpfs": true,
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "smbfs": true, "9p": true, "virtiofs": true,
	"afs": true, "ceph": true, "glusterfs": true, "davfs": true,
	"fuse.sshfs": true, "fuse.rclone": true, "fuse.s3fs": true, "fuse.gvfsd-fuse": true, "fuse.glusterfs": true, "fuse.ceph-fuse": true, "fuse.davfs2": true,
	"proc": true, "sysfs": true, "devpts": true, "cgroup": true, "cgroup2": true, "securityfs": true,
	"debugfs": true, "tracefs": true, "configfs": true, "bpf": true, "pstore": true, "efivarfs": true,
	"hugetlbfs": true, "mqueue": true, "autofs": true, "binfmt_misc": true, "fusectl": true,
	"rpc_pipefs": true, "nsfs": true, "fuse.portal": true,
}

// countedMount tells whether root is a counted mount. A counted mount is
// a path that, after symlink resolution, meets each of these rules:
//   - it is outside repoDir and outside stagingDir;
//   - it is not the packed tree of a disc under stagingDir, also through
//     the symlink of a pack --out disc, and also through a bind mount;
//   - it is itself a mount point, and the mount in effect there: its
//     device is the device of the mount table entry;
//   - the mount shows the root of its filesystem, not a directory of it;
//   - the filesystem type is not one of notDiscFS;
//   - it is mounted read-only;
//   - its device is not the device of repoDir or of stagingDir.
//
// The source of the mount is not checked, thus a loop mount of an image
// counts, also of an image file inside the repository. An empty
// stagingDir skips the staging checks. A relative path is relative to the
// working directory of e.
func countedMount(e *env, root, repoDir, stagingDir string) (mountVerdict, error) {
	resolved, err := resolvePath(e, root)
	if err != nil {
		return 0, fmt.Errorf("resolve disc root: %w", err)
	}
	repo, err := resolveDir(e, repoDir)
	if err != nil {
		return 0, err
	}
	if isWithin(resolved, repo) {
		return mountInsideRepo, nil
	}
	var staging string
	if stagingDir != "" {
		if staging, err = resolveDir(e, stagingDir); err != nil {
			return 0, err
		}
		if isWithin(resolved, staging) {
			return mountInsideStaging, nil
		}
		if isAnyPackedTree(resolved, staging) {
			return mountPackedTree, nil
		}
	}
	entry, found, err := findMount(e, resolved)
	if err != nil {
		return 0, err
	}
	if !found {
		return mountNotMountPoint, nil
	}
	rootDev, err := e.deviceOf(resolved)
	if err != nil {
		return 0, fmt.Errorf("disc root: %w", err)
	}
	switch {
	case rootDev != entry.device:
		return mountHidden, nil
	case entry.root != "/":
		return mountBindSubdir, nil
	case notDiscFS[entry.fsType]:
		return mountNotDiscFS, nil
	case !entry.readOnly:
		return mountReadWrite, nil
	}
	repoDev, err := existingDevice(e, repo)
	if err != nil {
		return 0, fmt.Errorf("repository: %w", err)
	}
	if rootDev == repoDev {
		return mountRepoDevice, nil
	}
	if staging != "" {
		stagingDev, err := existingDevice(e, staging)
		if err != nil {
			return 0, fmt.Errorf("staging store: %w", err)
		}
		if rootDev == stagingDev {
			return mountStagingDevice, nil
		}
	}
	return mountCounted, nil
}

// isAnyPackedTree tells whether dir is the same directory as the packed
// tree of a disc under staging, or the target of its symlink. A bind
// mount of a directory is the same directory: same device and inode.
func isAnyPackedTree(dir, staging string) bool {
	fi, err := os.Stat(dir)
	if err != nil {
		return false
	}
	trees, err := filepath.Glob(filepath.Join(staging, plansDirName, "*", planTreeName))
	if err != nil {
		return false
	}
	for _, tree := range trees {
		if ti, err := os.Stat(tree); err == nil && os.SameFile(fi, ti) {
			return true
		}
	}
	return false
}

// existingDevice returns the device of dir, or of its nearest parent that
// exists when dir does not exist yet.
func existingDevice(e *env, dir string) (devNum, error) {
	for {
		dev, err := e.deviceOf(dir)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			return dev, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dev, err
		}
		dir = parent
	}
}

// resolvePath makes path absolute and resolves every symlink in it.
func resolvePath(e *env, path string) (string, error) {
	if !filepath.IsAbs(path) {
		wd, err := e.getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(wd, path)
	}
	return filepath.EvalSymlinks(path)
}

// resolveDir resolves dir like resolvePath. A directory that does not
// exist keeps its clean absolute form.
func resolveDir(e *env, dir string) (string, error) {
	resolved, err := resolvePath(e, dir)
	if err == nil {
		return resolved, nil
	}
	if !filepath.IsAbs(dir) {
		wd, werr := e.getwd()
		if werr != nil {
			return "", werr
		}
		dir = filepath.Join(wd, dir)
	}
	return filepath.Clean(dir), nil
}

// isWithin tells whether path is dir or a path under dir. Both paths are
// absolute and clean.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// findMount reads the mount table of e and returns the entry of the mount
// point path. When several entries have the same mount point, the last
// entry is the mount in effect.
func findMount(e *env, path string) (mountEntry, bool, error) {
	r, err := e.mountinfo()
	if err != nil {
		return mountEntry{}, false, fmt.Errorf("read mount table: %w", err)
	}
	defer func() { _ = r.Close() }()

	var match mountEntry
	found := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		entry, err := parseMountLine(text)
		if err != nil {
			return mountEntry{}, false, fmt.Errorf("read mount table: line %d: %w", line, err)
		}
		if entry.mountPoint == path {
			match = entry
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return mountEntry{}, false, fmt.Errorf("read mount table: %w", err)
	}
	return match, found, nil
}

// parseMountLine decodes one line of a mount table in the format of
// /proc/self/mountinfo. The fields are: mount id, parent id, major:minor,
// root, mount point, mount options, zero or more optional fields, the
// separator "-", filesystem type, source, super block options.
func parseMountLine(text string) (mountEntry, error) {
	fields := strings.Fields(text)
	if len(fields) < 6 {
		return mountEntry{}, errors.New("too few fields")
	}
	sep := slices.Index(fields[6:], "-")
	if sep < 0 {
		return mountEntry{}, errors.New("no separator")
	}
	after := fields[6+sep+1:]
	if len(after) < 3 {
		return mountEntry{}, errors.New("too few fields after the separator")
	}
	device, err := parseDevNum(fields[2])
	if err != nil {
		return mountEntry{}, err
	}
	root, err := unescapeMountField(fields[3])
	if err != nil {
		return mountEntry{}, err
	}
	mountPoint, err := unescapeMountField(fields[4])
	if err != nil {
		return mountEntry{}, err
	}
	readOnly := hasOption(fields[5], "ro") || hasOption(after[2], "ro")
	return mountEntry{
		device:     device,
		root:       root,
		mountPoint: mountPoint,
		readOnly:   readOnly,
		fsType:     after[0],
	}, nil
}

// parseDevNum decodes a major:minor field.
func parseDevNum(s string) (devNum, error) {
	majText, minText, ok := strings.Cut(s, ":")
	if !ok {
		return devNum{}, fmt.Errorf("bad device number %q", s)
	}
	major, err := strconv.ParseUint(majText, 10, 32)
	if err != nil {
		return devNum{}, fmt.Errorf("bad device number %q", s)
	}
	minor, err := strconv.ParseUint(minText, 10, 32)
	if err != nil {
		return devNum{}, fmt.Errorf("bad device number %q", s)
	}
	return devNum{major: uint32(major), minor: uint32(minor)}, nil
}

// hasOption tells whether the comma-separated list opts holds name.
func hasOption(opts, name string) bool {
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == name {
			return true
		}
	}
	return false
}

// unescapeMountField decodes the octal escapes of a mount table field.
// The kernel writes a space, a tab, a newline and a backslash as a
// backslash and three octal digits.
func unescapeMountField(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+3 >= len(s) {
			return "", fmt.Errorf("bad escape in %q", s)
		}
		var v byte
		for _, c := range []byte(s[i+1 : i+4]) {
			if c < '0' || c > '7' {
				return "", fmt.Errorf("bad escape in %q", s)
			}
			v = v<<3 | (c - '0')
		}
		b.WriteByte(v)
		i += 3
	}
	return b.String(), nil
}
