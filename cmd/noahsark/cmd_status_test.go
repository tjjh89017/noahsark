package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// statusDiscLineRe matches one disc line of "status": the disc number,
// the label, the one-word state and the uuid.
var statusDiscLineRe = regexp.MustCompile(`^disc (\d+) "([^"]*)"  ([a-z0-9 /]+)  ([0-9a-f-]{36})$`)

// statusLines runs status and returns its lines.
func statusLines(t *testing.T, repo string) []string {
	t.Helper()
	code, out := runCmd(t, "status", "--repo="+repo)
	if code != 0 {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

// TestStatusReportsPackedDisc packs one disc and checks that status
// prints the staged line, one disc line with the word "packed", and the
// next line that names the burn.
func TestStatusReportsPackedDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir, "--label=my disc"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	lines := statusLines(t, repo)
	if len(lines) != 3 {
		t.Fatalf("status output = %q, want a staged line, a disc line and a next line", lines)
	}
	if lines[0] != "staged: 0 objects, 0 bytes" {
		t.Fatalf("staged line = %q", lines[0])
	}
	m := statusDiscLineRe.FindStringSubmatch(lines[1])
	if m == nil {
		t.Fatalf("disc line %q does not match the expected shape", lines[1])
	}
	if m[1] != "0" || m[2] != "my disc" || m[3] != "packed" {
		t.Fatalf("disc line = %q, want disc 0 %q packed", lines[1], "my disc")
	}
	if lines[2] != "next: burn disc 0, then run: noahsark disc burned 0" {
		t.Fatalf("next line = %q", lines[2])
	}
}

// TestStatusNextLinesFollowTheCycle walks one disc from packed to
// verified and checks the "next" line at each step: burn it, verify the
// second copy, then nothing to do.
func TestStatusNextLinesFollowTheCycle(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	lines := statusLines(t, repo)
	if got := lines[len(lines)-1]; got != "next: burn disc 0, then run: noahsark disc burned 0" {
		t.Fatalf("next line after pack = %q", got)
	}

	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, "0"); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "verify", "--repo="+repo, treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	lines = statusLines(t, repo)
	if got := lines[len(lines)-1]; got != "next: verify the second copy of disc 0" {
		t.Fatalf("next line after the first verify = %q", got)
	}
	if !strings.Contains(lines[1], "verified 1/2") {
		t.Fatalf("disc line after the first verify = %q, want verified 1/2", lines[1])
	}

	if code, out := runCmd(t, "verify", "--repo="+repo, treeDir); code != 0 {
		t.Fatalf("verify (second copy): exit %d: %s", code, out)
	}
	lines = statusLines(t, repo)
	if got := lines[len(lines)-1]; got != "next: nothing to do" {
		t.Fatalf("next line after the second verify = %q", got)
	}
	if !strings.Contains(lines[1], "verified") || strings.Contains(lines[1], "/") {
		t.Fatalf("disc line after the second verify = %q, want the plain word verified", lines[1])
	}
}

// TestStatusJSONKeepsExactNumbers checks --json still carries the exact
// counts the one-word text form leaves out.
func TestStatusJSONKeepsExactNumbers(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir, "--label=json disc"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "status", "--repo="+repo, "--json")
	if code != 0 {
		t.Fatalf("status --json: exit %d: %s", code, out)
	}
	var parsed struct {
		Discs []struct {
			UUID          string `json:"uuid"`
			Label         string `json:"label"`
			PackedObjects int    `json:"packed_objects"`
		} `json:"discs"`
		StagedObjects int `json:"staged_objects"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("status --json: invalid JSON: %v: %s", err, out)
	}
	if len(parsed.Discs) != 1 || parsed.Discs[0].Label != "json disc" {
		t.Fatalf("status --json = %+v, want one disc labelled %q", parsed, "json disc")
	}
	if parsed.Discs[0].PackedObjects == 0 {
		t.Fatalf("packed_objects = 0, want the exact count after a pack")
	}
	if parsed.StagedObjects != 0 {
		t.Fatalf("staged_objects = %d, want 0", parsed.StagedObjects)
	}
}

// TestStatusOnDiscOnlyAfterRecover packs one disc, deletes the
// repository, recovers it from that disc alone, and checks the disc
// reads "on disc only": the disc holds every object and staging holds
// no file for them.
func TestStatusOnDiscOnlyAfterRecover(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "recover", "--repo="+repo, "--disc="+treeDir); code != 0 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	lines := statusLines(t, repo)
	m := statusDiscLineRe.FindStringSubmatch(lines[1])
	if m == nil {
		t.Fatalf("disc line %q does not match the expected shape", lines[1])
	}
	if m[3] != "on disc only" {
		t.Fatalf("disc state = %q, want \"on disc only\"", m[3])
	}
	if lines[len(lines)-1] != "next: nothing to do" {
		t.Fatalf("next line = %q, want nothing to do", lines[len(lines)-1])
	}
}

// TestStatusEmptyRepository checks status on a repository with no
// commit and no pack: the staged line, and a next line that names
// commit.
func TestStatusEmptyRepository(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "status", "--repo="+repo)
	if code != 0 {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	if out != "staged: 0 objects, 0 bytes\nnext: commit your files, run: noahsark commit <SOURCE>\n" {
		t.Fatalf("status output = %q", out)
	}
}

// TestStatusAfterCommitAsksForAPack checks the next line a commit with
// no pack yet leaves.
func TestStatusAfterCommitAsksForAPack(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	lines := statusLines(t, repo)
	if lines[len(lines)-1] != "next: pack a disc, run: noahsark pack" {
		t.Fatalf("next line = %q, want the pack line", lines[len(lines)-1])
	}
}

// statusDisc is one disc of "status --json", as the tests read it.
type statusDisc struct {
	UUID          string `json:"uuid"`
	Seq           uint64 `json:"seq"`
	Label         string `json:"label"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	UsedBytes     uint64 `json:"used_bytes"`
	OnDiscObjects int    `json:"on_disc_objects"`
	PackedObjects int    `json:"packed_objects"`
	CleanObjects  int    `json:"clean_objects"`
}

// statusDiscs runs "status --json" and returns its disc rows.
func statusDiscs(t *testing.T, repo string) []statusDisc {
	t.Helper()
	code, out := runCmd(t, "status", "--repo="+repo, "--json")
	if code != 0 {
		t.Fatalf("status --json: exit %d: %s", code, out)
	}
	var parsed struct {
		Discs []statusDisc `json:"discs"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("status --json: invalid JSON: %v: %s", err, out)
	}
	return parsed.Discs
}
