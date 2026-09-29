package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// mountEnv returns an env whose mount table is text and whose working
// directory is wd.
func mountEnv(wd, text string) *env {
	return &env{
		getwd:     func() (string, error) { return wd, nil },
		mountinfo: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(text)), nil },
	}
}

// escapeMountPoint writes a path as the kernel writes it in mountinfo.
func escapeMountPoint(p string) string {
	return strings.NewReplacer(`\`, `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`).Replace(p)
}

// mountLine returns one mountinfo line for the mount point p.
func mountLine(id int, p, opts, superOpts string) string {
	return strings.Join([]string{
		strconv.Itoa(id), "1", "7:0", "/", escapeMountPoint(p), opts, "shared:1", "-",
		"udf", "/dev/loop0", superOpts,
	}, " ") + "\n"
}

// mountDirs makes a clean temporary root with a repository directory and
// a disc directory, and returns their resolved paths.
func mountDirs(t *testing.T) (base, repo, disc string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(base, "repo")
	disc = filepath.Join(base, "disc")
	for _, d := range []string{repo, disc} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return base, repo, disc
}

func TestMountCounted(t *testing.T) {
	base, repo, disc := mountDirs(t)
	table := mountLine(1, "/", "rw", "rw") + mountLine(2, disc, "ro,relatime", "ro")
	got, err := countedMount(mountEnv(base, table), disc, repo, filepath.Join(repo, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if got != mountCounted {
		t.Fatalf("got %v, want %v", got, mountCounted)
	}
}

func TestMountReadWriteNotCounted(t *testing.T) {
	base, repo, disc := mountDirs(t)
	table := mountLine(2, disc, "rw,relatime", "rw")
	got, err := countedMount(mountEnv(base, table), disc, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountReadWrite {
		t.Fatalf("got %v, want %v", got, mountReadWrite)
	}
}

func TestMountSuperBlockReadOnlyCounts(t *testing.T) {
	base, repo, disc := mountDirs(t)
	table := mountLine(2, disc, "rw,relatime", "ro,utf8")
	got, err := countedMount(mountEnv(base, table), disc, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountCounted {
		t.Fatalf("got %v, want %v", got, mountCounted)
	}
}

func TestMountDirectoryUnderMountPointNotCounted(t *testing.T) {
	base, repo, disc := mountDirs(t)
	sub := filepath.Join(disc, "NOAHSARK")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	table := mountLine(2, disc, "ro", "ro")
	got, err := countedMount(mountEnv(base, table), sub, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountNotMountPoint {
		t.Fatalf("got %v, want %v", got, mountNotMountPoint)
	}
}

func TestMountInsideRepoNotCounted(t *testing.T) {
	base, repo, _ := mountDirs(t)
	inside := filepath.Join(repo, "mnt")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	table := mountLine(2, inside, "ro", "ro")
	got, err := countedMount(mountEnv(base, table), inside, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountInsideRepo {
		t.Fatalf("got %v, want %v", got, mountInsideRepo)
	}
}

func TestMountInsideStagingNotCounted(t *testing.T) {
	base, repo, _ := mountDirs(t)
	staging := filepath.Join(base, "staging")
	tree := filepath.Join(staging, "plans", "x", "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	table := mountLine(2, tree, "ro", "ro")
	got, err := countedMount(mountEnv(base, table), tree, repo, staging)
	if err != nil {
		t.Fatal(err)
	}
	if got != mountInsideStaging {
		t.Fatalf("got %v, want %v", got, mountInsideStaging)
	}
}

func TestMountSymlinkToMountPointCounts(t *testing.T) {
	base, repo, disc := mountDirs(t)
	link := filepath.Join(base, "link")
	if err := os.Symlink(disc, link); err != nil {
		t.Fatal(err)
	}
	table := mountLine(2, disc, "ro", "ro")
	// A relative path resolves against the working directory of the env.
	got, err := countedMount(mountEnv(base, table), "link", repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountCounted {
		t.Fatalf("got %v, want %v", got, mountCounted)
	}
}

func TestMountSymlinkIntoRepoNotCounted(t *testing.T) {
	base, repo, _ := mountDirs(t)
	inside := filepath.Join(repo, "mnt")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(inside, link); err != nil {
		t.Fatal(err)
	}
	table := mountLine(2, inside, "ro", "ro")
	got, err := countedMount(mountEnv(base, table), link, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountInsideRepo {
		t.Fatalf("got %v, want %v", got, mountInsideRepo)
	}
}

func TestMountPointWithSpace(t *testing.T) {
	base, repo, _ := mountDirs(t)
	disc := filepath.Join(base, "my disc\\a")
	if err := os.Mkdir(disc, 0o755); err != nil {
		t.Fatal(err)
	}
	table := mountLine(2, disc, "ro", "ro")
	if !strings.Contains(table, `my\040disc\134a`) {
		t.Fatalf("table does not escape the mount point: %q", table)
	}
	got, err := countedMount(mountEnv(base, table), disc, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != mountCounted {
		t.Fatalf("got %v, want %v", got, mountCounted)
	}
}

func TestMountLastEntryDecides(t *testing.T) {
	base, repo, disc := mountDirs(t)
	cases := []struct {
		name  string
		table string
		want  mountVerdict
	}{
		{"ro then rw", mountLine(2, disc, "ro", "ro") + mountLine(3, disc, "rw", "rw"), mountReadWrite},
		{"rw then ro", mountLine(2, disc, "rw", "rw") + mountLine(3, disc, "ro", "ro"), mountCounted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := countedMount(mountEnv(base, c.table), disc, repo, "")
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMountOptionalFields(t *testing.T) {
	base, repo, disc := mountDirs(t)
	cases := []struct {
		name string
		line string
	}{
		{"none", "2 1 7:0 / " + disc + " ro - udf /dev/loop0 ro\n"},
		{"several", "2 1 7:0 / " + disc + " ro shared:4 master:2 propagate_from:1 - udf /dev/loop0 ro\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := countedMount(mountEnv(base, c.line), disc, repo, "")
			if err != nil {
				t.Fatal(err)
			}
			if got != mountCounted {
				t.Fatalf("got %v, want %v", got, mountCounted)
			}
		})
	}
}

func TestMountTableUnreadable(t *testing.T) {
	base, repo, disc := mountDirs(t)
	e := mountEnv(base, "")
	e.mountinfo = func() (io.ReadCloser, error) { return nil, os.ErrPermission }
	if _, err := countedMount(e, disc, repo, ""); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("got %v, want a permission error", err)
	}
}

func TestMountTableMalformed(t *testing.T) {
	base, repo, disc := mountDirs(t)
	for _, table := range []string{
		"2 1 7:0 / " + disc + "\n",
		"2 1 7:0 / " + disc + " ro shared:1 udf /dev/loop0 ro\n",
		"2 1 7:0 / " + disc + " ro - udf\n",
		`2 1 7:0 / /mnt\04 ro - udf /dev/loop0 ro` + "\n",
		`2 1 7:0 / /mnt\09x ro - udf /dev/loop0 ro` + "\n",
	} {
		if _, err := countedMount(mountEnv(base, table), disc, repo, ""); err == nil {
			t.Errorf("table %q: got no error", table)
		}
	}
}

func TestMountMissingRootIsError(t *testing.T) {
	base, repo, _ := mountDirs(t)
	if _, err := countedMount(mountEnv(base, ""), filepath.Join(base, "absent"), repo, ""); err == nil {
		t.Fatal("got no error for a disc root that does not exist")
	}
}
