package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
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
// docs/states.md. The text of args, stdout, stderr and absent can hold
// these placeholders: {DISC} is `disc SEQ "LABEL"`, {SEQ}, {LABEL},
// {UUID}, {ROOT} is the disc root that the event reads, {SRC} is the
// source of the commit, {REPO} is the repository, {REF} is the ref name
// of a commit today, and each key that setup puts into the vars of the
// fixture.
type stateCase struct {
	// row is the row number of the table, for example "24a".
	row  string
	name string
	// start is the state of the one disc of the repository that
	// repoWithDisc builds.
	start stage.DiscState
	// setup changes the fixture after repoWithDisc and before the event.
	// It can damage the disc root, point {ROOT} to another disc root,
	// add a disc, set the fake clock, or set a placeholder.
	setup func(t *testing.T, fx *discFixture)
	// noRepo runs the event with no --repo, in an empty working
	// directory.
	noRepo bool
	// root runs the event as the user id 0. Else the user id is 1000.
	root  bool
	args  []string
	stdin stateStdin
	exit  int
	// stdout and stderr are texts that the output must hold, in order.
	stdout []string
	stderr []string
	// exact is true when standard output must be exactly the stdout
	// texts, then the next line when next is true.
	exact bool
	// absent are texts that neither output may hold.
	absent []string
	// next is true when the last line of standard output must be the
	// next line. When next is false, no output may hold the next line.
	next bool
	// noEvent is true when the event must not write the disc state log.
	noEvent bool
	// sameCatalog is true when the event must not add, remove or rename
	// a file of the catalog.
	sameCatalog bool
	// end is the disc state after the event.
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

// stateRowRe matches a row of the state x event table and captures the
// row number.
var stateRowRe = regexp.MustCompile(`^\| (\d+[a-z]?) \|`)

// stateTableRows returns the row numbers of the state x event table in
// docs/states.md.
func stateTableRows(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open("../../docs/states.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := stateRowRe.FindStringSubmatch(sc.Text()); m != nil {
			rows[m[1]] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("docs/states.md holds no row of the state x event table")
	}
	return rows
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
// has no registered case, and when a case names a row that the table
// does not have.
func TestStatesTableIsComplete(t *testing.T) {
	rows := stateTableRows(t)
	covered := map[string]bool{}
	unknown := map[string]bool{}
	for _, c := range stateCases {
		covered[c.row] = true
		if !rows[c.row] {
			unknown[c.row] = true
		}
	}
	var missing []string
	for _, id := range sortedRows(rows) {
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
}

// TestStatesTable runs each registered case: it builds the repository
// with the start state, runs the event, and checks the exit code, the
// messages, the next line, the disc state and the item words.
func TestStatesTable(t *testing.T) {
	rows := stateTableRows(t)
	for _, c := range stateCases {
		t.Run("row "+c.row+"/"+c.name, func(t *testing.T) {
			if !rows[c.row] {
				t.Fatalf("docs/states.md has no row %s", c.row)
			}
			runStateCase(t, c)
		})
	}
}

// readSpy is a standard input that records a read.
type readSpy struct{ read bool }

func (r *readSpy) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

// runStateCase runs one state case.
func runStateCase(t *testing.T, c stateCase) {
	t.Helper()
	fx := repoWithDisc(t, c.start)
	if c.setup != nil {
		c.setup(t, fx)
	}
	fill := fx.filler()

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
	if code != c.exit {
		t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, c.exit, stdout, stderr)
	}
	if spy.read {
		t.Error("the command read standard input with no terminal")
	}
	wantInOrder(t, "stdout", stdout, c.stdout, fill)
	wantInOrder(t, "stderr", stderr, c.stderr, fill)
	if c.exact {
		var want strings.Builder
		for _, s := range c.stdout {
			want.WriteString(fill(s))
		}
		if c.next {
			want.WriteString(nextStatusLine + "\n")
		}
		if stdout != want.String() {
			t.Errorf("stdout %q, want exactly %q\nstderr: %s", stdout, want.String(), stderr)
		}
	}
	for _, a := range c.absent {
		if text := fill(a); strings.Contains(stdout, text) || strings.Contains(stderr, text) {
			t.Errorf("output holds %q\nstdout: %s\nstderr: %s", text, stdout, stderr)
		}
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if c.next {
		if last := lines[len(lines)-1]; last != nextStatusLine {
			t.Errorf("last stdout line %q, want %q", last, nextStatusLine)
		}
	} else if strings.Contains(stdout+stderr, nextStatusLine) {
		t.Errorf("output holds the next line, want none\nstdout: %s\nstderr: %s", stdout, stderr)
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

// stderrIs is a check: standard error is exactly text.
func stderrIs(text string) func(*testing.T, *discFixture, string, string) {
	return func(t *testing.T, fx *discFixture, _, stderr string) {
		t.Helper()
		if want := fx.filler()(text); stderr != want {
			t.Errorf("stderr %q, want %q", stderr, want)
		}
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
			exit: 1, stderr: []string{`{DISC} is missing`},
			end: stage.DiscMissing,
		},
		stateCase{
			row: "10", name: "pack refused while a disc is missing",
			start: stage.DiscMissing, args: []string{"pack", "--capacity=64MiB"},
			exit: 1, stderr: []string{`{DISC} is missing`},
			end: stage.DiscMissing,
		},
	)
}
