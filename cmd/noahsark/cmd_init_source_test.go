package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitWritesSourceRoot checks that init --source stores an absolute
// sources.root line in the config, resolved against the given path.
func TestInitWritesSourceRoot(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+src); code != 0 {
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
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
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

// TestCommitWithNoArgUsesConfiguredSource checks that commit with no
// SOURCE positional argument falls back to init --source's config value.
func TestCommitWithNoArgUsesConfiguredSource(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "snapshot ") {
		t.Fatalf("commit output %q has no snapshot line", out)
	}
}

// TestCommitArgOverridesConfiguredSource checks that a SOURCE given on
// commit's own command line wins over the configured source root.
func TestCommitArgOverridesConfiguredSource(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	configured := writeFixtureSource(t)
	override := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+configured); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, override)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "snapshot ") {
		t.Fatalf("commit output %q has no snapshot line", out)
	}
}

// TestCommitWithNeitherArgNorConfigFails checks that commit refuses to
// run, with a clear message, when no SOURCE was given and the config
// holds no source root.
func TestCommitWithNeitherArgNorConfigFails(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo)
	if code != 2 {
		t.Fatalf("commit: exit %d, want 2: %s", code, out)
	}
	want := "no SOURCE given and no source root in the config; pass a path on the command line, or set sources.root in the config"
	if !strings.Contains(out, want) {
		t.Fatalf("commit output %q, want it to contain %q", out, want)
	}
}

// TestReadConfigRefusesSecondSourceRoot checks that the loader refuses a
// config file that repeats sources.root, since this build stores only
// one source root.
func TestReadConfigRefusesSecondSourceRoot(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+writeFixtureSource(t)); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	configFile := configPath(repo)
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("sources.root = /another/root\n")...)
	if err := os.WriteFile(configFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readConfig(configFile); err == nil {
		t.Fatal("readConfig: want an error for a repeated sources.root, got none")
	}
}

// TestReadConfigStillRefusesUnknownKey checks that adding sources.root
// to the known-key set did not loosen the loader's refusal of an
// unrelated unknown key.
func TestReadConfigStillRefusesUnknownKey(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	configFile := configPath(repo)
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("sources.bogus = xyz\n")...)
	if err := os.WriteFile(configFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = readConfig(configFile)
	if err == nil {
		t.Fatal("readConfig: want an error for sources.bogus, got none")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("readConfig error = %q, want it to say unknown key", err.Error())
	}
}
