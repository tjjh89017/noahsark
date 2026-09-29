package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// stateStdin is the standard input of a state case.
type stateStdin int

const (
	// stdinNone is no terminal. The command must not read it.
	stdinNone stateStdin = iota
	// stdinYes is a terminal where the operator types "y".
	stdinYes
	// stdinNo is a terminal where the operator types "n".
	stdinNo
)

// stateCase is one case of a row of the state x event table in
// docs/states.md. The harness takes the exit code, the lines of the
// output, the next line and the disc state after the event from the
// cells of the row. The text of args, also and absent can hold these
// placeholders: {DISC} is `disc SEQ "LABEL"`, {SEQ}, {LABEL}, {UUID},
// {ROOT} is the disc root that the event reads, {SRC} is the source of
// the commit, {REPO} is the repository, {REF} is the ref name of a
// commit today, and each key that setup puts into the vars of the
// fixture.
type stateCase struct {
	// row is the row number of the table, for example "24a".
	row  string
	name string
	// like is the answer-yes row whose warning and message a row of the
	// answer rules names, for example "24" for a case of row 80.
	like string
	// start is the state of the one disc of the repository that
	// repoWithDisc builds.
	start stage.DiscState
	// setup changes the fixture after repoWithDisc and before the event.
	// It can damage the disc root, point {ROOT} to another disc root,
	// add a disc, set the fake clock, set a placeholder, or give the
	// value of a placeholder of the cells with fx.cell.
	setup func(t *testing.T, fx *discFixture)
	// cells gives the values of placeholders of the cells, for example
	// "N": "1".
	cells map[string]string
	// noRepo runs the event with no --repo, in an empty working
	// directory.
	noRepo bool
	// root runs the event as the user id 0. Else the user id is 1000.
	root  bool
	args  []string
	stdin stateStdin
	// omit are spans of the Message cell that this case does not print,
	// as the cell writes them. The output must not hold them. An omitted
	// span of an alternative drops that alternative.
	omit []string
	// also are texts that the output, standard output and then standard
	// error, must hold in order, beyond the lines of the cells.
	also []string
	// exact is true when each line of standard output is a line of the
	// cell or the next line. exactStderr is the same for standard error.
	exact       bool
	exactStderr bool
	// absent are texts that neither output may hold.
	absent []string
	// noEvent is true when the event must not write the disc state log.
	noEvent bool
	// sameCatalog is true when the event must not add, remove or rename
	// a file of the catalog.
	sameCatalog bool
	// subject returns the uuid of the disc whose state the Result cell
	// names. nil names the disc of the fixture.
	subject func(t *testing.T, fx *discFixture, stdout string) string
	// end is the disc state of the disc of the fixture after the event.
	end stage.DiscState
	// word is the item word of every item of the disc after the event.
	// An empty word skips the check.
	word stage.ItemWord
	// check makes the checks of the row that the other fields cannot
	// express. It gets the output of the event.
	check func(t *testing.T, fx *discFixture, stdout, stderr string)
}

// stateCases holds the cases that the init functions of the
// states_rows_*_test.go files register.
var stateCases []stateCase

// registerStateCases adds cs to the cases that TestStatesTable runs.
func registerStateCases(cs ...stateCase) {
	stateCases = append(stateCases, cs...)
}

// sortedRows returns the keys of rows in the order of the row number,
// then the letter suffix.
func sortedRows(rows map[string]bool) []string {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	number := func(id string) int {
		n, _ := strconv.Atoi(strings.TrimRight(id, "abcdefghijklmnopqrstuvwxyz"))
		return n
	}
	sort.Slice(ids, func(i, j int) bool {
		if a, b := number(ids[i]), number(ids[j]); a != b {
			return a < b
		}
		return ids[i] < ids[j]
	})
	return ids
}

// TestStatesTableIsComplete fails when a row of the state x event table
// has no registered case, when a case names a row that the table does
// not have, and when knownDisagreements names no case.
func TestStatesTableIsComplete(t *testing.T) {
	tb := loadStateTable(t)
	covered := map[string]bool{}
	unknown := map[string]bool{}
	for _, c := range stateCases {
		covered[c.row] = true
		if tb.rows[c.row] == nil {
			unknown[c.row] = true
		}
	}
	var missing []string
	for _, id := range tb.ids {
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		t.Errorf("rows of docs/states.md with no registered case: %s", strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		t.Errorf("registered cases name rows that docs/states.md does not have: %s", strings.Join(sortedRows(unknown), ", "))
	}
	for key := range knownDisagreements {
		row, name, _ := strings.Cut(key, "/")
		if !slices.ContainsFunc(stateCases, func(c stateCase) bool { return c.row == row && (name == "" || c.name == name) }) {
			t.Errorf("knownDisagreements names %q, which is no case", key)
		}
	}
}

// TestStatesTable runs each registered case: it builds the repository
// with the start state, runs the event, and compares the exit code, the
// messages, the next line and the disc state with the cells of the row.
// It also makes the extra checks of the case.
func TestStatesTable(t *testing.T) {
	tb := loadStateTable(t)
	for _, c := range stateCases {
		t.Run("row "+c.row+"/"+c.name, func(t *testing.T) {
			if tb.rows[c.row] == nil {
				t.Fatalf("docs/states.md has no row %s", c.row)
			}
			runStateCase(t, tb, c)
		})
	}
}

// readSpy is a standard input that records a read.
type readSpy struct{ read bool }

func (r *readSpy) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

// runStateCase runs one state case and compares it with the cells of
// its row.
func runStateCase(t *testing.T, tb *stateTable, c stateCase) {
	t.Helper()
	fx := repoWithDisc(t, c.start)
	for k, v := range c.cells {
		fx.cell(k, v)
	}
	if c.setup != nil {
		c.setup(t, fx)
	}
	fill := fx.filler()
	before := discStateIfRepo(t, fx.repo, fx.uuid)

	spy := &readSpy{}
	switch c.stdin {
	case stdinNone:
		setFakeStdin(t, spy)
	case stdinYes:
		setFakeTerminal(t, "y\n")
	case stdinNo:
		setFakeTerminal(t, "n\n")
	}

	var args []string
	if !c.noRepo {
		args = append(args, "--repo="+fx.repo)
	}
	for _, a := range c.args {
		args = append(args, fill(a))
	}
	var logBefore []byte
	if c.noEvent {
		logBefore = discLogBytes(t, fx.repo)
	}
	var catalogBefore []string
	if c.sameCatalog {
		catalogBefore = listFilesUnder(t, testLayout(t, fx.repo).catalogDir())
	}

	te := newTestEnv(t.TempDir())
	uid := 1000
	if c.root {
		uid = 0
	}
	te.euid = func() int { return uid }
	code, _ := te.run(args...)
	stdout, stderr := te.out.String(), te.errOut.String()

	cellErrs := compareWithCells(t, tb, c, fx, before, code, stdout, stderr)
	key := c.row + "/" + c.name
	reason, known := knownDisagreements[key]
	if !known {
		reason, known = knownDisagreements[c.row]
	}
	switch {
	case known && len(cellErrs) == 0:
		t.Errorf("knownDisagreements names %s (%s), but the case agrees with the cells", key, reason)
	case known:
		for _, e := range cellErrs {
			t.Logf("known disagreement of row %s (%s): %s", c.row, reason, e)
		}
	case len(cellErrs) > 0:
		for _, e := range cellErrs {
			t.Errorf("row %s: %s", c.row, e)
		}
		t.Logf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
	}

	if spy.read {
		t.Error("the command read standard input with no terminal")
	}
	wantInOrder(t, "output", stdout+stderr, c.also, fill)
	for _, a := range c.absent {
		if text := fill(a); strings.Contains(stdout, text) || strings.Contains(stderr, text) {
			t.Errorf("output holds %q\nstdout: %s\nstderr: %s", text, stdout, stderr)
		}
	}
	if c.noEvent && !bytes.Equal(discLogBytes(t, fx.repo), logBefore) {
		t.Error("the event wrote the disc state log")
	}
	if c.sameCatalog && !slices.Equal(listFilesUnder(t, testLayout(t, fx.repo).catalogDir()), catalogBefore) {
		t.Error("the event changed the files of the catalog")
	}

	if got := discState(t, fx.repo, fx.uuid).State; got != c.end {
		t.Errorf("disc state %s, want %s", got, c.end)
	}
	if c.word != "" {
		words := itemWords(t, fx.repo, fx.uuid)
		if len(words) != 1 || words[c.word] == 0 {
			t.Errorf("item words %v, want every item %s", words, c.word)
		}
	}
	if c.check != nil {
		c.check(t, fx, stdout, stderr)
	}
}

// compareWithCells compares the exit code, the output, the next line
// and the disc state after the event with the cells of the row of c.
// before is the state of the disc of the fixture before the event. It
// returns the differences.
func compareWithCells(t *testing.T, tb *stateTable, c stateCase, fx *discFixture, before stage.DiscState, code int, stdout, stderr string) []string {
	t.Helper()
	var diffs []string
	diff := func(format string, a ...any) { diffs = append(diffs, fmt.Sprintf(format, a...)) }

	want, err := tb.exitCode(c.row)
	if err != nil {
		t.Fatal(err)
	}
	if code != want {
		diff("Exit: exit %d, the cell says %d", code, want)
	}

	values := map[string]string{
		"SEQ":   strconv.FormatUint(fx.seq, 10),
		"LABEL": fx.label,
		"UUID":  fx.uuid,
		"REPO":  fx.repo,
	}
	if before != stage.DiscUnknown {
		values["STATE"] = before.String()
	}
	maps.Copy(values, fx.cells)
	out, errOut := newOutputLines(stdout), newOutputLines(stderr)
	msg, err := tb.message(c.row, c.like)
	switch {
	case errors.Is(err, errIrregular) && irregularMessageRows[c.row] != "":
		t.Logf("row %s: no Message check: %s", c.row, irregularMessageRows[c.row])
	case err != nil:
		t.Fatalf("row %s: Message: %v", c.row, err)
	default:
		spans := tb.spansOf(c.row)
		for _, o := range c.omit {
			if !slices.Contains(spans, o) {
				t.Fatalf("omit names %q, which is not a span of the Message cell of row %s", o, c.row)
			}
		}
		mc := messageCheck{msg: msg, values: values, flags: c.args, omit: c.omit}
		for _, p := range mc.run(out, errOut) {
			diff("Message: %s", p)
		}
	}

	// The document prints no next line for a --dry-run run.
	if msg.next && !slices.Contains(c.args, "--dry-run") {
		if n := len(out.lines); n == 0 || out.lines[n-1] != nextStatusLine {
			diff("Message: the last line of stdout is not %q", nextStatusLine)
		} else {
			out.used[n-1] = true
		}
	} else if strings.Contains(stdout+stderr, nextStatusLine) {
		diff("Message: the output holds %q, and the cell does not name it", nextStatusLine)
	}
	if c.exact {
		for i, used := range out.used {
			if !used {
				diff("Message: stdout line %q is not a line of the cell", out.lines[i])
			}
		}
	}
	if c.exactStderr {
		for i, used := range errOut.used {
			if !used {
				diff("Message: stderr line %q is not a line of the cell", errOut.lines[i])
			}
		}
	}

	kind, wantState, err := tb.result(c.row, c.like)
	if err != nil {
		t.Fatalf("row %s: Result: %v", c.row, err)
	}
	subject := fx.uuid
	if c.subject != nil {
		subject = c.subject(t, fx, stdout)
	}
	switch got := discState(t, fx.repo, subject).State; {
	case kind == resultUnchanged && subject == fx.uuid && got != before:
		diff("Result: disc state %s, the cell says unchanged from %s", got, before)
	case kind == resultState && got != wantState:
		diff("Result: disc state %s, the cell says %s", got, wantState)
	}
	return diffs
}

// discStateIfRepo returns the state of the disc uuidText in repo, or
// unknown when repo has no config.
func discStateIfRepo(t *testing.T, repo, uuidText string) stage.DiscState {
	t.Helper()
	if _, err := os.Stat(configPath(repo)); err != nil {
		return stage.DiscUnknown
	}
	return discState(t, repo, uuidText).State
}

// wantInOrder checks that out holds each of want, after fill, in order.
func wantInOrder(t *testing.T, name, out string, want []string, fill func(string) string) {
	t.Helper()
	rest := out
	for _, w := range want {
		text := fill(w)
		i := strings.Index(rest, text)
		if i < 0 {
			t.Errorf("%s %q does not hold %q after the earlier texts", name, out, text)
			return
		}
		rest = rest[i+len(text):]
	}
}

// lastCheckIs is a check: the last check of the disc is r.
func lastCheckIs(r stage.CheckResult) func(*testing.T, *discFixture, string, string) {
	return func(t *testing.T, fx *discFixture, _, _ string) {
		t.Helper()
		if got := discState(t, fx.repo, fx.uuid).LastCheck; got != r {
			t.Errorf("last check %d, want %d", got, r)
		}
	}
}

// allChecks is a check that runs each of checks.
func allChecks(checks ...func(*testing.T, *discFixture, string, string)) func(*testing.T, *discFixture, string, string) {
	return func(t *testing.T, fx *discFixture, stdout, stderr string) {
		t.Helper()
		for _, c := range checks {
			c(t, fx, stdout, stderr)
		}
	}
}

func init() {
	// Rows 2 and 10: a missing disc refuses commit and pack.
	registerStateCases(
		stateCase{
			row: "2", name: "commit refused while a disc is missing",
			start: stage.DiscMissing, args: []string{"commit", "{SRC}"},
			end: stage.DiscMissing,
		},
		stateCase{
			row: "10", name: "pack refused while a disc is missing",
			start: stage.DiscMissing, args: []string{"pack", "--capacity=64MiB"},
			end: stage.DiscMissing,
		},
	)
}
