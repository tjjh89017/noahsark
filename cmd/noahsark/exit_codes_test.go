package main

import (
	"path/filepath"
	"testing"
)

// TestUsageErrorsExitTwo is a table-driven check of the exit code
// registry's usage-error convention: a bad flag, a bad argument, or an
// unknown command all exit 2, across every command, never 0 or 1. A
// repository that already exists is shared by the cases that need one,
// so a usage error is caught before any of them would otherwise start
// doing real work.
func TestUsageErrorsExitTwo(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"unknown top-level command", []string{"bogus"}},
		{"commit: unknown flag", []string{"commit", "--no-such-flag", "/nowhere"}},
		{"init: repository already exists", []string{"init", "--repo=" + repo}},
		{"pack: missing --capacity", []string{"pack", "--repo=" + repo}},
		{"pack: --fec and --no-fec together", []string{"pack", "--repo=" + repo, "--capacity=64MiB", "--fec", "--no-fec"}},
		{"gc: unexpected positional argument", []string{"gc", "--repo=" + repo, "extra"}},
		{"ls: missing SNAPSHOT", []string{"ls", "--repo=" + repo}},
		{"log: unknown flag", []string{"log", "--repo=" + repo, "--no-such-flag"}},
		{"verify: missing DISC-ROOT", []string{"verify", "--repo=" + repo}},
		{"restore: unknown flag", []string{"restore", "--repo=" + repo, "--no-such-flag"}},
		{"restore: --dry-run without --mount", []string{"restore", "--repo=" + repo, "--dry-run", "LATEST", filepath.Join(work, "out")}},
		{"disc: unknown subcommand", []string{"disc", "bogus"}},
		{"disc burned: missing DISC", []string{"disc", "burned", "--repo=" + repo}},
		{"recover: unknown flag", []string{"recover", "--repo=" + repo, "--no-such-flag"}},
		{"image build: missing --out", []string{"image", "build", work}},
		{"status: unexpected positional argument", []string{"status", "--repo=" + repo, "extra"}},
		{"pack: capacity without a unit", []string{"pack", "--repo=" + repo, "--capacity=7500000"}},
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
