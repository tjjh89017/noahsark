package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// hostLine fills a line of /proc/self/mountinfo of a real host: ROOT is
// the root field and MNT is the mount point p, each escaped as the kernel
// escapes it.
func hostLine(format, root, p string) string {
	return strings.NewReplacer("ROOT", escapeMountPoint(root), "MNT", escapeMountPoint(p)).Replace(format) + "\n"
}

// Lines of /proc/self/mountinfo of a Debian host. hostRootLine is the
// root filesystem, where the repository is.
const (
	hostRootLine = "31 2 8:2 / / rw,relatime shared:1 - ext4 /dev/sda2 rw,errors=remount-ro"
	hostBindLine = "1234 31 8:2 ROOT MNT ro,relatime shared:1 - ext4 /dev/sda2 rw,errors=remount-ro"
	hostOverlay  = "1300 31 0:50 / MNT ro,relatime shared:200 - overlay overlay ro,lowerdir=/srv/lower,upperdir=/srv/upper,workdir=/srv/work"
	hostTmpfs    = "45 31 0:36 / MNT ro,nosuid,nodev shared:20 - tmpfs tmpfs ro,size=4194304k,inode64"
	hostNFS      = "1500 31 0:60 / MNT ro,relatime shared:400 - nfs4 server:/export ro,vers=4.2,rsize=1048576"
	hostUDF      = "1400 31 7:0 / MNT ro,relatime shared:300 - udf /dev/loop0 ro,uid=1000,gid=1000,umask=022,iocharset=utf8"
	hostISO      = "1401 31 7:1 / MNT ro,relatime shared:301 - iso9660 /dev/loop1 ro,nojoliet,check=s,map=n,blocksize=2048,iocharset=utf8"
	hostSrvLine  = "60 31 8:3 / MNT rw,relatime shared:60 - ext4 /dev/sda3 rw"
)

func TestMountHostLines(t *testing.T) {
	base, repo, disc := mountDirs(t)
	tree := filepath.Join(repo, "staging", "plans", "u", "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	root := hostLine(hostRootLine, "/", "/")
	cases := []struct {
		name  string
		table string
		want  mountVerdict
	}{
		{"bind mount of the packed tree", root + hostLine(hostBindLine, tree, disc), mountBindSubdir},
		{"read-only bind mount of the root filesystem", root + hostLine(hostBindLine, "/", disc), mountRepoDevice},
		{"overlay", root + hostLine(hostOverlay, "/", disc), mountNotDiscFS},
		{"tmpfs", root + hostLine(hostTmpfs, "/", disc), mountNotDiscFS},
		{"nfs", root + hostLine(hostNFS, "/", disc), mountNotDiscFS},
		{"loop mount of a UDF image", root + hostLine(hostUDF, "/", disc), mountCounted},
		{"loop mount of an ISO 9660 image", root + hostLine(hostISO, "/", disc), mountCounted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := countedMount(mountEnv(base, c.table), disc, repo, filepath.Join(repo, "staging"))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestMountLoopImageInsideRepoCounts loop-mounts the image that image
// build writes into the staging store at a mount point outside the
// repository. The mount shows the loop device, thus it counts.
func TestMountLoopImageInsideRepoCounts(t *testing.T) {
	base, repo, disc := mountDirs(t)
	plan := filepath.Join(repo, "staging", "plans", "u")
	if err := os.MkdirAll(filepath.Join(plan, "tree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plan, "tree.img"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	table := hostLine(hostRootLine, "/", "/") + hostLine(hostUDF, "/", disc)
	got, err := countedMount(mountEnv(base, table), disc, repo, filepath.Join(repo, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if got != mountCounted {
		t.Fatalf("got %v, want %v", got, mountCounted)
	}
}

func TestMountHostLineWithSpace(t *testing.T) {
	base, repo, _ := mountDirs(t)
	disc := filepath.Join(base, "my disc")
	if err := os.Mkdir(disc, 0o755); err != nil {
		t.Fatal(err)
	}
	line := hostLine(hostUDF, "/", disc)
	if !strings.Contains(line, `my\040disc`) {
		t.Fatalf("line does not escape the mount point: %q", line)
	}
	got, err := countedMount(mountEnv(base, hostLine(hostRootLine, "/", "/")+line), disc, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountCounted {
		t.Fatalf("got %v, want %v", got, mountCounted)
	}
}

// TestMountBelowMountPoint mounts an image at outer, and a second image
// at inner, a directory of the first image. Each mount point counts. A
// directory of the outer image is not a mount point.
func TestMountBelowMountPoint(t *testing.T) {
	base, repo, outer := mountDirs(t)
	inner := filepath.Join(outer, "inner")
	plain := filepath.Join(outer, "NOAHSARK")
	for _, d := range []string{inner, plain} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	table := hostLine(hostRootLine, "/", "/") + hostLine(hostUDF, "/", outer) + hostLine(hostISO, "/", inner)
	for dir, want := range map[string]mountVerdict{outer: mountCounted, inner: mountCounted, plain: mountNotMountPoint} {
		got, err := countedMount(mountEnv(base, table), dir, repo, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: got %v, want %v", dir, got, want)
		}
	}
}

// TestMountHiddenByParentMount mounts an image at disc, then mounts the
// root filesystem again at the parent of disc. The mount table still
// lists disc, but the path disc now shows the later mount.
func TestMountHiddenByParentMount(t *testing.T) {
	base, repo, disc := mountDirs(t)
	table := hostLine(hostRootLine, "/", "/") + hostLine(hostUDF, "/", disc) +
		hostLine("1600 31 8:2 / MNT ro,relatime shared:1 - ext4 /dev/sda2 ro", "/", base)
	e := mountEnv(base, table)
	e.deviceOf = func(string) (devNum, error) { return devNum{major: 8, minor: 2}, nil }
	got, err := countedMount(e, disc, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountHidden {
		t.Fatalf("got %v, want %v", got, mountHidden)
	}
}

// TestMountPackOutTreeNotCounted mounts the pack --out directory of a
// disc read-only: the staging store holds a symlink to it.
func TestMountPackOutTreeNotCounted(t *testing.T) {
	base, repo, disc := mountDirs(t)
	staging := filepath.Join(repo, "staging")
	plan := filepath.Join(staging, "plans", "u")
	if err := os.MkdirAll(plan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(disc, filepath.Join(plan, "tree")); err != nil {
		t.Fatal(err)
	}
	table := hostLine(hostRootLine, "/", "/") + hostLine(hostUDF, "/", disc)
	got, err := countedMount(mountEnv(base, table), disc, repo, staging)
	if err != nil {
		t.Fatal(err)
	}
	if got != mountPackedTree {
		t.Fatalf("got %v, want %v", got, mountPackedTree)
	}
}

// TestMountOnStagingDevice mounts the root of the filesystem of a
// staging store outside the repository read-only a second time.
func TestMountOnStagingDevice(t *testing.T) {
	base, repo, disc := mountDirs(t)
	srv := filepath.Join(base, "srv")
	staging := filepath.Join(srv, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	table := hostLine(hostRootLine, "/", "/") + hostLine(hostSrvLine, "/", srv) +
		hostLine("1700 31 8:3 / MNT ro,relatime shared:60 - ext4 /dev/sda3 rw", "/", disc)
	got, err := countedMount(mountEnv(base, table), disc, repo, staging)
	if err != nil {
		t.Fatal(err)
	}
	if got != mountStagingDevice {
		t.Fatalf("got %v, want %v", got, mountStagingDevice)
	}
}

// TestMountRepoNotCreatedYet checks a repository directory that does not
// exist, as for recover: its device is the device of its nearest parent.
func TestMountRepoNotCreatedYet(t *testing.T) {
	base, _, disc := mountDirs(t)
	repo := filepath.Join(base, "new", "repo")
	table := hostLine(hostRootLine, "/", "/") + hostLine(hostBindLine, "/", disc)
	got, err := countedMount(mountEnv(base, table), disc, repo, filepath.Join(repo, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if got != mountRepoDevice {
		t.Fatalf("got %v, want %v", got, mountRepoDevice)
	}
}

func TestParseMountLineFields(t *testing.T) {
	got, err := parseMountLine(`1234 31 259:3 /home/a\040b /mnt/x ro,relatime shared:1 - ext4 /dev/nvme0n1p3 rw`)
	if err != nil {
		t.Fatal(err)
	}
	want := mountEntry{device: devNum{major: 259, minor: 3}, root: "/home/a b", mountPoint: "/mnt/x", readOnly: true, fsType: "ext4"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for _, bad := range []string{
		"2 1 7 / /mnt ro - udf /dev/loop0 ro",
		"2 1 x:0 / /mnt ro - udf /dev/loop0 ro",
		"2 1 7:y / /mnt ro - udf /dev/loop0 ro",
	} {
		if _, err := parseMountLine(bad); err == nil {
			t.Errorf("line %q: got no error", bad)
		}
	}
}

// TestStatDeviceMatchesMountTable compares the st_dev of /proc with the
// major:minor field of its entry in the mount table of this host.
func TestStatDeviceMatchesMountTable(t *testing.T) {
	e := realEnv()
	dev, err := statDevice("/proc")
	if err != nil {
		t.Skipf("no /proc: %v", err)
	}
	entry, found, err := findMount(e, "/proc")
	if err != nil || !found {
		t.Skipf("no mount table entry of /proc: %v", err)
	}
	if dev != entry.device {
		t.Fatalf("st_dev of /proc %v, mount table %v", dev, entry.device)
	}
}

// TestVerifyNotDiscMountRecordsNothing verifies a copy of a good disc
// root under mounts that are not a disc. Each check passes, prints the
// reason to standard error, and records nothing.
func TestVerifyNotDiscMountRecordsNothing(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	cases := []struct {
		name   string
		mount  fakeMount
		reason mountVerdict
	}{
		{"overlay", fakeMount{readOnly: true, fsType: "overlay"}, mountNotDiscFS},
		{"tmpfs", fakeMount{readOnly: true, fsType: "tmpfs"}, mountNotDiscFS},
		{"bind mount of a directory", fakeMount{readOnly: true, root: "/staging/plans/u/tree"}, mountBindSubdir},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(fx.work, "mount-"+strconv.Itoa(i))
			if out, err := exec.Command("cp", "-a", fx.root, dir).CombinedOutput(); err != nil {
				t.Fatalf("cp -a: %v: %s", err, out)
			}
			addFakeMountEntry(t, dir, c.mount)
			logBefore := discLogBytes(t, fx.repo)
			te := newTestEnv(t.TempDir())
			code, _ := te.run("--repo="+fx.repo, "verify", dir)
			want := notCountedDisc + " (" + c.reason.String() + ")"
			if code != 0 || !strings.HasSuffix(te.out.String(), "\n"+want+"\n") {
				t.Fatalf("exit %d, stdout %q, want 0 and %q", code, te.out.String(), want)
			}
			if strings.Contains(te.errOut.String(), "not counted") {
				t.Errorf("stderr %q repeats the not counted line", te.errOut.String())
			}
			if string(discLogBytes(t, fx.repo)) != string(logBefore) {
				t.Error("verify wrote the disc state log")
			}
		})
	}
}

func TestDiscChangedRefusal(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	ident, err := readDiscIdentity(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	rr, err := image.Read(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	if got := discChangedRefusal(fx.root, ident, rr); got != "" {
		t.Fatalf("same disc: got %q, want no refusal", got)
	}
	if got := discChangedRefusal(fx.root, ident, nil); got != "" {
		t.Fatalf("same disc after a failed check: got %q, want no refusal", got)
	}
	other := ident
	other.DiscUUID[0] ^= 0xff
	if got := discChangedRefusal(fx.root, other, rr); !strings.Contains(got, "the disc changed during the check") {
		t.Fatalf("other disc: got %q, want the changed-disc refusal", got)
	}
	if got := discChangedRefusal(fx.root, other, nil); !strings.Contains(got, "the disc changed during the check") {
		t.Fatalf("other disc after a failed check: got %q, want the changed-disc refusal", got)
	}
}
