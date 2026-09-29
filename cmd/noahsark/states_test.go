package main

import (
	"bufio"
	"io"
	"os"
	"regexp"
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
// {UUID}, {ROOT} is the copy of the disc root, and {SRC} is the source
// of the commit.
type stateCase struct {
	// row is the row number of the table, for example "24a".
	row  string
	name string
	// start is the state of the one disc of the repository before the
	// event.
	start stage.DiscState
	args  []string
	stdin stateStdin
	exit  int
	// stdout and stderr are texts that the output must hold, in order.
	stdout []string
	stderr []string
	// absent are texts that neither output may hold.
	absent []string
	// next is true when the last line of standard output must be the
	// next line. When next is false, no output may hold the next line.
	next bool
	// end is the disc state after the event.
	end stage.DiscState
	// word is the item word of every item of the disc after the event.
	// An empty word skips the check.
	word stage.ItemWord
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

// TestStatesTableRegistryNamesRealRows fails when a case names a row
// that docs/states.md does not have.
func TestStatesTableRegistryNamesRealRows(t *testing.T) {
	rows := stateTableRows(t)
	if len(stateCases) == 0 {
		t.Fatal("no state case is registered")
	}
	for _, c := range stateCases {
		if !rows[c.row] {
			t.Errorf("case %q names row %s, which docs/states.md does not have", c.name, c.row)
		}
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
	fill := strings.NewReplacer(
		"{DISC}", fx.name(),
		"{SEQ}", strconv.FormatUint(fx.seq, 10),
		"{LABEL}", fx.label,
		"{UUID}", fx.uuid,
		"{ROOT}", fx.root,
		"{SRC}", fx.src,
	).Replace

	spy := &readSpy{}
	switch c.stdin {
	case stdinNone:
		setFakeStdin(t, spy)
	case stdinYes:
		setFakeTerminal(t, "y\n")
	case stdinNo:
		setFakeTerminal(t, "n\n")
	}

	args := []string{"--repo=" + fx.repo}
	for _, a := range c.args {
		args = append(args, fill(a))
	}
	te := newTestEnv(t.TempDir())
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

	if got := discState(t, fx.repo, fx.uuid).State; got != c.end {
		t.Errorf("disc state %s, want %s", got, c.end)
	}
	if c.word != "" {
		words := itemWords(t, fx.repo, fx.uuid)
		if len(words) != 1 || words[c.word] == 0 {
			t.Errorf("item words %v, want every item %s", words, c.word)
		}
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
