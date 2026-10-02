package image

import (
	"bytes"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCheckToolsSkipsWithoutMkudffs checks that CheckTools reports an
// error, rather than panicking, when mkudffs is not installed.
func TestCheckToolsSkipsWithoutMkudffs(t *testing.T) {
	if _, err := findTool("mkudffs"); err == nil {
		t.Skip("mkudffs is installed; nothing to test here")
	}
	if _, err := CheckTools(); err == nil {
		t.Fatal("expected an error when mkudffs is not installed")
	}
}

// TestFindToolIgnoresPath checks that image build never takes mkudffs
// from PATH: a mkudffs in a directory of PATH that is not a system
// directory is not found.
func TestFindToolIgnoresPath(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "noahsark-test-tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if path, err := findTool("noahsark-test-tool"); err == nil {
		t.Fatalf("findTool found %s through PATH", path)
	}
}

// buildImageFixture packs a fixture tree and returns its output
// directory and the target capacity to build an image at.
func buildImageFixture(t *testing.T) (outDir string, sectors uint64) {
	t.Helper()
	stagingDir, snapID := stageFixture(t)
	outDir = t.TempDir()
	opts := testOpts(t, stagingDir, snapID, outDir)
	if _, err := Build(opts); err != nil {
		t.Fatal(err)
	}
	return outDir, opts.TargetCapacitySectors
}

// planFixture packs a fixture tree into a pack --out directory and
// returns a plan directory whose disc root "tree" is a symlink to it.
func planFixture(t *testing.T) (planDir, outDir string, sectors uint64) {
	t.Helper()
	outDir, sectors = buildImageFixture(t)
	planDir = t.TempDir()
	if err := os.Symlink(outDir, filepath.Join(planDir, "tree")); err != nil {
		t.Fatal(err)
	}
	return planDir, outDir, sectors
}

// fakeUDFHost records the host calls of BuildImage.
type fakeUDFHost struct {
	// populated receives the NOAHSARK tree of the mount at the unmount.
	populated string
	// mkudffsImage is the fstat of the file that mkudffs got, and label
	// is the volume label that mkudffs got.
	mkudffsImage syscall.Stat_t
	label        string
	// chowned is the fstat of the file that fchown got.
	chowned    syscall.Stat_t
	chownCalls int
	mounted    string
	unmounted  int
	detached   int
	// beforeMkudffs and afterMount run in those steps when set.
	beforeMkudffs func()
	afterMount    func()
	unmountErr    error
}

// installFakeUDFHost replaces the root check, mkudffs, the loop device,
// the mount and fchown with fakes, so that BuildImage runs with no
// root. The fake unmount moves the copied tree out of the mount point,
// as a real unmount makes it go away.
func installFakeUDFHost(t *testing.T) *fakeUDFHost {
	t.Helper()
	h := &fakeUDFHost{populated: filepath.Join(t.TempDir(), "populated")}
	oldHost, oldEuid, oldFchown := udfHost, geteuid, fchown
	t.Cleanup(func() { udfHost, geteuid, fchown = oldHost, oldEuid, oldFchown })
	geteuid = func() int { return 0 }
	udfHost.mkudffs = func(img *os.File, label string) error {
		h.label = label
		if h.beforeMkudffs != nil {
			h.beforeMkudffs()
		}
		return syscall.Fstat(int(img.Fd()), &h.mkudffsImage)
	}
	udfHost.attachLoop = func(*os.File, string) (string, func() error, error) {
		return "/dev/loop-fake", func() error { h.detached++; return nil }, nil
	}
	udfHost.mount = func(_, mnt string) error {
		h.mounted = mnt
		if h.afterMount != nil {
			h.afterMount()
		}
		return nil
	}
	udfHost.unmount = func(mnt string) error {
		h.unmounted++
		if h.unmountErr != nil {
			return h.unmountErr
		}
		if err := os.Rename(filepath.Join(mnt, discRootDirName), h.populated); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	fchown = func(f *os.File, uid, gid int) error {
		h.chownCalls++
		if err := syscall.Fstat(int(f.Fd()), &h.chowned); err != nil {
			return err
		}
		return f.Chown(uid, gid)
	}
	return h
}

// buildPlan runs BuildImage on the plan directory planDir.
func buildPlan(t *testing.T, planDir string, sectors uint64) error {
	t.Helper()
	p, err := OpenPlan(planDir, "tree", "tree.img")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	return BuildImage(p, sectors, VolumeLabel(7), nil)
}

// statOf returns the lstat of path.
func statOf(t *testing.T, path string) syscall.Stat_t {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// treeFiles returns the relative path and the content of each regular
// file below root.
func treeFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[rel] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// assertNoMountPointLeft checks that the fake mount point is gone.
func assertNoMountPointLeft(t *testing.T, h *fakeUDFHost) {
	t.Helper()
	if h.mounted == "" {
		return
	}
	if _, err := os.Lstat(h.mounted); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the mount point %s stays: %v", h.mounted, err)
	}
}

// TestMakeImageNeedsRoot checks that MakeImage refuses to populate the
// image and leaves no image when the calling process is not root,
// without shelling out to sudo itself.
func TestMakeImageNeedsRoot(t *testing.T) {
	planDir, _, sectors := planFixture(t)
	oldEuid := geteuid
	geteuid = func() int { return 1000 }
	defer func() { geteuid = oldEuid }()

	imagePath := filepath.Join(planDir, "tree.img")
	err := MakeImage(filepath.Join(planDir, "tree"), imagePath, sectors, VolumeLabel(0), nil)
	if !errors.Is(err, ErrPopulateNeedsRoot) {
		t.Fatalf("MakeImage error = %v, want ErrPopulateNeedsRoot", err)
	}
	if _, statErr := os.Lstat(imagePath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatal("MakeImage left an image behind after refusing to populate")
	}
}

// TestBuildImageCopiesTheTreeThroughDescriptors checks the whole build
// with fake host calls: mkudffs and fchown get the image file that
// BuildImage created, the mount gets the whole NOAHSARK tree, and the
// image ends with mode 0644, the length of the capacity, no mount point
// and a detached loop device.
func TestBuildImageCopiesTheTreeThroughDescriptors(t *testing.T) {
	h := installFakeUDFHost(t)
	planDir, outDir, sectors := planFixture(t)

	if err := buildPlan(t, planDir, sectors); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(planDir, "tree.img")
	img := statOf(t, imagePath)
	if img.Mode&0o7777 != 0o644 || img.Mode&syscall.S_IFMT != syscall.S_IFREG {
		t.Errorf("image mode %o, want a regular file with mode 0644", img.Mode)
	}
	if uint64(img.Size) != sectors*SectorSize {
		t.Errorf("image size %d, want %d", img.Size, sectors*SectorSize)
	}
	if h.label != "NOAHSARK_0007" {
		t.Errorf("mkudffs got the label %q, want NOAHSARK_0007", h.label)
	}
	if h.mkudffsImage.Ino != img.Ino || h.chowned.Ino != img.Ino || h.chownCalls != 1 {
		t.Errorf("mkudffs got inode %d and fchown got inode %d (%d calls), want the image inode %d",
			h.mkudffsImage.Ino, h.chowned.Ino, h.chownCalls, img.Ino)
	}
	want := treeFiles(t, filepath.Join(outDir, discRootDirName))
	got := treeFiles(t, h.populated)
	if len(want) == 0 || len(got) != len(want) {
		t.Fatalf("populated %d files, want %d", len(got), len(want))
	}
	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Errorf("populated %s differs from the disc root", name)
		}
	}
	if h.unmounted != 1 || h.detached != 1 {
		t.Errorf("unmount %d times and detach %d times, want 1 and 1", h.unmounted, h.detached)
	}
	assertNoMountPointLeft(t, h)
}

// TestBuildImageRefusesASymlinkAtTheImageName checks that a symlink at
// the name of the image makes the build fail, and that the file it
// names keeps its content and its mode.
func TestBuildImageRefusesASymlinkAtTheImageName(t *testing.T) {
	h := installFakeUDFHost(t)
	planDir, _, sectors := planFixture(t)
	victim := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(planDir, "tree.img")); err != nil {
		t.Fatal(err)
	}

	err := buildPlan(t, planDir, sectors)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("BuildImage over a symlink: %v, want an error that the name exists", err)
	}
	assertVictimUnchanged(t, victim)
	if h.chownCalls != 0 {
		t.Errorf("fchown ran %d times", h.chownCalls)
	}
}

// assertVictimUnchanged checks that victim still holds "secret" with
// mode 0600.
func assertVictimUnchanged(t *testing.T, victim string) {
	t.Helper()
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	st := statOf(t, victim)
	if string(data) != "secret" || st.Mode&0o7777 != 0o600 {
		t.Errorf("the file behind the symlink changed: content %q, mode %o", data, st.Mode&0o7777)
	}
}

// TestBuildImageIgnoresASymlinkPutAtTheImageName checks the swap of the
// image for a symlink while the build runs: the user renames the image
// and puts a symlink to another file at its name. The owner and the mode
// go to the image file, never to the file that the symlink names.
func TestBuildImageIgnoresASymlinkPutAtTheImageName(t *testing.T) {
	h := installFakeUDFHost(t)
	planDir, _, sectors := planFixture(t)
	victim := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(planDir, "tree.img")
	moved := filepath.Join(planDir, "moved.img")
	h.afterMount = func() {
		if err := os.Rename(imagePath, moved); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(victim, imagePath); err != nil {
			t.Error(err)
		}
	}

	if err := buildPlan(t, planDir, sectors); err != nil {
		t.Fatal(err)
	}
	assertVictimUnchanged(t, victim)
	img := statOf(t, moved)
	if h.chowned.Ino != img.Ino || img.Mode&0o7777 != 0o644 {
		t.Errorf("fchown got inode %d, image inode %d with mode %o", h.chowned.Ino, img.Ino, img.Mode&0o7777)
	}
	if st := statOf(t, imagePath); st.Mode&syscall.S_IFMT != syscall.S_IFLNK {
		t.Errorf("the symlink at the image name changed")
	}
}

// TestBuildImageRefusesASymlinkInTheTree checks that a symlink in the
// disc root stops the build: the file it names does not go into the
// image, the image is removed, and the mount is undone.
func TestBuildImageRefusesASymlinkInTheTree(t *testing.T) {
	h := installFakeUDFHost(t)
	planDir, outDir, sectors := planFixture(t)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("secret content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(outDir, discRootDirName, "zz-link")); err != nil {
		t.Fatal(err)
	}

	err := buildPlan(t, planDir, sectors)
	if err == nil || !strings.Contains(err.Error(), "zz-link is a symbolic link") {
		t.Fatalf("BuildImage with a symlink in the tree: %v", err)
	}
	assertBuildUndone(t, h, planDir)
	for name, data := range treeFiles(t, h.populated) {
		if bytes.Contains(data, []byte("secret content")) {
			t.Errorf("the image holds the secret as %s", name)
		}
	}
}

// assertBuildUndone checks that a failed build left no image, no mount
// point and no loop device.
func assertBuildUndone(t *testing.T, h *fakeUDFHost, planDir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(planDir, "tree.img")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the image stays after a failed build: %v", err)
	}
	if h.unmounted != 1 || h.detached != 1 {
		t.Errorf("unmount %d times and detach %d times, want 1 and 1", h.unmounted, h.detached)
	}
	assertNoMountPointLeft(t, h)
}

// TestBuildImageRefusesAFIFOInTheTree checks that a FIFO in the disc
// root stops the build at once. A read of a FIFO blocks until a writer
// comes.
func TestBuildImageRefusesAFIFOInTheTree(t *testing.T) {
	h := installFakeUDFHost(t)
	planDir, outDir, sectors := planFixture(t)
	if err := syscall.Mkfifo(filepath.Join(outDir, discRootDirName, "zz-fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- buildPlan(t, planDir, sectors) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "zz-fifo is a FIFO") {
			t.Fatalf("BuildImage with a FIFO in the tree: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("BuildImage blocks on a FIFO in the tree")
	}
	assertBuildUndone(t, h, planDir)
}

// TestBuildImageReportsAFailedUnmount checks that a failed unmount is an
// error that names the mount point, the loop device and the commands
// that remove them, and that the image is removed.
func TestBuildImageReportsAFailedUnmount(t *testing.T) {
	h := installFakeUDFHost(t)
	h.unmountErr = syscall.EBUSY
	planDir, _, sectors := planFixture(t)

	err := buildPlan(t, planDir, sectors)
	if _, ok := errors.AsType[*MountLeftError](err); !ok {
		t.Fatalf("BuildImage with a failed unmount: %v, want a MountLeftError", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(h.mounted) })
	for _, want := range []string{h.mounted, "/dev/loop-fake", "sudo umount " + h.mounted, "sudo rmdir " + h.mounted} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(planDir, "tree.img")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the image stays after a failed unmount: %v", err)
	}
	if h.chownCalls != 0 {
		t.Errorf("fchown ran after a failed unmount")
	}
}

// TestBuildImageRemovesTheImageOnMountFailure checks that a failing
// mount leaves no image, no mount point and no loop device.
func TestBuildImageRemovesTheImageOnMountFailure(t *testing.T) {
	h := installFakeUDFHost(t)
	var mnt string
	udfHost.mount = func(_, m string) error {
		mnt = m
		return syscall.EINVAL
	}
	planDir, _, sectors := planFixture(t)

	if err := buildPlan(t, planDir, sectors); err == nil {
		t.Fatal("expected an error when the mount fails")
	}
	if _, err := os.Lstat(filepath.Join(planDir, "tree.img")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the image stays after a mount failure: %v", err)
	}
	if _, err := os.Lstat(mnt); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the mount point stays after a mount failure: %v", err)
	}
	if h.detached != 1 {
		t.Errorf("detach %d times, want 1", h.detached)
	}
}

// TestBuildImageStopsOnASignal checks that a SIGINT during the populate
// stops the build, unmounts, and removes the image.
func TestBuildImageStopsOnASignal(t *testing.T) {
	h := installFakeUDFHost(t)
	planDir, _, sectors := planFixture(t)
	h.afterMount = func() {
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
		// The watch of BuildImage takes the signal in its own goroutine.
		time.Sleep(200 * time.Millisecond)
	}

	err := buildPlan(t, planDir, sectors)
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("BuildImage after a SIGINT: %v, want errInterrupted", err)
	}
	assertBuildUndone(t, h, planDir)
}

// TestOpenPlanRefusesASymlinkedPlanDirectory checks that the plan
// directory itself must not be a symlink.
func TestOpenPlanRefusesASymlinkedPlanDirectory(t *testing.T) {
	planDir, _, _ := planFixture(t)
	link := filepath.Join(t.TempDir(), "plan")
	if err := os.Symlink(planDir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPlan(link, "tree", "tree.img"); err == nil || !strings.Contains(err.Error(), "is a symbolic link") {
		t.Fatalf("OpenPlan of a symlink: %v", err)
	}
}

// TestOpenPlanReportsAMissingDiscRoot checks that a missing disc root
// wraps fs.ErrNotExist, which image build prints as "no disc root".
func TestOpenPlanReportsAMissingDiscRoot(t *testing.T) {
	if _, err := OpenPlan(t.TempDir(), "tree", "tree.img"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenPlan with no disc root: %v, want fs.ErrNotExist", err)
	}
}

// TestDirRefusesAFileOfAnotherOwner checks the owner rule of Dir: a
// file that does not belong to the owner of its directory, such as a
// hard link to a file of another user, is refused, and the error holds
// no content of the file. A test cannot give a file to another user,
// thus it changes the owner that the Dir holds.
func TestDirRefusesAFileOfAnotherOwner(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDirNoFollow(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	d.UID++
	_, err = d.ReadFile("f")
	if err == nil || !strings.Contains(err.Error(), "belongs to uid") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("ReadFile of a file of another owner: %v", err)
	}
}

// TestDirReadFileRefusesSpecialEntries checks that ReadFile refuses a
// symlink, a FIFO and a directory, and never blocks.
func TestDirReadFileRefusesSpecialEntries(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDirNoFollow(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for name, want := range map[string]string{
		"link": "is a symbolic link",
		"fifo": "is a FIFO",
		"sub":  "is a directory",
	} {
		_, err := d.ReadFile(name)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("ReadFile(%s): %v, want %q", name, err, want)
		}
	}
	names, err := d.names()
	if err != nil || !slices.Equal(names, []string{"fifo", "link", "sub"}) {
		t.Errorf("names %v, %v", names, err)
	}
}

// TestCheckRootOnlyPath checks the rule for the parent of the mount
// point of root: /tmp, a sticky directory of root, passes, and a
// directory of another user does not.
func TestCheckRootOnlyPath(t *testing.T) {
	if fi, err := os.Lstat("/tmp"); err == nil && fi.Mode()&os.ModeSticky != 0 {
		if err := checkRootOnlyPath("/tmp"); err != nil {
			t.Errorf("/tmp: %v", err)
		}
	}
	if os.Geteuid() == 0 {
		t.Skip("a directory of root passes the check")
	}
	if err := checkRootOnlyPath(t.TempDir()); err == nil {
		t.Error("a directory of the test user passes the check for root")
	}
}

// TestMkudffsArgs checks the arguments of mkudffs: the label follows
// --udfrev, and the image path comes last.
func TestMkudffsArgs(t *testing.T) {
	got := mkudffsArgs("/dev/fd/3", "NOAHSARK_0012")
	want := []string{"--utf8", "--media-type=hd", "--blocksize=2048", "--udfrev=2.01",
		"--label=NOAHSARK_0012", "--uid=0", "--gid=0", "--mode=0555", "--bootarea=erase", "/dev/fd/3"}
	if !slices.Equal(got, want) {
		t.Fatalf("mkudffs arguments %q, want %q", got, want)
	}
}

// TestVolumeLabel checks the label rule: NOAHSARK_, then the disc
// number zero-padded to 4 digits, with more digits when the number is
// larger. The largest number gives 29 characters, within the UDF limit
// of 30.
func TestVolumeLabel(t *testing.T) {
	for seq, want := range map[uint64]string{
		0:              "NOAHSARK_0000",
		7:              "NOAHSARK_0007",
		12345:          "NOAHSARK_12345",
		math.MaxUint64: "NOAHSARK_18446744073709551615",
	} {
		if got := VolumeLabel(seq); got != want {
			t.Errorf("VolumeLabel(%d) = %q, want %q", seq, got, want)
		}
	}
	if n := len(VolumeLabel(math.MaxUint64)); n > 30 {
		t.Errorf("the longest label has %d characters, want at most 30", n)
	}
}
