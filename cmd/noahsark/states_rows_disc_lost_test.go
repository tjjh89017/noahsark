package main

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(stateCase{
			row: "57", name: s.String() + " disc marked lost",
			start: s, args: []string{"disc", "lost", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscLost,
		})
	}
	registerStateCases(
		// Rows 58 and 59: the Lost event is newer than each snapshot, so
		// status gives the commit block.
		stateCase{
			row: "58", name: "on disc only disc marked lost",
			start: stage.DiscOnDiscOnly, setup: laterClockSetup,
			args:  []string{"disc", "lost", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscLost, word: stage.WordLost,
			check: statusShows(commitBlock...),
		},
		stateCase{
			row: "59", name: "missing disc marked lost",
			start: stage.DiscMissing, setup: laterClockSetup,
			args:  []string{"disc", "lost", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscLost,
			check: statusShows(commitBlock...),
		},
	)
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscMissing} {
		registerStateCases(
			stateCase{
				row: "59a", name: s.String() + " disc lost, answer no",
				start: s, args: []string{"disc", "lost", "{SEQ}"},
				stdin:  stdinNo,
				absent: []string{"marked lost", "needs --force-yes"},
				end:    s,
			},
			stateCase{
				row: "66", name: "lost undo of a " + s.String() + " disc",
				start: s, args: []string{"disc", "lost", "--undo", "{SEQ}"},
				stdin:  stdinYes,
				absent: []string{confirmQuestion, "warning:"},
				end:    s,
			},
		)
	}
	registerStateCases(
		stateCase{
			row: "59b", name: "disc lost with no terminal and no answer flag", like: "57",
			start: stage.DiscBurned, args: []string{"disc", "lost", "{SEQ}"},
			absent: []string{confirmQuestion, "needs --force-yes"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "60", name: "lost disc marked lost again",
			start: stage.DiscLost, args: []string{"--force-yes", "disc", "lost", "{SEQ}"},
			absent: []string{"warning:"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "61", name: "verified lost disc found, answer yes",
			start: stage.DiscLost, args: []string{"disc", "lost", "--undo", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "64", name: "verified lost disc found, answer no",
			start: stage.DiscLost, args: []string{"disc", "lost", "--undo", "{SEQ}"},
			stdin: stdinNo,
			end:   stage.DiscLost,
		},
		stateCase{
			row: "80", name: "lost undo with --yes", like: "61",
			start: stage.DiscLost, args: []string{"--yes", "disc", "lost", "--undo", "{SEQ}"},
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "81", name: "lost undo with no terminal and no answer flag", like: "61",
			start: stage.DiscLost, args: []string{"disc", "lost", "--undo", "{SEQ}"},
			absent: []string{confirmQuestion},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "82", name: "disc lost with --force-yes", like: "57",
			start: stage.DiscVerified, args: []string{"--force-yes", "disc", "lost", "{SEQ}"},
			end: stage.DiscLost,
		},
		stateCase{
			row: "83", name: "disc lost with --yes and no terminal", like: "57",
			start: stage.DiscVerified, args: []string{"--yes", "disc", "lost", "{SEQ}"},
			omit:   []string{"nothing changed; disc verified needs --force-yes"},
			absent: []string{confirmQuestion},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "84", name: "disc lost with --yes, answer yes", like: "57",
			start: stage.DiscVerified, args: []string{"--yes", "disc", "lost", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscLost,
		},
		stateCase{
			row: "84a", name: "disc lost with --yes, answer no", like: "57",
			start: stage.DiscVerified, args: []string{"--yes", "disc", "lost", "{SEQ}"},
			stdin: stdinNo,
			end:   stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "64", name: "missing disc found, answer no",
			start: stage.DiscMissing, from: stage.DiscLost, setup: lostSetup,
			args:  []string{"disc", "lost", "--undo", "{UUID}"},
			stdin: stdinNo,
			end:   stage.DiscLost,
		},
		// Row 57 with the count: the items return to staged, the plan
		// directory is removed, and the catalog data of the disc stays.
		stateCase{
			row: "57", name: "verified disc marked lost, the count",
			start: stage.DiscVerified, setup: countItemsSetup,
			args: []string{"--force-yes", "disc", "lost", "0"},
			end:  stage.DiscLost,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				u := fx.uuidBytes(t)
				if got := countByState(t, fx.repo, stage.Staged); got != itemCount(t, fx) {
					t.Errorf("%d staged items, want %d", got, itemCount(t, fx))
				}
				if _, err := os.Lstat(testLayout(t, fx.repo).planDir(u)); !os.IsNotExist(err) {
					t.Errorf("plan directory stays: %v", err)
				}
				c, err := catalog.Open(fx.repo)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := c.IndexForDisc(u); err != nil {
					t.Errorf("catalog INDEX of the lost disc: %v", err)
				}
			},
		},
		// Row 58 with the counts: each freed chunk is lost, and each
		// object that the catalog holds is staged. status prints the lost
		// line, and pack takes the staged objects from the catalog.
		stateCase{
			row: "58", name: "on disc only disc marked lost, the counts",
			start: stage.DiscOnDiscOnly, setup: catalogSplitSetup,
			args: []string{"--force-yes", "disc", "lost", "0"},
			end:  stage.DiscLost, word: stage.WordLost,
			check: allChecks(
				func(t *testing.T, fx *discFixture, _, _ string) {
					if got := countByState(t, fx.repo, stage.Lost); got != fixtureNumber(t, fx, "{CHUNKS}") {
						t.Errorf("%d lost items, want %s", got, fx.vars["{CHUNKS}"])
					}
					if got := countByState(t, fx.repo, stage.Staged); got != fixtureNumber(t, fx, "{OBJECTS}") {
						t.Errorf("%d staged items, want %s", got, fx.vars["{OBJECTS}"])
					}
				},
				statusShows("lost: {CHUNKS} items; only a lost disc holds them\n"),
				func(t *testing.T, fx *discFixture, _, _ string) {
					out := fx.mustRun(t, "pack", "--capacity=64MiB")
					if !strings.Contains(out, ": "+fx.vars["{OBJECTS}"]+" item(s), ") {
						t.Errorf("pack output %q, want the %s staged objects", out, fx.vars["{OBJECTS}"])
					}
				},
			),
		},
		// Row 61 after a real disc lost: the disc is burned with no
		// verified time, and each item is burned again.
		stateCase{
			row: "61", name: "verified disc found, the count",
			start: stage.DiscVerified, from: stage.DiscLost, setup: lostSetup,
			args: []string{"--yes", "disc", "lost", "--undo", "0"},
			end:  stage.DiscBurned, word: stage.WordBurned,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if d := discState(t, fx.repo, fx.uuid); !d.VerifiedTime.IsZero() {
					t.Errorf("verified time %v, want none", d.VerifiedTime)
				}
				if words := itemWords(t, fx.repo, fx.uuid); words[stage.WordBurned] != itemCount(t, fx) {
					t.Errorf("item words %v, want %d burned", words, itemCount(t, fx))
				}
			},
		},
		// Row 61 when a later pack took the items: they stay on the new
		// disc.
		stateCase{
			row: "61", name: "verified disc found, a later pack took the items",
			start: stage.DiscVerified, from: stage.DiscLost,
			setup: func(t *testing.T, fx *discFixture) {
				lostSetup(t, fx)
				fx.mustRun(t, "pack", "--capacity=64MiB")
				fx.cell("N", "0")
			},
			args: []string{"--yes", "disc", "lost", "--undo", "0"},
			end:  stage.DiscBurned,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if n := len(itemWords(t, fx.repo, fx.uuid)); n != 0 {
					t.Errorf("the found disc has %d item word(s), want none", n)
				}
			},
		},
		// Row 62: status gives the found disc the verify lines until a
		// verify logs a check.
		stateCase{
			row: "62", name: "on disc only disc found",
			start: stage.DiscOnDiscOnly, from: stage.DiscLost, setup: lostSetup,
			args: []string{"--yes", "disc", "lost", "--undo", "0"},
			end:  stage.DiscOnDiscOnly, word: stage.WordOnDisc,
			check: allChecks(
				func(t *testing.T, fx *discFixture, _, _ string) {
					if words := itemWords(t, fx.repo, fx.uuid); words[stage.WordOnDisc] != itemCount(t, fx) {
						t.Errorf("item words %v, want %d on-disc", words, itemCount(t, fx))
					}
				},
				statusShows(append([]string{"next: load disc {SEQ}, then run:\n"}, statusVerifyLines...)...),
				func(t *testing.T, fx *discFixture, _, _ string) {
					fx.mustRun(t, "verify", fx.root)
				},
				statusShows("next: nothing to do\n"),
			),
		},
		stateCase{
			row: "62", name: "on disc only disc found, answer yes",
			start: stage.DiscOnDiscOnly, from: stage.DiscLost, setup: lostSetup,
			args:  []string{"disc", "lost", "--undo", "0"},
			stdin: stdinYes,
			end:   stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "63", name: "missing disc found",
			start: stage.DiscMissing, from: stage.DiscLost, setup: lostSetup,
			args:   []string{"--yes", "disc", "lost", "--undo", "{UUID}"},
			absent: []string{"verify"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "64", name: "on disc only disc found, answer no",
			start: stage.DiscOnDiscOnly, from: stage.DiscLost, setup: lostSetup,
			args:  []string{"disc", "lost", "--undo", "0"},
			stdin: stdinNo,
			end:   stage.DiscLost,
		},
	)
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscMissing} {
		registerStateCases(stateCase{
			row: "59b", name: s.String() + " disc lost with no terminal and no answer flag", like: "57",
			start: s, args: []string{"disc", "lost", "{SEQ}"},
			absent: []string{confirmQuestion, "needs --force-yes"},
			end:    s,
		})
	}
	// Rows 82 to 84a for the states that the cases of disc verified and
	// of the verified disc do not cover. like is the answer-yes row of
	// the state.
	for _, c := range []struct {
		s    stage.DiscState
		like string
	}{{stage.DiscPacked, "57"}, {stage.DiscOnDiscOnly, "58"}, {stage.DiscMissing, "59"}} {
		registerStateCases(
			stateCase{
				row: "82", name: c.s.String() + " disc lost with --force-yes", like: c.like,
				start: c.s, args: []string{"--force-yes", "disc", "lost", "{SEQ}"},
				end: stage.DiscLost,
			},
			stateCase{
				row: "83", name: c.s.String() + " disc lost with --yes and no terminal", like: c.like,
				start: c.s, args: []string{"--yes", "disc", "lost", "{SEQ}"},
				omit:   []string{"nothing changed; disc verified needs --force-yes"},
				absent: []string{confirmQuestion},
				end:    c.s,
			},
			stateCase{
				row: "84", name: c.s.String() + " disc lost with --yes, answer yes", like: c.like,
				start: c.s, args: []string{"--yes", "disc", "lost", "{SEQ}"},
				stdin: stdinYes,
				end:   stage.DiscLost,
			},
			stateCase{
				row: "84a", name: c.s.String() + " disc lost with --yes, answer no", like: c.like,
				start: c.s, args: []string{"--yes", "disc", "lost", "{SEQ}"},
				stdin: stdinNo,
				end:   c.s,
			},
		)
	}
	// Rows 80 and 81 for a disc that was on disc only or missing when it
	// was marked lost.
	for _, c := range []struct {
		s    stage.DiscState
		like string
	}{{stage.DiscOnDiscOnly, "62"}, {stage.DiscMissing, "63"}} {
		registerStateCases(
			stateCase{
				row: "80", name: "lost undo with --yes, " + c.s.String() + " when marked lost", like: c.like,
				start: c.s, from: stage.DiscLost, setup: lostSetup,
				args: []string{"--yes", "disc", "lost", "--undo", "{UUID}"},
				end:  c.s,
			},
			stateCase{
				row: "81", name: "lost undo with no terminal, " + c.s.String() + " when marked lost", like: c.like,
				start: c.s, from: stage.DiscLost, setup: lostSetup,
				args:   []string{"disc", "lost", "--undo", "{UUID}"},
				absent: []string{confirmQuestion},
				end:    stage.DiscLost,
			},
		)
	}
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned} {
		registerStateCases(stateCase{
			row: "65", name: s.String() + " disc found",
			start: s, from: stage.DiscLost, setup: lostSetup,
			args:   []string{"--force-yes", "disc", "lost", "--undo", "0"},
			absent: []string{"warning:"},
			end:    stage.DiscLost,
		})
	}
}

// lostSetup marks the disc of fx lost with disc lost, from the state
// that repoWithDisc built. It sets {N} to the number of items of the
// disc in the item state that disc lost changes: Packed for a packed,
// burned or verified disc, OnDisc for an on disc only disc.
func lostSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	before := discState(t, fx.repo, fx.uuid).State
	countItemsSetup(t, fx)
	fx.mustRun(t, "--force-yes", "disc", "lost", fx.uuid)
	if got := discState(t, fx.repo, fx.uuid); got.State != stage.DiscLost || got.BeforeLost != before {
		t.Fatalf("disc state %s before lost %s, want lost before lost %s", got.State, got.BeforeLost, before)
	}
}

// countItemsSetup sets {N} and the placeholder N of the cells to the
// number of Packed items, or to the number of OnDisc items when no item
// is Packed.
func countItemsSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	n := countByState(t, fx.repo, stage.Packed)
	if n == 0 {
		n = countByState(t, fx.repo, stage.OnDisc)
	}
	fx.set("{N}", strconv.Itoa(n))
	fx.cell("N", strconv.Itoa(n))
}

// catalogSplitSetup counts the OnDisc items of the disc of fx: {OBJECTS}
// and the cell I are the items that the catalog holds, {CHUNKS} and the
// cell N the other items.
func catalogSplitSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	holds := catalogHolds(fx.repo)
	objects, chunks := 0, 0
	for _, id := range openTestLog(t, fx.repo).ItemsOfDiscInState(fx.uuidBytes(t), stage.OnDisc) {
		if holds(id) {
			objects++
		} else {
			chunks++
		}
	}
	if objects == 0 || chunks == 0 {
		t.Fatalf("the disc holds %d catalog objects and %d chunks, want both", objects, chunks)
	}
	fx.set("{OBJECTS}", strconv.Itoa(objects))
	fx.set("{CHUNKS}", strconv.Itoa(chunks))
	fx.cell("I", strconv.Itoa(objects))
	fx.cell("N", strconv.Itoa(chunks))
}

// itemCount is the {N} number of countItemsSetup.
func itemCount(t *testing.T, fx *discFixture) int {
	t.Helper()
	n, err := strconv.Atoi(fx.vars["{N}"])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// wantLines fails when out does not hold each of want, in order. out is
// standard output and then standard error.
func wantLines(t *testing.T, out string, want ...string) {
	t.Helper()
	wantInOrder(t, "output", out, want, func(s string) string { return s })
}

// TestDiscLostPackOut checks that disc lost of a pack --out disc removes
// the plan directory and keeps the directory that holds the disc root.
func TestDiscLostPackOut(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscMissing)
	u := fx.uuidBytes(t)
	fx.mustRun(t, "--force-yes", "disc", "lost", fx.uuid)
	if _, err := os.Lstat(testLayout(t, fx.repo).planDir(u)); !os.IsNotExist(err) {
		t.Errorf("plan directory stays: %v", err)
	}
	if _, err := os.Stat(fx.root); err != nil {
		t.Errorf("the pack --out directory is gone: %v", err)
	}
}
