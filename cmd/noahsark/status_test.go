package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// statusDiscLineRe matches one disc line of "status": the disc number,
// the label, the one-word state and the uuid.
var statusDiscLineRe = regexp.MustCompile(`^disc (\d+) "([^"]*)"  ([a-z0-9 /]+)  ([0-9a-f-]{36})$`)

// statusLines runs status and returns its lines.
func statusLines(t *testing.T, repo string) []string {
	t.Helper()
	code, out := runCmd(t, "--repo="+repo, "status")
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
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
	if m[1] != "0" || m[2] != defaultRefName()+" disc 0" || m[3] != "packed" {
		t.Fatalf("disc line = %q, want disc 0 %q packed", lines[1], defaultRefName()+" disc 0")
	}
	if lines[2] != "next: burn disc 0, then run: noahsark disc burned 0" {
		t.Fatalf("next line = %q", lines[2])
	}
}

// TestStatusNextLinesFollowTheCycle walks one disc from packed to
// verified and checks the "next" line at each step: burn it, then
// nothing to do after one verify.
func TestStatusNextLinesFollowTheCycle(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	lines := statusLines(t, repo)
	if got := lines[len(lines)-1]; got != "next: burn disc 0, then run: noahsark disc burned 0" {
		t.Fatalf("next line after pack = %q", got)
	}

	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", "0"); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "verify", treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	lines = statusLines(t, repo)
	if got := lines[len(lines)-1]; got != "next: nothing to do" {
		t.Fatalf("next line after the verify = %q, want next: nothing to do", got)
	}
	if !strings.Contains(lines[1], "verified") || strings.Contains(lines[1], "/") {
		t.Fatalf("disc line after the verify = %q, want the plain word verified", lines[1])
	}
}

// TestStatusDiscsKeepsExactNumbers checks that the disc summaries
// behind "status" carry the exact counts the one-word text form leaves
// out.
func TestStatusDiscsKeepsExactNumbers(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	discs := statusDiscs(t, repo)
	if len(discs) != 1 || discs[0].Label != defaultRefName()+" disc 0" {
		t.Fatalf("status discs = %+v, want one disc labelled %q", discs, defaultRefName()+" disc 0")
	}
	if discs[0].PackedObjects == 0 {
		t.Fatalf("packed_objects = 0, want the exact count after a pack")
	}
	if stagedObjects := countByState(t, repo, stage.Staged); stagedObjects != 0 {
		t.Fatalf("staged_objects = %d, want 0", stagedObjects)
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "recover", treeDir); code != 0 {
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
	want := "next: nothing to do"
	if lines[len(lines)-1] != want {
		t.Fatalf("next line = %q, want %q", lines[len(lines)-1], want)
	}
}

// TestStatusEmptyRepository checks status on a repository with no
// commit and no pack: the staged line, and a next line that names
// commit.
func TestStatusEmptyRepository(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "status")
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

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	lines := statusLines(t, repo)
	if lines[len(lines)-1] != "next: pack a disc, run: noahsark pack" {
		t.Fatalf("next line = %q, want the pack line", lines[len(lines)-1])
	}
}

// TestStatusRunsWhileRepoLockHeld checks that a read-only command
// takes no lock: status must still run, and must not report the
// held exclusive lock, while a writer holds it.
func TestStatusRunsWhileRepoLockHeld(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	held, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	code, out := runCmd(t, "--repo="+repo, "status")
	if code != 0 {
		t.Fatalf("status while a writer holds the lock: exit %d, want 0: %s", code, out)
	}
}

// TestStatusUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// status: each case exits 2, never 0 or 1.
func TestStatusUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"unexpected positional argument", []string{"--repo=" + repo, "status", "extra"}},
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
