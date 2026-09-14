package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var discListLineRe = regexp.MustCompile(`^([0-9a-f-]{36})  seq=(\d+)  label="([^"]*)"  capacity=(\d+)  used=(\d+)  runs=(\d+)  objects=(\d+)$`)

// TestDiscListReportsPackedDiscs packs one disc and checks that
// "disc list" prints its uuid, seq, label, capacity, used bytes, run
// count and packed object count, plus the staged line.
func TestDiscListReportsPackedDiscs(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir, "--label=my disc"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "list", "--repo="+repo)
	if code != 0 {
		t.Fatalf("disc list: exit %d: %s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("disc list output = %q, want one disc line and one staged line", out)
	}
	m := discListLineRe.FindStringSubmatch(lines[0])
	if m == nil {
		t.Fatalf("disc line %q does not match the expected column order", lines[0])
	}
	if m[2] != "0" {
		t.Fatalf("seq = %q, want 0 for the first disc", m[2])
	}
	if m[3] != "my disc" {
		t.Fatalf("label = %q, want %q", m[3], "my disc")
	}
	if m[4] == "0" {
		t.Fatalf("capacity = %q, want nonzero", m[4])
	}
	if m[5] == "0" {
		t.Fatalf("used = %q, want nonzero after a pack", m[5])
	}
	if m[6] != "1" {
		t.Fatalf("runs = %q, want 1", m[6])
	}
	if m[7] == "0" {
		t.Fatalf("objects = %q, want nonzero after a pack", m[7])
	}
	if lines[1] != "staged: 0 objects, 0 bytes" {
		t.Fatalf("staged line = %q, want \"staged: 0 objects, 0 bytes\"", lines[1])
	}
}

// TestDiscListJSON checks --json produces the same fields as the text
// form, machine-readable.
func TestDiscListJSON(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir, "--label=json disc"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "list", "--repo="+repo, "--json")
	if code != 0 {
		t.Fatalf("disc list --json: exit %d: %s", code, out)
	}
	var parsed struct {
		Discs []struct {
			UUID  string `json:"uuid"`
			Label string `json:"label"`
		} `json:"discs"`
		StagedObjects int `json:"staged_objects"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("disc list --json: invalid JSON: %v: %s", err, out)
	}
	if len(parsed.Discs) != 1 || parsed.Discs[0].Label != "json disc" {
		t.Fatalf("disc list --json = %+v, want one disc labelled %q", parsed, "json disc")
	}
	if parsed.StagedObjects != 0 {
		t.Fatalf("staged_objects = %d, want 0", parsed.StagedObjects)
	}
}

// TestDiscListEmptyRepository checks disc list on a repository with no
// pack yet: no disc lines, and the staged line still prints.
func TestDiscListEmptyRepository(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "disc", "list", "--repo="+repo)
	if code != 0 {
		t.Fatalf("disc list: exit %d: %s", code, out)
	}
	if out != "staged: 0 objects, 0 bytes\n" {
		t.Fatalf("disc list output = %q, want just the staged line", out)
	}
}

// TestDiscLabelAndMarkDegradedRefused checks that "disc label" and
// "disc mark-degraded" are refused with a clear message, not silently
// ignored.
func TestDiscLabelAndMarkDegradedRefused(t *testing.T) {
	for _, args := range [][]string{
		{"disc", "label", "00000000-0000-0000-0000-000000000000", "TEXT"},
		{"disc", "mark-degraded", "00000000-0000-0000-0000-000000000000"},
	} {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, "not in this build") {
			t.Fatalf("%v: output %q, want \"not in this build\"", args, out)
		}
	}
}
