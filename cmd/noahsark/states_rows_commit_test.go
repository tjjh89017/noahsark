package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	registerStateCases(
		stateCase{
			row: "1", name: "commit, the one new item is the snapshot",
			start: stage.DiscPacked, args: []string{"commit", "{SRC}"},
			stdout: []string{"new items: 1, existing items: ", "staged: 1 items, "}, next: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "1", name: "commit, the items are staged",
			start: stage.DiscUndone, args: []string{"commit", "{SRC}"},
			stdout: []string{"new items: 1, existing items: ", "staged: "}, next: true,
			end: stage.DiscUndone,
		},
		stateCase{
			row: "3", name: "commit after the disc is marked lost",
			start: stage.DiscLost, args: []string{"commit", "{SRC}"},
			stdout: []string{"new items: ", "staged: "}, next: true,
			end: stage.DiscLost,
		},
	)
}

// commitLinesRe matches the lines of a commit with no unstable, skipped,
// special or excluded path, in order.
var commitLinesRe = regexp.MustCompile(`^snapshot \S+\n` +
	`ref \S+ -> \S+\n` +
	`new items: \d+, existing items: \d+\n` +
	`unstable: 0, skipped: 0\n` +
	`staged: \d+ items, \d+ bytes\n` +
	`next: noahsark status\n$`)

// TestStatesRow1CommitLines checks every line of a first commit in a new
// repository, where each item is new.
func TestStatesRow1CommitLines(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !commitLinesRe.MatchString(out) {
		t.Fatalf("commit output %q, want the lines snapshot, ref, new items, unstable, staged, next", out)
	}
	if n := countByState(t, repo, stage.Staged); n == 0 {
		t.Fatal("no item is staged after the first commit")
	}
}

// TestStatesRow1CommitSkippedPrintsNext checks that a commit that skips
// a file exits 1 and still ends with the next line: the snapshot is
// committed all the same.
func TestStatesRow1CommitSkippedPrintsNext(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file with mode 0")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	unreadable := filepath.Join(src, "unreadable.txt")
	if err := os.WriteFile(unreadable, []byte("no access"), 0o000); err != nil {
		t.Fatal(err)
	}
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "unstable: 0, skipped: 1\n") {
		t.Fatalf("commit output %q, want the count line with one skipped path", out)
	}
	if !strings.HasSuffix(out, "\nnext: noahsark status\n") {
		t.Fatalf("commit output %q, want the next line as the last line", out)
	}
}

// repoWithLostItems returns a repository whose one disc was on disc only
// and is now lost. Each item of the disc is Lost.
func repoWithLostItems(t *testing.T) (fx *discFixture, lost int) {
	t.Helper()
	fx = repoWithDisc(t, stage.DiscOnDiscOnly)
	markDiscLostInLog(t, fx)
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscLost {
		t.Fatalf("disc state %s, want lost", got)
	}
	lost = countByState(t, fx.repo, stage.Lost)
	if lost == 0 {
		t.Fatal("the fixture has no lost item")
	}
	if n := countByState(t, fx.repo, stage.Staged); n != 0 {
		t.Fatalf("the fixture has %d staged items, want 0", n)
	}
	return fx, lost
}

// TestStatesRow3CommitRestagesLostItems checks that a commit of a source
// that still has the data stages each lost item again, writes its chunk
// file again, and counts it in the staged line.
func TestStatesRow3CommitRestagesLostItems(t *testing.T) {
	fx, lost := repoWithLostItems(t)

	code, out := fx.run(t, "commit", fx.src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !strings.HasSuffix(out, "\nnext: noahsark status\n") {
		t.Fatalf("commit output %q, want the next line as the last line", out)
	}
	// The old snapshot object is not in the new commit, so it stays lost.
	if n := countByState(t, fx.repo, stage.Lost); n != 1 {
		t.Fatalf("%d items stay lost, want 1 (the old snapshot)", n)
	}
	// The staged items are the other lost items and the new snapshot.
	staged := countByState(t, fx.repo, stage.Staged)
	if staged != lost {
		t.Fatalf("%d items staged, want %d", staged, lost)
	}
	m := stagedLineRe.FindStringSubmatch(out)
	if m == nil || m[1] != strconv.Itoa(staged) {
		t.Fatalf("commit output %q, want the line staged: %d items", out, staged)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscLost {
		t.Errorf("disc state %s, want lost", got)
	}
	// pack reads the chunk file of each staged item.
	fx.mustRun(t, "pack", "--capacity=64MiB")
}

// TestStatesRow4CommitLeavesLostItems checks that a commit of a source
// that no longer has the data succeeds, names no lost item, and leaves
// each lost item lost.
func TestStatesRow4CommitLeavesLostItems(t *testing.T) {
	fx, lost := repoWithLostItems(t)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "other.txt"), []byte("other content"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := fx.run(t, "commit", other)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !commitLinesRe.MatchString(out) {
		t.Fatalf("commit output %q, want only the lines snapshot, ref, new items, unstable, staged, next", out)
	}
	if n := countByState(t, fx.repo, stage.Lost); n != lost {
		t.Fatalf("%d items lost, want %d", n, lost)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscLost {
		t.Errorf("disc state %s, want lost", got)
	}
}
