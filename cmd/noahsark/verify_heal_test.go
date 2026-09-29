package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyHealRefusesAnOutThatHoldsFiles checks that verify --heal
// refuses an --out directory that holds files, and a path that is not a
// directory, with exit code 2. An empty directory is accepted.
func TestVerifyHealRefusesAnOutThatHoldsFiles(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	tree := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec", "--out="+tree); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	full := filepath.Join(work, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "old"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+full, tree)
	if code != 2 || !strings.Contains(out, "--out="+full+" holds files; give an empty or absent directory") {
		t.Fatalf("verify --heal into a directory that holds files: exit %d, want 2 and the refusal: %s", code, out)
	}
	if entries, err := os.ReadDir(full); err != nil || len(entries) != 1 {
		t.Fatalf("verify --heal changed %s: %v %v", full, entries, err)
	}

	file := filepath.Join(work, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+file, tree)
	if code != 2 || !strings.Contains(out, "--out="+file+" is not a directory") {
		t.Fatalf("verify --heal into a file: exit %d, want 2 and the refusal: %s", code, out)
	}

	empty := filepath.Join(work, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+empty, tree); code != 0 {
		t.Fatalf("verify --heal into an empty directory: exit %d: %s", code, out)
	}
}

// TestVerifyHealWithADamagedRunHeader damages RUN.bin of a disc. A plain
// verify of the disc is bad. verify --heal takes the run header from
// RUN2.bin, writes RUN.bin again, and the healed disc root checks ok.
func TestVerifyHealWithADamagedRunHeader(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	tree := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec", "--out="+tree); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	runPath := filepath.Join(tree, "NOAHSARK", "runs", "0000000001", "RUN.bin")
	buf, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatal(err)
	}
	buf[64] ^= 0xff
	if err := os.WriteFile(runPath, buf, 0o644); err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "--repo="+repo, "verify", "--no-mark", tree); code != 1 || !strings.Contains(out, ": bad; ") {
		t.Fatalf("verify of a disc with a damaged RUN.bin: exit %d, want 1 and the bad line: %s", code, out)
	}
	healed := filepath.Join(work, "healed")
	code, out := runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+healed, tree)
	if code != 0 || !strings.Contains(out, ": healed 1 file(s) into "+healed+"\n") || !strings.Contains(out, " items, ok\n") {
		t.Fatalf("verify --heal: exit %d, want 0, one healed file and the ok line: %s", code, out)
	}
}
