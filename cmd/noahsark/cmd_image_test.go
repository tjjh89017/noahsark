package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImageBuildRefusesExistingOutputUnlessForced checks that "image
// build" refuses to overwrite an existing --out path, the same way
// pack refuses an existing --out directory, and that --force lets it
// proceed (here failing later, on the tree directory itself, to prove
// the existence check no longer stood in the way).
func TestImageBuildRefusesExistingOutputUnlessForced(t *testing.T) {
	work := t.TempDir()
	out := filepath.Join(work, "run.img")
	if err := os.WriteFile(out, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	treeDir := filepath.Join(work, "tree")

	code, cmdOut := runCmd(t, "image", "build", "--out="+out, "--capacity=1MB", treeDir)
	if code != 2 {
		t.Fatalf("image build (existing --out): exit %d, want 2: %s", code, cmdOut)
	}
	if !strings.Contains(cmdOut, "already exists") || !strings.Contains(cmdOut, "--force") {
		t.Fatalf("image build (existing --out) output %q missing the --force hint", cmdOut)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "already here" {
		t.Fatalf("image build (existing --out) overwrote %s despite refusing", out)
	}

	code, cmdOut = runCmd(t, "image", "build", "--out="+out, "--capacity=1MB", "--force", treeDir)
	if code == 2 && strings.Contains(cmdOut, "already exists") {
		t.Fatalf("image build --force still refused an existing --out: %s", cmdOut)
	}
}
