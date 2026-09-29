package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// countingReader counts the calls to Read.
type countingReader struct {
	r     io.Reader
	reads int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	return c.r.Read(p)
}

func TestConfirm(t *testing.T) {
	const (
		w1       = `warning: disc 3 "L" (UUID): burned -> verified`
		w2       = "the effect"
		warnText = w1 + "\n" + w2 + "\n"
		asked    = warnText + "Continue? [y/N] "
	)
	type answer struct {
		name     string
		tty      bool
		yes      bool
		forceYes bool
		input    string
	}
	type want struct {
		proceed bool
		stderr  string
		stdout  string
		read    bool
	}
	answers := []answer{
		{name: "tty y", tty: true, input: "y\n"},
		{name: "tty yes", tty: true, input: "yes\n"},
		{name: "tty Y", tty: true, input: "Y\n"},
		{name: "tty YeS", tty: true, input: "YeS\n"},
		{name: "tty spaces around yes", tty: true, input: "  yes \t\n"},
		{name: "tty yess", tty: true, input: "yess\n"},
		{name: "tty y with inner space", tty: true, input: "y es\n"},
		{name: "tty n", tty: true, input: "n\n"},
		{name: "tty empty line", tty: true, input: "\n"},
		{name: "tty other text", tty: true, input: "sure\n"},
		{name: "tty end of input", tty: true, input: ""},
		{name: "tty y then end of input", tty: true, input: "y"},
		{name: "no tty", input: "y\n"},
		{name: "no tty --yes", yes: true, input: "n\n"},
		{name: "no tty --force-yes", forceYes: true, input: "n\n"},
		{name: "no tty both flags", yes: true, forceYes: true, input: "n\n"},
		{name: "tty --yes y", tty: true, yes: true, input: "y\n"},
		{name: "tty --yes n", tty: true, yes: true, input: "n\n"},
		{name: "tty --force-yes", tty: true, forceYes: true, input: "n\n"},
	}
	yesAsked := want{proceed: true, stderr: asked, read: true}
	noAsked := want{stderr: asked, stdout: "nothing changed\n", read: true}
	noEOF := want{stderr: asked + "\n", stdout: "nothing changed\n", read: true}
	noUnread := want{stderr: warnText, stdout: "nothing changed\n"}
	yesUnread := want{proceed: true, stderr: warnText}
	wants := map[confirmLevel]map[string]want{
		confirmOrdinary: {
			"tty y":                   yesAsked,
			"tty yes":                 yesAsked,
			"tty Y":                   yesAsked,
			"tty YeS":                 yesAsked,
			"tty spaces around yes":   yesAsked,
			"tty yess":                noAsked,
			"tty y with inner space":  noAsked,
			"tty n":                   noAsked,
			"tty empty line":          noAsked,
			"tty other text":          noAsked,
			"tty end of input":        noEOF,
			"tty y then end of input": noEOF,
			"no tty":                  noUnread,
			"no tty --yes":            yesUnread,
			"no tty --force-yes":      yesUnread,
			"no tty both flags":       yesUnread,
			"tty --yes y":             yesUnread,
			"tty --yes n":             yesUnread,
			"tty --force-yes":         yesUnread,
		},
		confirmCritical: {
			"tty y":                   yesAsked,
			"tty yes":                 yesAsked,
			"tty Y":                   yesAsked,
			"tty YeS":                 yesAsked,
			"tty spaces around yes":   yesAsked,
			"tty yess":                noAsked,
			"tty y with inner space":  noAsked,
			"tty n":                   noAsked,
			"tty empty line":          noAsked,
			"tty other text":          noAsked,
			"tty end of input":        noEOF,
			"tty y then end of input": noEOF,
			"no tty":                  noUnread,
			"no tty --yes":            {stderr: warnText, stdout: "nothing changed; disc verified needs --force-yes\n"},
			"no tty --force-yes":      yesUnread,
			"no tty both flags":       yesUnread,
			"tty --yes y":             yesAsked,
			"tty --yes n":             noAsked,
			"tty --force-yes":         yesUnread,
		},
	}
	levelNames := map[confirmLevel]string{confirmOrdinary: "ordinary", confirmCritical: "critical"}
	for level, byAnswer := range wants {
		for _, a := range answers {
			w, ok := byAnswer[a.name]
			if !ok {
				t.Fatalf("%s: no expected result for %q", levelNames[level], a.name)
			}
			t.Run(levelNames[level]+"/"+a.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				stdin := &countingReader{r: strings.NewReader(a.input)}
				e := &env{stdout: &stdout, stderr: &stderr, stdin: stdin, stdinTTY: a.tty}
				e.global.yes = a.yes
				e.global.forceYes = a.forceYes

				got := e.confirm(level, "disc verified", []string{w1, w2})

				if got != w.proceed {
					t.Errorf("confirm = %v, want %v", got, w.proceed)
				}
				if stderr.String() != w.stderr {
					t.Errorf("stderr = %q, want %q", stderr.String(), w.stderr)
				}
				if stdout.String() != w.stdout {
					t.Errorf("stdout = %q, want %q", stdout.String(), w.stdout)
				}
				if read := stdin.reads > 0; read != w.read {
					t.Errorf("read standard input = %v, want %v", read, w.read)
				}
			})
		}
	}
}

func TestConfirmReadsOneLine(t *testing.T) {
	stdin := strings.NewReader("yes\r\nnext line\n")
	e := &env{stdout: io.Discard, stderr: io.Discard, stdin: stdin, stdinTTY: true}

	if !e.confirm(confirmOrdinary, "pack --undo", nil) {
		t.Fatal("confirm = false for \"yes\" and CR LF, want true")
	}

	rest, err := io.ReadAll(stdin)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "next line\n" {
		t.Errorf("input after the answer = %q, want %q", rest, "next line\n")
	}
}
