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
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"unknown top-level command", []string{"bogus"}},
		{"commit: unknown flag", []string{"commit", "--no-such-flag", "/nowhere"}},
		{"init: --repo given", []string{"--repo=" + repo, "init"}},
		{"pack: missing --capacity", []string{"--repo=" + repo, "pack"}},
		{"pack: unknown flag", []string{"--repo=" + repo, "pack", "--capacity=64MiB", "--no-such-flag"}},
		{"gc: unexpected positional argument", []string{"--repo=" + repo, "gc", "extra"}},
		{"ls: missing SNAPSHOT", []string{"--repo=" + repo, "ls"}},
		{"log: unknown flag", []string{"--repo=" + repo, "log", "--no-such-flag"}},
		{"verify: missing DISC-ROOT", []string{"--repo=" + repo, "verify"}},
		{"restore: unknown flag", []string{"--repo=" + repo, "restore", "--no-such-flag"}},
		{"restore: --dry-run without --mount", []string{"--repo=" + repo, "restore", "--dry-run", defaultRefName(), filepath.Join(work, "out")}},
		{"disc: unknown subcommand", []string{"disc", "bogus"}},
		{"disc burned: missing DISC", []string{"--repo=" + repo, "disc", "burned"}},
		{"recover: unknown flag", []string{"--repo=" + repo, "recover", "--no-such-flag"}},
		{"image build: missing --out", []string{"image", "build", work}},
		{"status: unexpected positional argument", []string{"--repo=" + repo, "status", "extra"}},
		{"pack: capacity without a unit", []string{"--repo=" + repo, "pack", "--capacity=7500000"}},
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
