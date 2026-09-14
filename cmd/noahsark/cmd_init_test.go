package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestInitRefusesACapacityTooSmall checks that init validates --capacity
// the same way pack does, before writing disc.force_capacity into the
// config: a value too small for even an empty run must be refused at
// init time, not accepted and left to fail every later pack.
func TestInitRefusesACapacityTooSmall(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	code, out := runCmd(t, "init", "--repo="+repo, "--capacity=25")
	if code != 2 {
		t.Fatalf("init --capacity=25: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "too small") {
		t.Fatalf("init --capacity=25: output %q does not explain the too-small capacity", out)
	}
	if isRepoDir(repo) {
		t.Fatal("init --capacity=25 must not create a repository with an unusable capacity")
	}
}
