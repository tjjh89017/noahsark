package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// statesDoc is the document that holds the state x event table. A run
// can point it to a changed copy: go test -run TestStates -args
// -states=FILE.
var statesDoc = flag.String("states", "../../docs/states.md", "the document that holds the state x event table")

// irregularMessageRows are the rows whose Message cell the parser cannot
// turn into lines. The harness skips the Message check of these rows and
// logs the reason. The parse test fails when a listed row parses, so
// that the entry goes when the cell is fixed.
var irregularMessageRows = map[string]string{}

// knownDisagreements are the cases, as "ROW" or "ROW/NAME", whose
// output disagrees with the cells of their row. The harness logs each
// difference of a listed case, and fails when a listed case agrees.
var knownDisagreements = map[string]string{}

// stateRow is one row of the state x event table, one text for each
// column.
type stateRow struct {
	id, state, event, result, message, exit, next string
}

// stateTable is the state x event table of statesDoc.
type stateTable struct {
	rows map[string]*stateRow
	ids  []string
}

// stateRowRe matches a row of the state x event table and captures the
// row number.
var stateRowRe = regexp.MustCompile(`^\| (\d+[a-z]?) \|`)

// splitTableLine returns the cells of a markdown table line. A `\|`
// is a literal bar.
func splitTableLine(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// loadStateTable reads the state x event table of statesDoc. It finds
// each column by the name in the header line of the table.
func loadStateTable(t *testing.T) *stateTable {
	t.Helper()
	f, err := os.Open(*statesDoc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	tb := &stateTable{rows: map[string]*stateRow{}}
	var col map[string]int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "| # |") {
			col = map[string]int{}
			for i, name := range splitTableLine(line) {
				col[name] = i
			}
			for _, name := range []string{"#", "State", "Event", "Result", "Message", "Exit", "Next"} {
				if _, ok := col[name]; !ok {
					t.Fatalf("%s: the state x event table has no column %s", *statesDoc, name)
				}
			}
			continue
		}
		m := stateRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if col == nil {
			t.Fatalf("%s: row %s comes before the header of the table", *statesDoc, m[1])
		}
		cells := splitTableLine(line)
		if len(cells) != len(col) {
			t.Fatalf("%s: row %s has %d cells, want %d", *statesDoc, m[1], len(cells), len(col))
		}
		if _, dup := tb.rows[m[1]]; dup {
			t.Fatalf("%s: row %s is in the table twice", *statesDoc, m[1])
		}
		tb.rows[m[1]] = &stateRow{
			id: m[1], state: cells[col["State"]], event: cells[col["Event"]],
			result: cells[col["Result"]], message: cells[col["Message"]],
			exit: cells[col["Exit"]], next: cells[col["Next"]],
		}
		tb.ids = append(tb.ids, m[1])
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tb.rows) == 0 {
		t.Fatalf("%s holds no row of the state x event table", *statesDoc)
	}
	return tb
}

// cellLine is one line that a Message cell names.
type cellLine struct {
	// text is the span of the cell, after a "with" of a reference.
	text string
	// span is the span as the cell writes it.
	span string
	// question is true for the question of a confirmation.
	question bool
	// coveredBy is the answer flag that answers the question, so that
	// the command does not ask: "--yes" for an ordinary confirmation
	// (--force-yes also answers it), "--force-yes" for a critical one.
	// An empty text means that the command always asks.
	coveredBy string
}

// cellStep is a set of alternatives. The output must hold the lines of
// one alternative, in order.
type cellStep [][]cellLine

// cellMessage is the parsed Message cell of a row.
type cellMessage struct {
	steps []cellStep
	// noQuestion is true when the output must not hold the question.
	noQuestion bool
	// next is true when the cell names the next line as the last line.
	next bool
}

// errNeedsAnswerRow is the error of a Message cell that names "the
// warning" or "the message of the answer-yes row" when no answer-yes
// row is given.
var errNeedsAnswerRow = errors.New("the cell names the answer-yes row; the case must give it")

// errIrregular is the error of a Message cell that the parser cannot
// turn into lines.
var errIrregular = errors.New("irregular Message cell")

const (
	spanOpen  = "\x01"
	spanClose = "\x02"
)

var (
	spanRe       = regexp.MustCompile("`([^`]*)`")
	maskedSpanRe = regexp.MustCompile(spanOpen + `(\d+)` + spanClose)
	nextMarkRe   = regexp.MustCompile(`(?:,?\s*then\s+|;\s*)?the last line(?: is)? ` + spanOpen + `(\d+)` + spanClose)
	parenNoteRe  = regexp.MustCompile(`\s*\(([^()]*)\)`)
	noQuestionRe = regexp.MustCompile(`^no question with (?:an answer flag|` + spanOpen + `(\d+)` + spanClose + `)$`)
	sentenceRe   = regexp.MustCompile(`\.(?:\s|$)`)
	thenRe       = regexp.MustCompile(`,?\s*\bthen\b,?\s*`)
	rowListRe    = regexp.MustCompile(`\d+[a-z]?`)
	listSepRe    = regexp.MustCompile(`^(?:,\s*|,\s*or\s+|\s+or\s+)$`)
)

const rowList = `(\d+[a-z]?(?:(?:,\s*or\s+|,\s*|\s+or\s+|\s+and\s+)\d+[a-z]?)*)`

var (
	warnQuestionOfRe = regexp.MustCompile(`^the warning and the question of rows? ` + rowList + `(?:, with ` + spanOpen + `(\d+)` + spanClose + `)?$`)
	asRowsRe         = regexp.MustCompile(`^as rows? ` + rowList + `(?:, with the reason ` + spanOpen + `(\d+)` + spanClose + `)?$`)
)

// confirmQuestionSpan is the question as the cells write it.
const confirmQuestionSpan = "Continue? [y/N]"

// message parses the Message cell of row id. like is the answer-yes row
// that "the warning" and "the message of the answer-yes row" name.
func (tb *stateTable) message(id, like string) (cellMessage, error) {
	return tb.messageDepth(id, like, 0)
}

func (tb *stateTable) messageDepth(id, like string, depth int) (cellMessage, error) {
	var msg cellMessage
	if depth > 4 {
		return msg, fmt.Errorf("row %s: the references of the Message cell loop", id)
	}
	row, ok := tb.rows[id]
	if !ok {
		return msg, fmt.Errorf("the table has no row %s", id)
	}
	var spans []string
	masked := spanRe.ReplaceAllStringFunc(row.message, func(s string) string {
		spans = append(spans, s[1:len(s)-1])
		return spanOpen + strconv.Itoa(len(spans)-1) + spanClose
	})
	span := func(index string) string {
		i, _ := strconv.Atoi(index)
		return spans[i]
	}

	masked = nextMarkRe.ReplaceAllStringFunc(masked, func(s string) string {
		if span(nextMarkRe.FindStringSubmatch(s)[1]) == nextStatusLine {
			msg.next = true
			return ""
		}
		return s
	})
	coveredBy := ""
	masked = parenNoteRe.ReplaceAllStringFunc(masked, func(s string) string {
		note := parenNoteRe.FindStringSubmatch(s)[1]
		if m := noQuestionRe.FindStringSubmatch(note); m != nil {
			coveredBy = "--yes"
			if m[1] != "" {
				coveredBy = span(m[1])
			}
		}
		return ""
	})
	if loc := sentenceRe.FindStringIndex(masked); loc != nil {
		masked = masked[:loc[0]]
	}

	lineOf := func(text string) cellLine {
		if text == confirmQuestionSpan {
			return cellLine{text: text, span: text, question: true, coveredBy: coveredBy}
		}
		return cellLine{text: text, span: text}
	}
	for _, clause := range thenRe.Split(masked, -1) {
		clause = strings.Trim(clause, " ,;:")
		if clause == "" {
			continue
		}
		switch {
		case clause == "the warning and the question":
			if like == "" {
				return msg, errNeedsAnswerRow
			}
			warning, _, _, err := tb.answerParts(like, depth)
			if err != nil {
				return msg, err
			}
			msg.steps = append(msg.steps, warning...)
			msg.steps = append(msg.steps, cellStep{{lineOf(confirmQuestionSpan)}})
		case clause == "the warning":
			if like == "" {
				return msg, errNeedsAnswerRow
			}
			warning, _, _, err := tb.answerParts(like, depth)
			if err != nil {
				return msg, err
			}
			msg.steps = append(msg.steps, warning...)
		case clause == "the warning, no question":
			if like == "" {
				return msg, errNeedsAnswerRow
			}
			warning, _, _, err := tb.answerParts(like, depth)
			if err != nil {
				return msg, err
			}
			msg.steps = append(msg.steps, warning...)
			msg.noQuestion = true
		case clause == "the message of the answer-yes row":
			if like == "" {
				return msg, errNeedsAnswerRow
			}
			_, after, next, err := tb.answerParts(like, depth)
			if err != nil {
				return msg, err
			}
			msg.steps = append(msg.steps, after...)
			msg.next = msg.next || next
		case warnQuestionOfRe.MatchString(clause):
			m := warnQuestionOfRe.FindStringSubmatch(clause)
			var step cellStep
			for _, ref := range rowListRe.FindAllString(m[1], -1) {
				warning, question, err := tb.warningAndQuestion(ref, depth)
				if err != nil {
					return msg, err
				}
				alt := append(flatten(warning), question)
				if m[2] != "" {
					if alt, err = withTransition(alt, span(m[2])); err != nil {
						return msg, fmt.Errorf("row %s: %w", id, err)
					}
				}
				step = append(step, alt)
			}
			msg.steps = append(msg.steps, step)
		case asRowsRe.MatchString(clause):
			m := asRowsRe.FindStringSubmatch(clause)
			var step cellStep
			for _, ref := range rowListRe.FindAllString(m[1], -1) {
				other, err := tb.messageDepth(ref, like, depth+1)
				if err != nil {
					return msg, err
				}
				alt := flatten(other.steps)
				if m[2] != "" {
					if alt, err = withReason(alt, span(m[2])); err != nil {
						return msg, fmt.Errorf("row %s: %w", id, err)
					}
				}
				step = append(step, alt)
			}
			msg.steps = append(msg.steps, step)
		default:
			step, err := clauseLines(clause, span, lineOf)
			if err != nil {
				return msg, fmt.Errorf("row %s: %w: %q", id, err, clause)
			}
			if step != nil {
				msg.steps = append(msg.steps, step)
			}
		}
	}
	return msg, nil
}

// clauseLines returns the step of a clause that holds no reference. One
// span is one line, and the prose around it describes it. Spans with
// only ", " between them are lines in order. Spans with "or" between
// them are alternatives.
func clauseLines(clause string, span func(string) string, lineOf func(string) cellLine) (cellStep, error) {
	locs := maskedSpanRe.FindAllStringSubmatchIndex(clause, -1)
	switch len(locs) {
	case 0:
		return nil, nil
	case 1:
		return cellStep{{lineOf(span(clause[locs[0][2]:locs[0][3]]))}}, nil
	}
	alternatives := false
	for i := 1; i < len(locs); i++ {
		sep := clause[locs[i-1][1]:locs[i][0]]
		if !listSepRe.MatchString(sep) {
			return nil, errIrregular
		}
		alternatives = alternatives || strings.Contains(sep, "or")
	}
	var lines []cellLine
	for _, l := range locs {
		lines = append(lines, lineOf(span(clause[l[2]:l[3]])))
	}
	if !alternatives {
		return cellStep{lines}, nil
	}
	step := make(cellStep, len(lines))
	for i, l := range lines {
		step[i] = []cellLine{l}
	}
	return step, nil
}

// flatten joins steps with one alternative each into one list of lines.
// A step with alternatives keeps its first alternative only; the rows
// that a reference names have none.
func flatten(steps []cellStep) []cellLine {
	var lines []cellLine
	for _, s := range steps {
		lines = append(lines, s[0]...)
	}
	return lines
}

// warningAndQuestion returns the lines of row id before its question,
// and the question.
func (tb *stateTable) warningAndQuestion(id string, depth int) ([]cellStep, cellLine, error) {
	msg, err := tb.messageDepth(id, "", depth+1)
	if err != nil {
		return nil, cellLine{}, err
	}
	warning, question, _, ok := splitAtQuestion(msg.steps)
	if !ok {
		return nil, cellLine{}, fmt.Errorf("row %s has no question", id)
	}
	return warning, question, nil
}

// answerParts returns the warning of the answer-yes row id, the steps
// after its question, and whether it names the next line.
func (tb *stateTable) answerParts(id string, depth int) (warning, after []cellStep, next bool, err error) {
	msg, err := tb.messageDepth(id, "", depth+1)
	if err != nil {
		return nil, nil, false, err
	}
	warning, _, after, ok := splitAtQuestion(msg.steps)
	if !ok {
		return nil, nil, false, fmt.Errorf("row %s has no question", id)
	}
	return warning, after, msg.next, nil
}

// splitAtQuestion returns the steps before the question, the question,
// and the steps after it. The question must be in a step with one
// alternative.
func splitAtQuestion(steps []cellStep) (before []cellStep, question cellLine, after []cellStep, ok bool) {
	for i, s := range steps {
		if len(s) != 1 {
			continue
		}
		for j, l := range s[0] {
			if !l.question {
				continue
			}
			before = append(slices.Clone(steps[:i]), cellStep{s[0][:j]})
			if rest := s[0][j+1:]; len(rest) > 0 {
				after = append(after, cellStep{rest})
			}
			return before, l, append(after, steps[i+1:]...), true
		}
	}
	return nil, cellLine{}, nil, false
}

// withTransition replaces the transition "A -> B" of the warning with
// the transition that the cell gives.
func withTransition(lines []cellLine, transition string) ([]cellLine, error) {
	from, _, ok := strings.Cut(transition, " -> ")
	if !ok {
		return nil, fmt.Errorf("%q is not a transition", transition)
	}
	out := slices.Clone(lines)
	for i, l := range out {
		if j := strings.Index(l.text, from+" -> "); j >= 0 {
			out[i].text = l.text[:j] + transition
			return out, nil
		}
	}
	return nil, fmt.Errorf("no line holds the transition %q", from+" -> ")
}

// withReason puts reason in place of the placeholder REASON.
func withReason(lines []cellLine, reason string) ([]cellLine, error) {
	out := slices.Clone(lines)
	done := false
	for i, l := range out {
		if strings.Contains(l.text, "REASON") {
			out[i].text = strings.ReplaceAll(l.text, "REASON", reason)
			done = true
		}
	}
	if !done {
		return nil, errors.New("no line holds REASON")
	}
	return out, nil
}

// resultKind is what the Result cell says about the disc of the event.
type resultKind int

const (
	// resultNone names no disc state.
	resultNone resultKind = iota
	// resultUnchanged keeps the disc state: "refused" or "unchanged".
	resultUnchanged
	// resultState names the disc state after the event.
	resultState
)

// resultWords are the disc states, the longest word first.
var resultWords = []stage.DiscState{
	stage.DiscOnDiscOnly, stage.DiscVerified, stage.DiscMissing,
	stage.DiscPacked, stage.DiscBurned, stage.DiscUndone, stage.DiscLost,
}

// result parses the Result cell of row id. like is the answer-yes row
// that "as the answer-yes row" names.
func (tb *stateTable) result(id, like string) (resultKind, stage.DiscState, error) {
	text := tb.rows[id].result
	if strings.HasPrefix(text, "as the answer-yes row") {
		if like == "" {
			return resultNone, 0, errNeedsAnswerRow
		}
		return tb.result(like, "")
	}
	if strings.HasPrefix(text, "refused") || strings.HasPrefix(text, "unchanged") {
		return resultUnchanged, 0, nil
	}
	for _, s := range resultWords {
		word := s.String()
		if rest, ok := strings.CutPrefix(text, word); ok && (rest == "" || !isLetter(rest[0])) {
			return resultState, s, nil
		}
	}
	return resultNone, 0, nil
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// exitCode parses the Exit cell of row id.
func (tb *stateTable) exitCode(id string) (int, error) {
	n, err := strconv.Atoi(tb.rows[id].exit)
	if err != nil {
		return 0, fmt.Errorf("row %s: Exit cell %q is not a number", id, tb.rows[id].exit)
	}
	return n, nil
}

// placeholderPatterns match each placeholder of a Message cell by its
// kind.
var placeholderPatterns = map[string]string{
	"SEQ": `\d+`, "N": `\d+`, "B": `\d+`, "I": `\d+`, "D": `\d+`,
	"LABEL":        `[^"]*`,
	"UUID":         uuidPattern,
	"RUUID":        uuidPattern,
	"DATE":         `\d{4}-\d{2}-\d{2}`,
	"FILE":         `\S+`,
	"DIR":          `\S+`,
	"DEST":         `\S+`,
	"PATH":         `\S+`,
	"REPO":         `\S+`,
	"ID":           `\S+`,
	"FULL-TEXT-ID": `\S+`,
	"ARG":          `\S+`,
	"CAP":          `\S+`,
	"KIND":         `(?:chunk|blob|tree|snapshot)`,
	"REASON":       `.+`,
	"STATE":        `(?:packed|burned|verified|on disc only|lost|missing)`,
}

// placeholderSuffixes are the optional texts that can follow a
// placeholder, also when it has a value. `status` adds the last check to
// the disc word STATE.
var placeholderSuffixes = map[string]string{
	"STATE": `(?:, last check(?: failed)? \d{4}-\d{2}-\d{2}|, not checked)?`,
}

const uuidPattern = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`

var placeholderRe = regexp.MustCompile(`[A-Z]+(?:-[A-Z]+)*`)

// lineRegexp turns the text of a cell line into the pattern of one
// output line. A placeholder with a value in values matches that value.
// Another placeholder matches by its kind. Then the suffix of
// placeholderSuffixes can follow. "..." matches any text. The
// line can start with the "noahsark: COMMAND: " prefix of an error, and
// can go on after a space.
func lineRegexp(text string, values map[string]string) *regexp.Regexp {
	var b strings.Builder
	literal := func(s string) {
		parts := strings.Split(s, "...")
		for i, p := range parts {
			if i > 0 {
				b.WriteString(`.*`)
			}
			b.WriteString(regexp.QuoteMeta(p))
		}
	}
	last := 0
	for _, loc := range placeholderRe.FindAllStringIndex(text, -1) {
		name := text[loc[0]:loc[1]]
		pattern, ok := placeholderPatterns[name]
		if !ok || (loc[0] > 0 && (text[loc[0]-1] == '/' || isLetter(text[loc[0]-1]))) {
			continue
		}
		literal(text[last:loc[0]])
		if v := values[name]; v != "" {
			b.WriteString(regexp.QuoteMeta(v))
		} else {
			b.WriteString(pattern)
		}
		b.WriteString(placeholderSuffixes[name])
		last = loc[1]
	}
	literal(text[last:])
	return regexp.MustCompile(`^\s*(?:noahsark: (?:[a-z][a-z -]*: )?)?` + b.String() + `(?:\s.*)?$`)
}

// outputLines are the lines of one output stream, with a read position
// and a mark for each line that a cell line matched.
type outputLines struct {
	lines []string
	pos   int
	used  []bool
}

func newOutputLines(text string) *outputLines {
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	return &outputLines{lines: lines, used: make([]bool, len(lines))}
}

// take finds the first line at or after the read position that re
// matches. It marks the line and moves the read position after it.
func (o *outputLines) take(re *regexp.Regexp) bool {
	for i := o.pos; i < len(o.lines); i++ {
		if re.MatchString(o.lines[i]) {
			o.pos = i + 1
			o.used[i] = true
			return true
		}
	}
	return false
}

func (o *outputLines) save() (int, []bool) { return o.pos, slices.Clone(o.used) }

func (o *outputLines) restore(pos int, used []bool) { o.pos, o.used = pos, used }

// messageCheck is the comparison of the output of a case with a parsed
// Message cell.
type messageCheck struct {
	msg    cellMessage
	values map[string]string
	// flags are the arguments of the case, for the answer flags.
	flags []string
	// omit are spans that this case does not print.
	omit []string
}

// asked reports whether the command asks the question q.
func (mc messageCheck) asked(q cellLine) bool {
	switch q.coveredBy {
	case "--yes":
		return !slices.Contains(mc.flags, "--yes") && !slices.Contains(mc.flags, "--force-yes")
	case "--force-yes":
		return !slices.Contains(mc.flags, "--force-yes")
	}
	return true
}

// run compares out and errOut with the cell. It returns the problems.
// Each line of the cell must be a line of one output, in the order of
// the cell within each output.
func (mc messageCheck) run(out, errOut *outputLines) []string {
	var problems []string
	var absent []cellLine
	if mc.msg.noQuestion {
		absent = append(absent, cellLine{text: confirmQuestionSpan})
	}
	for _, step := range mc.msg.steps {
		var alts [][]cellLine
		for _, alt := range step {
			var lines []cellLine
			omitted := false
			for _, l := range alt {
				switch {
				case slices.Contains(mc.omit, l.span):
					absent = append(absent, l)
					omitted = true
				case l.question && !mc.asked(l):
					absent = append(absent, l)
				default:
					lines = append(lines, l)
				}
			}
			// An omitted span of one of several alternatives drops the
			// alternative. Else it drops the line only.
			if !omitted || len(step) == 1 {
				alts = append(alts, lines)
			}
		}
		if len(alts) == 0 {
			continue
		}
		if !mc.takeOne(alts, out, errOut) {
			var texts []string
			for _, alt := range alts {
				var t []string
				for _, l := range alt {
					t = append(t, fmt.Sprintf("%q", l.text))
				}
				texts = append(texts, strings.Join(t, " then "))
			}
			problems = append(problems, "the output does not hold "+strings.Join(texts, ", or "))
		}
	}
	for _, l := range absent {
		re := lineRegexp(l.text, mc.values)
		if slices.ContainsFunc(append(slices.Clone(out.lines), errOut.lines...), re.MatchString) {
			problems = append(problems, fmt.Sprintf("the output holds %q, which this case must not print", l.text))
		}
	}
	return problems
}

// takeOne takes the lines of the first alternative that the output
// holds.
func (mc messageCheck) takeOne(alts [][]cellLine, out, errOut *outputLines) bool {
	for _, alt := range alts {
		outPos, outUsed := out.save()
		errPos, errUsed := errOut.save()
		ok := true
		for _, l := range alt {
			re := lineRegexp(l.text, mc.values)
			if !out.take(re) && !errOut.take(re) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
		out.restore(outPos, outUsed)
		errOut.restore(errPos, errUsed)
	}
	return false
}

// spansOf returns the spans of the Message cell of row id.
func (tb *stateTable) spansOf(id string) []string {
	var spans []string
	for _, m := range spanRe.FindAllStringSubmatch(tb.rows[id].message, -1) {
		spans = append(spans, m[1])
	}
	return spans
}

// cellState is one state that a State cell names: a disc state, and for
// a lost disc the state when it was marked lost. beforeLost is unknown
// when the cell does not name it.
type cellState struct {
	state      stage.DiscState
	beforeLost stage.DiscState
}

func (s cellState) String() string {
	if s.beforeLost == stage.DiscUnknown {
		return s.state.String()
	}
	return fmt.Sprintf("%s, %s when marked lost", s.state, s.beforeLost)
}

// holds reports whether the state of a case, got, is the state s.
func (s cellState) holds(got cellState) bool {
	return got.state == s.state && (s.beforeLost == stage.DiscUnknown || got.beforeLost == s.beforeLost)
}

var (
	// stateWordRe matches a disc state word of a State cell.
	stateWordRe = regexp.MustCompile(`\b(?:on disc only|packed|burned|verified|lost|missing)\b`)
	// stateRowsRe matches the list of the rows whose states a State cell
	// names, for example "(rows 11, 24, 47, 61 to 63)".
	stateRowsRe = regexp.MustCompile(`\(rows? ([^)]*)\)`)
	// rowRangeRe matches one row or a range of rows of such a list.
	rowRangeRe = regexp.MustCompile(`(\d+[a-z]?)(?: to (\d+[a-z]?))?`)
)

// namedStates returns the disc states that the State cell of row id
// names. A cell that lists rows names the states of those rows. A span
// that is not a state word, such as a command, names no state. In a cell
// that says "when marked lost", each span is the state of a lost disc
// when it was marked lost. Else each state word is one state.
func (tb *stateTable) namedStates(id string) []cellState {
	return tb.namedStatesDepth(id, 0)
}

func (tb *stateTable) namedStatesDepth(id string, depth int) []cellState {
	row, ok := tb.rows[id]
	if !ok || depth > 2 {
		return nil
	}
	var states []cellState
	add := func(s cellState) {
		if !slices.Contains(states, s) {
			states = append(states, s)
		}
	}
	if m := stateRowsRe.FindStringSubmatch(row.state); m != nil {
		for _, r := range rowRangeRe.FindAllStringSubmatch(m[1], -1) {
			last := r[1]
			if r[2] != "" {
				last = r[2]
			}
			from, to := slices.Index(tb.ids, r[1]), slices.Index(tb.ids, last)
			if from < 0 || to < from {
				continue
			}
			for _, ref := range tb.ids[from : to+1] {
				for _, s := range tb.namedStatesDepth(ref, depth+1) {
					add(s)
				}
			}
		}
		return states
	}
	words := map[string]stage.DiscState{}
	for _, s := range resultWords {
		words[s.String()] = s
	}
	var spans []string
	text := spanRe.ReplaceAllStringFunc(row.state, func(s string) string {
		word := s[1 : len(s)-1]
		if _, ok := words[word]; !ok {
			return ""
		}
		spans = append(spans, word)
		return word
	})
	if strings.Contains(text, "when marked lost") {
		for _, w := range spans {
			add(cellState{state: stage.DiscLost, beforeLost: words[w]})
		}
		return states
	}
	for _, w := range stateWordRe.FindAllString(text, -1) {
		add(cellState{state: words[w]})
	}
	return states
}

// TestStatesNamedStates checks the states of a few State cells.
func TestStatesNamedStates(t *testing.T) {
	tb := loadStateTable(t)
	lostFrom := func(s stage.DiscState) cellState { return cellState{state: stage.DiscLost, beforeLost: s} }
	for _, c := range []struct {
		row  string
		want []cellState
	}{
		{"1", nil},
		{"38b", []cellState{{state: stage.DiscPacked}, {state: stage.DiscBurned}, {state: stage.DiscVerified}, {state: stage.DiscOnDiscOnly}}},
		{"64", []cellState{lostFrom(stage.DiscVerified), lostFrom(stage.DiscOnDiscOnly), lostFrom(stage.DiscMissing)}},
		{"85a", nil},
		{"89a", nil},
		{"82", []cellState{{state: stage.DiscBurned}, {state: stage.DiscPacked}, {state: stage.DiscVerified}, {state: stage.DiscOnDiscOnly}, {state: stage.DiscMissing}}},
	} {
		if got := tb.namedStates(c.row); !slices.Equal(got, c.want) {
			t.Errorf("row %s: states %v, want %v", c.row, got, c.want)
		}
	}
}

// TestStatesTableCellsParse fails when a cell of the state x event table
// does not parse. A row in irregularMessageRows must still fail to
// parse.
func TestStatesTableCellsParse(t *testing.T) {
	tb := loadStateTable(t)
	for _, id := range tb.ids {
		if _, err := tb.exitCode(id); err != nil {
			t.Error(err)
		}
		if _, _, err := tb.result(id, ""); err != nil && !errors.Is(err, errNeedsAnswerRow) {
			t.Errorf("row %s: Result: %v", id, err)
		}
		msg, err := tb.message(id, "")
		_, irregular := irregularMessageRows[id]
		switch {
		case irregular && errors.Is(err, errIrregular):
			t.Logf("row %s: the Message cell is irregular: %s", id, irregularMessageRows[id])
		case irregular:
			t.Errorf("row %s is in irregularMessageRows, but its Message cell parses: %v", id, err)
		case err != nil && !errors.Is(err, errNeedsAnswerRow):
			t.Errorf("row %s: Message: %v", id, err)
		case err == nil && msg.next && strings.HasPrefix(tb.rows[id].result, "refused"):
			t.Errorf("row %s is refused, but its Message cell names the next line", id)
		}
	}
	for id := range irregularMessageRows {
		if _, ok := tb.rows[id]; !ok {
			t.Errorf("irregularMessageRows names row %s, which the table does not have", id)
		}
	}
}

// TestStatesLineRegexp checks the pattern of a cell line.
func TestStatesLineRegexp(t *testing.T) {
	values := map[string]string{"SEQ": "3", "LABEL": "x disc 3"}
	for _, c := range []struct {
		text, line string
		match      bool
	}{
		{`disc SEQ "LABEL": burn recorded`, `disc 3 "x disc 3": burn recorded`, true},
		{`disc SEQ "LABEL": burn recorded`, `disc 4 "x disc 3": burn recorded`, false},
		{`disc SEQ is marked lost`, `noahsark: disc burned: disc 3 is marked lost`, true},
		{`disc SEQ is marked lost`, `disc 3 is marked lost; later`, false},
		{`recover: ok`, `recover: ok; disc 3 "x" already known`, false},
		{confirmQuestionSpan, "Continue? [y/N] ", true},
		{`gc: disc SEQ: not verified; N item(s) held`, `gc: disc 3: not verified; 5 item(s) held`, true},
		{`capacity ... holds not one item`, `noahsark: pack: capacity 50KiB holds not one item`, true},
		{`capacity CAP (B bytes) holds not one item; the smallest staged item is KIND ID, B bytes`, `noahsark: pack: capacity 50KiB (51200 bytes) holds not one item; the smallest staged item is chunk 1220ab, 60000 bytes`, true},
		{`disc SEQ "LABEL"  STATE  UUID`, `disc 3 "x disc 3"  verified, last check 2026-09-29  0a1b2c3d-0000-4000-8000-00000000000f`, true},
		{`disc SEQ "LABEL"  STATE  UUID`, `disc 3 "x disc 3"  verified, checked  0a1b2c3d-0000-4000-8000-00000000000f`, false},
		{`burned`, `  burned    record a burn`, true},
		{`warning: disc SEQ "LABEL" (UUID): STATE -> lost`, `warning: disc 3 "x disc 3" (0a1b2c3d-0000-4000-8000-00000000000f): on disc only -> lost`, true},
	} {
		if got := lineRegexp(c.text, values).MatchString(c.line); got != c.match {
			t.Errorf("%q against %q: match %v, want %v", c.text, c.line, got, c.match)
		}
	}
}
