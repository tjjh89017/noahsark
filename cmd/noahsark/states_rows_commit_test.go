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
			cells: map[string]string{"N": "1"},
			also:  []string{"new items: 1, existing items: "},
			end:   stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "1", name: "commit, the items are staged",
			start: stage.DiscUndone, args: []string{"commit", "{SRC}"},
			also: []string{"new items: 1, existing items: "},
			end:  stage.DiscUndone,
		},
		stateCase{
			row: "3", name: "commit after the disc is marked lost",
			start: stage.DiscLost, args: []string{"commit", "{SRC}"},
			end: stage.DiscLost,
		},
		// Row 3: the source still has the data. Each lost chunk is staged
		// again, its chunk file is written again, and the staged line
		// counts it with the objects that disc lost staged from the
		// catalog and the new snapshot object. No item stays lost, thus
		// status prints no lost line.
		stateCase{
			row: "3", name: "commit stages each lost item again",
			start: stage.DiscOnDiscOnly, from: stage.DiscLost, setup: lostItemsSetup,
			args: []string{"commit", "{SRC}"},
			end:  stage.DiscLost,
			check: allChecks(
				func(t *testing.T, fx *discFixture, _, _ string) {
					if n := countByState(t, fx.repo, stage.Lost); n != 0 {
						t.Fatalf("%d items stay lost, want 0", n)
					}
					if staged, want := countByState(t, fx.repo, stage.Staged), lostCount(t, fx)+stagedCount(t, fx)+1; staged != want {
						t.Fatalf("%d items staged, want %d", staged, want)
					}
				},
				statusLacks("lost: "),
				// pack reads the chunk file of each staged item.
				func(t *testing.T, fx *discFixture, _, _ string) { fx.mustRun(t, "pack", "--capacity=64MiB") },
			),
		},
		// Row 4: the source no longer has the data. The commit names no
		// lost item, and each lost item stays lost.
		stateCase{
			row: "4", name: "commit of a source without the lost data",
			start: stage.DiscOnDiscOnly, from: stage.DiscLost,
			setup: func(t *testing.T, fx *discFixture) {
				laterClockSetup(t, fx)
				lostItemsSetup(t, fx)
				statusShows(commitBlock...)(t, fx, "", "")
				// N of this row counts the items of the other source,
				// not the lost items.
				fx.cell("N", "")
				other := t.TempDir()
				if err := os.WriteFile(filepath.Join(other, "other.txt"), []byte("other content"), 0o644); err != nil {
					t.Fatal(err)
				}
				fx.set("{OTHER}", other)
			},
			args: []string{"commit", "{OTHER}"},
			end:  stage.DiscLost,
			check: func(t *testing.T, fx *discFixture, stdout, stderr string) {
				if out := stdout + stderr; !commitLinesRe.MatchString(out) {
					t.Fatalf("commit output %q, want only the lines snapshot, ref, new items, unstable, staged, next", out)
				}
				if n := countByState(t, fx.repo, stage.Lost); n != lostCount(t, fx) {
					t.Fatalf("%d items lost, want %d", n, lostCount(t, fx))
				}
				// The commit ends the commit block, also for the items
				// that stay lost. The lost line stays.
				statusLacks(commitBlock[0])(t, fx, stdout, stderr)
				statusShows("lost: {LOST} items; only a lost disc holds them\n")(t, fx, stdout, stderr)
			},
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

// lostItemsSetup marks the on disc only disc of fx lost, as disc lost
// does. Each chunk of the disc is then Lost, and each other item Staged.
// {LOST} is the number of lost items, and {STAGED} the number of staged
// items. A commit of the same source stages the lost items again and adds
// its snapshot object, thus the count of its staged line is the sum of
// both and 1.
func lostItemsSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	markDiscLostInLog(t, fx)
	lost := countByState(t, fx.repo, stage.Lost)
	staged := countByState(t, fx.repo, stage.Staged)
	if lost == 0 || staged == 0 {
		t.Fatalf("the fixture has %d lost and %d staged items, want both", lost, staged)
	}
	fx.set("{LOST}", strconv.Itoa(lost))
	fx.set("{STAGED}", strconv.Itoa(staged))
	fx.cell("N", strconv.Itoa(lost+staged+1))
}

// lostCount is the {LOST} number of lostItemsSetup.
func lostCount(t *testing.T, fx *discFixture) int {
	t.Helper()
	return fixtureNumber(t, fx, "{LOST}")
}

// stagedCount is the {STAGED} number of lostItemsSetup.
func stagedCount(t *testing.T, fx *discFixture) int {
	t.Helper()
	return fixtureNumber(t, fx, "{STAGED}")
}

// fixtureNumber is the number that the placeholder key of fx stands for.
func fixtureNumber(t *testing.T, fx *discFixture, key string) int {
	t.Helper()
	n, err := strconv.Atoi(fx.vars[key])
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return n
}
