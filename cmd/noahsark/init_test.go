package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitRefusesRepo is row 77.
func TestInitRefusesRepo(t *testing.T) {
	dir := t.TempDir()
	code, out := runIn(t, dir, "--repo="+dir, "init")
	if code != 2 {
		t.Fatalf("--repo init: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "init makes the current directory the repository; it does not take --repo") {
		t.Fatalf("--repo init: output %q", out)
	}
	if isRepoDir(dir) {
		t.Fatal("--repo init wrote a repository")
	}
}

// TestInitMakesTheWorkingDirectoryTheRepository checks that init uses
// the working directory, and refuses a directory that already is a
// repository.
func TestInitMakesTheWorkingDirectoryTheRepository(t *testing.T) {
	dir := t.TempDir()
	if code, out := runIn(t, dir, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if !isRepoDir(dir) {
		t.Fatal("init did not make the working directory a repository")
	}
	code, out := runIn(t, dir, "init")
	if code != 2 || !strings.Contains(out, "already a noahsark repository") {
		t.Fatalf("second init: exit %d, output %q", code, out)
	}
}

// TestInitWritesSourceRoot checks that init --source stores an absolute
// sources.root in the config, resolved against the given path.
func TestInitWritesSourceRoot(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourceRoot != src {
		t.Fatalf("SourceRoot = %q, want %q", cfg.SourceRoot, src)
	}
	if !filepath.IsAbs(cfg.SourceRoot) {
		t.Fatalf("SourceRoot = %q, want an absolute path", cfg.SourceRoot)
	}
}

// TestInitWithNoSourceLeavesConfigWithoutKey checks that a plain init,
// with no --source, writes no sources.root key at all.
func TestInitWithNoSourceLeavesConfigWithoutKey(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	data, err := os.ReadFile(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sources:") || strings.Contains(string(data), "root:") {
		t.Fatalf("config = %q, want no sources.root key", data)
	}

	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourceRoot != "" {
		t.Fatalf("SourceRoot = %q, want empty", cfg.SourceRoot)
	}
}

// TestInitHelpTextIsPlain asserts init's usage text describes the
// command in plain words, not the old "SOURCE-less setup" phrasing.
func TestInitHelpTextIsPlain(t *testing.T) {
	code, out := runCmd(t, "init", "-h")
	if code != 0 {
		t.Fatalf("init -h: exit %d, want 0; output: %s", code, out)
	}
	if strings.Contains(out, "SOURCE-less") {
		t.Fatalf("init -h output = %q, want plain wording, not \"SOURCE-less\"", out)
	}
	if !strings.Contains(out, "repository") {
		t.Fatalf("init -h output = %q, want it to describe creating a repository", out)
	}
}

// TestInitUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// init: each case exits 2, never 0 or 1.
func TestInitUsageErrorsExitTwo(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"--repo given", []string{"--repo=" + repo, "init"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runCmd(t, c.args...)
			if code != 2 {
				t.Fatalf("args %v: exit %d, want 2: %s", c.args, code, out)
			}
		})
	}
}

// TestInitWritesConfigLast makes the creation of state/ fail. init must
// then leave no config.yaml: config.yaml makes the directory a
// repository, and a later init must accept the directory again.
func TestInitWritesConfigLast(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runIn(t, dir, "init")
	if code != 1 {
		t.Fatalf("init: exit %d, want 1: %s", code, out)
	}
	if isRepoDir(dir) {
		t.Fatal("init wrote config.yaml before it created state/")
	}
	if err := os.Remove(filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	if code, out := runIn(t, dir, "init"); code != 0 {
		t.Fatalf("init again: exit %d: %s", code, out)
	}
}

// repoFileState gives the path, the mode, the size and the mtime of each
// path below dir, for a check that a command changed no file.
func repoFileState(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out[path] = fmt.Sprintf("%v %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCloneWithNoStateDirectory makes a repository as a git clone of a
// repository with no commit gives it: config.yaml and .gitignore only.
// status and log change no file. commit creates state/ and catalog/ and
// works.
func TestCloneWithNoStateDirectory(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	for _, name := range []string{"state", "catalog", "staging", "lock"} {
		if err := os.RemoveAll(filepath.Join(repo, name)); err != nil {
			t.Fatal(err)
		}
	}
	before := repoFileState(t, repo)
	for _, args := range [][]string{{"status"}, {"log"}} {
		code, out := runCmd(t, append([]string{"--repo=" + repo}, args...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, out)
		}
		if after := repoFileState(t, repo); !maps.Equal(before, after) {
			t.Fatalf("%v changed the repository: before %v, after %v", args, before, after)
		}
	}

	src := writeFixtureSource(t)
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	for _, name := range []string{"state", "catalog"} {
		fi, err := os.Stat(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.IsDir() || fi.Mode().Perm() != 0o755&^currentUmask() {
			t.Fatalf("%s: mode %v, want a directory with mode 0755", name, fi.Mode())
		}
	}
	if code, out := runCmd(t, "--repo="+repo, "status"); code != 0 {
		t.Fatalf("status after commit: exit %d: %s", code, out)
	}
}

// currentUmask returns the umask of the process without a change of it.
func currentUmask() os.FileMode {
	dir, err := os.MkdirTemp("", "umask")
	if err != nil {
		return 0
	}
	defer func() { _ = os.RemoveAll(dir) }()
	probe := filepath.Join(dir, "d")
	if err := os.Mkdir(probe, 0o777); err != nil {
		return 0
	}
	fi, err := os.Stat(probe)
	if err != nil {
		return 0
	}
	return 0o777 &^ fi.Mode().Perm()
}
