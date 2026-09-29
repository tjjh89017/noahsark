package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// lostWarning is the warning of "disc lost" for a disc in state.
func lostWarning(state stage.DiscState) []string {
	return []string{
		fmt.Sprintf("warning: {DISC} ({UUID}): %s -> lost", state),
		"the tool stops trusting this disc",
	}
}

// lostUndoWarning is the warning of "disc lost --undo" for a disc that
// goes to after.
func lostUndoWarning(after stage.DiscState) []string {
	return []string{
		fmt.Sprintf("warning: {DISC} ({UUID}): lost -> %s", after),
		"the tool trusts this disc again only after a good check; you must run verify on it",
	}
}

// withQuestion is warning, then the question.
func withQuestion(warning []string) []string {
	return append(append([]string{}, warning...), confirmQuestion)
}

func init() {
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified} {
		registerStateCases(stateCase{
			row: "57", name: s.String() + " disc marked lost",
			start: s, args: []string{"disc", "lost", "{SEQ}"},
			stdin:  stdinYes,
			stderr: withQuestion(lostWarning(s)),
			stdout: []string{`{DISC}: marked lost; `, ` item(s) returned to staged`}, next: true,
			end: stage.DiscLost,
		})
	}
	registerStateCases(
		stateCase{
			row: "58", name: "on disc only disc marked lost",
			start: stage.DiscOnDiscOnly, args: []string{"disc", "lost", "{SEQ}"},
			stdin:  stdinYes,
			stderr: withQuestion(lostWarning(stage.DiscOnDiscOnly)),
			stdout: []string{`{DISC}: marked lost; `, ` item(s) need a new commit`}, next: true,
			end: stage.DiscLost, word: stage.WordLost,
		},
		stateCase{
			row: "59", name: "missing disc marked lost",
			start: stage.DiscMissing, args: []string{"disc", "lost", "{SEQ}"},
			stdin:  stdinYes,
			stderr: withQuestion(lostWarning(stage.DiscMissing)),
			stdout: []string{`{DISC}: marked lost; its items are not known; a new commit stages what the source still holds`}, next: true,
			end: stage.DiscLost,
		},
	)
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscMissing} {
		registerStateCases(
			stateCase{
				row: "59a", name: s.String() + " disc lost, answer no",
				start: s, args: []string{"disc", "lost", "{SEQ}"},
				stdin: stdinNo, exit: 1,
				stderr: withQuestion(lostWarning(s)),
				stdout: []string{"nothing changed"},
				absent: []string{"marked lost", "needs --force-yes"},
				end:    s,
			},
			stateCase{
				row: "66", name: "lost undo of a " + s.String() + " disc",
				start: s, args: []string{"disc", "lost", "--undo", "{SEQ}"},
				stdin: stdinYes, exit: 1,
				stderr: []string{"disc {SEQ} is not marked lost"},
				absent: []string{confirmQuestion, "warning:"},
				end:    s,
			},
		)
	}
	registerStateCases(
		stateCase{
			row: "59a", name: "disc lost with no terminal and no answer flag",
			start: stage.DiscBurned, args: []string{"disc", "lost", "{SEQ}"},
			exit:   1,
			stderr: lostWarning(stage.DiscBurned), absent: []string{confirmQuestion, "needs --force-yes"},
			stdout: []string{"nothing changed"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "60", name: "lost disc marked lost again",
			start: stage.DiscLost, args: []string{"--force-yes", "disc", "lost", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is already marked lost"},
			absent: []string{"warning:"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "61", name: "verified lost disc found, answer yes",
			start: stage.DiscLost, args: []string{"disc", "lost", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			stderr: withQuestion(lostUndoWarning(stage.DiscBurned)),
			stdout: []string{`{DISC}: lost mark removed; `, ` item(s) back on this disc; verify it now`}, next: true,
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "64", name: "verified lost disc found, answer no",
			start: stage.DiscLost, args: []string{"disc", "lost", "--undo", "{SEQ}"},
			stdin: stdinNo, exit: 1,
			stderr: withQuestion(lostUndoWarning(stage.DiscBurned)),
			stdout: []string{"nothing changed"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "80", name: "lost undo with --yes",
			start: stage.DiscLost, args: []string{"--yes", "disc", "lost", "--undo", "{SEQ}"},
			stderr: lostUndoWarning(stage.DiscBurned), absent: []string{confirmQuestion},
			stdout: []string{`{DISC}: lost mark removed; `}, next: true,
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "81", name: "lost undo with no terminal and no answer flag",
			start: stage.DiscLost, args: []string{"disc", "lost", "--undo", "{SEQ}"},
			exit:   1,
			stderr: lostUndoWarning(stage.DiscBurned), absent: []string{confirmQuestion},
			stdout: []string{"nothing changed"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "82", name: "disc lost with --force-yes",
			start: stage.DiscVerified, args: []string{"--force-yes", "disc", "lost", "{SEQ}"},
			stderr: lostWarning(stage.DiscVerified), absent: []string{confirmQuestion},
			stdout: []string{`{DISC}: marked lost; `}, next: true,
			end: stage.DiscLost,
		},
		stateCase{
			row: "83", name: "disc lost with --yes and no terminal",
			start: stage.DiscVerified, args: []string{"--yes", "disc", "lost", "{SEQ}"},
			exit:   1,
			stderr: lostWarning(stage.DiscVerified), absent: []string{confirmQuestion},
			stdout: []string{"nothing changed; disc lost needs --force-yes"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "84", name: "disc lost with --yes, answer yes",
			start: stage.DiscVerified, args: []string{"--yes", "disc", "lost", "{SEQ}"},
			stdin:  stdinYes,
			stderr: withQuestion(lostWarning(stage.DiscVerified)),
			stdout: []string{`{DISC}: marked lost; `}, next: true,
			end: stage.DiscLost,
		},
		stateCase{
			row: "84a", name: "disc lost with --yes, answer no",
			start: stage.DiscVerified, args: []string{"--yes", "disc", "lost", "{SEQ}"},
			stdin: stdinNo, exit: 1,
			stderr: withQuestion(lostWarning(stage.DiscVerified)),
			stdout: []string{"nothing changed"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
	)
}

// lostFromState returns a repository whose one disc was in state when
// disc lost marked it.
func lostFromState(t *testing.T, state stage.DiscState) *discFixture {
	t.Helper()
	fx := repoWithDisc(t, state)
	fx.mustRun(t, "--force-yes", "disc", "lost", fx.uuid)
	if got := discState(t, fx.repo, fx.uuid); got.State != stage.DiscLost || got.BeforeLost != state {
		t.Fatalf("disc state %s before lost %s, want lost before lost %s", got.State, got.BeforeLost, state)
	}
	return fx
}

// wantLines fails when out does not hold each of want, in order. out is
// standard output and then standard error.
func wantLines(t *testing.T, out string, want ...string) {
	t.Helper()
	wantInOrder(t, "output", out, want, func(s string) string { return s })
}

// TestDiscLostCounts checks the item count of rows 57 and 58, the item
// records, the removal of the plan directory, and that the catalog data
// of the disc stays.
func TestDiscLostCounts(t *testing.T) {
	t.Run("row 57", func(t *testing.T) {
		fx := repoWithDisc(t, stage.DiscVerified)
		n := countByState(t, fx.repo, stage.Packed)
		if n == 0 {
			t.Fatal("the fixture disc has no item")
		}
		layout := testLayout(t, fx.repo)
		u := fx.uuidBytes(t)
		out := fx.mustRun(t, "--force-yes", "disc", "lost", "0")
		wantLines(t, out, fmt.Sprintf("%s: marked lost; %d item(s) returned to staged\n", fx.name(), n), nextStatusLine)
		if got := countByState(t, fx.repo, stage.Staged); got != n {
			t.Errorf("%d staged items, want %d", got, n)
		}
		if _, err := os.Lstat(layout.planDir(u)); !os.IsNotExist(err) {
			t.Errorf("plan directory stays: %v", err)
		}
		c, err := catalog.Open(fx.repo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.IndexForDisc(u); err != nil {
			t.Errorf("catalog INDEX of the lost disc: %v", err)
		}
	})
	t.Run("row 58", func(t *testing.T) {
		fx := repoWithDisc(t, stage.DiscOnDiscOnly)
		n := countByState(t, fx.repo, stage.OnDisc)
		out := fx.mustRun(t, "--force-yes", "disc", "lost", "0")
		wantLines(t, out, fmt.Sprintf("%s: marked lost; %d item(s) need a new commit\n", fx.name(), n), nextStatusLine)
		if got := countByState(t, fx.repo, stage.Lost); got != n {
			t.Errorf("%d lost items, want %d", got, n)
		}
	})
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

// TestDiscLostUndoRows checks rows 61 to 63 and 65 with the state that
// the disc had when disc lost marked it.
func TestDiscLostUndoRows(t *testing.T) {
	t.Run("row 61", func(t *testing.T) {
		fx := repoWithDisc(t, stage.DiscVerified)
		n := countByState(t, fx.repo, stage.Packed)
		fx.mustRun(t, "--force-yes", "disc", "lost", "0")
		out := fx.mustRun(t, "--yes", "disc", "lost", "--undo", "0")
		wantLines(t, out, fmt.Sprintf("warning: %s (%s): lost -> burned\n", fx.name(), fx.uuid))
		wantLines(t, out,
			fmt.Sprintf("%s: lost mark removed; %d item(s) back on this disc; verify it now\n", fx.name(), n),
			nextStatusLine)
		d := discState(t, fx.repo, fx.uuid)
		if d.State != stage.DiscBurned || !d.VerifiedTime.IsZero() {
			t.Errorf("disc state %s, verified time %v, want burned with no verified time", d.State, d.VerifiedTime)
		}
		if words := itemWords(t, fx.repo, fx.uuid); words[stage.WordBurned] != n || len(words) != 1 {
			t.Errorf("item words %v, want %d burned", words, n)
		}
	})
	t.Run("row 61, a later pack took the items", func(t *testing.T) {
		fx := repoWithDisc(t, stage.DiscVerified)
		fx.mustRun(t, "--force-yes", "disc", "lost", "0")
		fx.mustRun(t, "pack", "--capacity=64MiB")
		out := fx.mustRun(t, "--yes", "disc", "lost", "--undo", "0")
		wantLines(t, out, fmt.Sprintf("%s: lost mark removed; 0 item(s) back on this disc; verify it now\n", fx.name()))
		if n := len(itemWords(t, fx.repo, fx.uuid)); n != 0 {
			t.Errorf("the found disc has %d item word(s), want none", n)
		}
	})
	t.Run("row 62", func(t *testing.T) {
		fx := lostFromState(t, stage.DiscOnDiscOnly)
		n := countByState(t, fx.repo, stage.Lost)
		out := fx.mustRun(t, "--yes", "disc", "lost", "--undo", "0")
		wantLines(t, out,
			fmt.Sprintf("warning: %s (%s): lost -> on disc only\n", fx.name(), fx.uuid),
			"the tool trusts this disc again only after a good check; you must run verify on it\n")
		wantLines(t, out,
			fmt.Sprintf("%s: lost mark removed; %d item(s) back on this disc; verify it now\n", fx.name(), n),
			nextStatusLine)
		if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscOnDiscOnly {
			t.Errorf("disc state %s, want on disc only", got)
		}
		if words := itemWords(t, fx.repo, fx.uuid); words[stage.WordOnDisc] != n || len(words) != 1 {
			t.Errorf("item words %v, want %d on-disc", words, n)
		}
	})
	t.Run("row 63", func(t *testing.T) {
		fx := lostFromState(t, stage.DiscMissing)
		out := fx.mustRun(t, "--yes", "disc", "lost", "--undo", fx.uuid)
		wantLines(t, out, fmt.Sprintf("warning: %s (%s): lost -> missing\n", fx.name(), fx.uuid))
		wantLines(t, out,
			fmt.Sprintf("%s: lost mark removed; give it to recover\n", fx.name()),
			nextStatusLine)
		if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscMissing {
			t.Errorf("disc state %s, want missing", got)
		}
	})
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned} {
		t.Run("row 65, "+s.String(), func(t *testing.T) {
			fx := lostFromState(t, s)
			code, out := fx.run(t, "--force-yes", "disc", "lost", "--undo", "0")
			if code != 1 {
				t.Fatalf("exit %d, want 1: %s", code, out)
			}
			wantLines(t, out, "disc 0 had no verified record when it was marked lost; its items are staged again; the lost mark stays\n")
			if strings.Contains(out, "warning:") || strings.Contains(out, nextStatusLine) {
				t.Errorf("output holds a warning or the next line: %s", out)
			}
			if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscLost {
				t.Errorf("disc state %s, want lost", got)
			}
		})
	}
}
