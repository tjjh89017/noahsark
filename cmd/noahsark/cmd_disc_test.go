package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var discListLineRe = regexp.MustCompile(`^([0-9a-f-]{36})  seq=(\d+)  label="([^"]*)"  capacity=(\d+)  used=(\d+)  runs=(\d+)  objects=(\d+)  packed=(\d+)  clean=(\d+)$`)

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

// TestDiscListUsedSectorsSurviveRebuildCache packs one disc, rebuilds
// the repository from that disc alone, and checks "disc list" still
// reports a nonzero used value: the disc's own DISCS.bin row always
// carries used_sectors 0 for itself, so rebuild-cache must derive the
// real value from the disc's own RUN.bin instead of copying the row
// as-is.
func TestDiscListUsedSectorsSurviveRebuildCache(t *testing.T) {
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
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "rebuild-cache", "--from-disc", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("rebuild-cache: exit %d: %s", code, out)
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
	if m[5] == "0" {
		t.Fatalf("used = %q, want nonzero after rebuild-cache", m[5])
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
// TestDiscFlagBeforeSubcommandNamesTheFix checks that a flag given
// before disc's subcommand ("disc --repo=X list") is reported with the
// corrected command line, not as an unknown subcommand.
func TestDiscFlagBeforeSubcommandNamesTheFix(t *testing.T) {
	code, out := runCmd(t, "disc", "--repo=X", "list")
	if code != 2 {
		t.Fatalf("disc --repo=X list: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "flags come after the subcommand: noahsark disc list --repo=X") {
		t.Fatalf("disc --repo=X list: output %q, want the flags-come-after-the-subcommand fix", out)
	}
	if strings.Contains(out, "unknown subcommand") {
		t.Fatalf("disc --repo=X list: output %q, want no unknown-subcommand wording", out)
	}
}

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
