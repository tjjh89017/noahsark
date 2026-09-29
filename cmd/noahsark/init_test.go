package main

import (
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
// sources.root line in the config, resolved against the given path.
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
// with no --source, writes no sources.root line at all.
func TestInitWithNoSourceLeavesConfigWithoutKey(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	data, err := os.ReadFile(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sources.root") {
		t.Fatalf("config = %q, want no sources.root line", data)
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
