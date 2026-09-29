package main

import (
	"fmt"
	"io"
	"strings"
)

// confirmLevel is the kind of a confirmation.
type confirmLevel int

const (
	// confirmOrdinary is answered by --yes or by --force-yes.
	confirmOrdinary confirmLevel = iota
	// confirmCritical is answered only by --force-yes.
	confirmCritical
)

// confirmQuestion is the question after the warning. The answer follows
// it on the same line of the terminal.
const confirmQuestion = "Continue? [y/N] "

// confirm asks the operator before a command changes a record. It prints
// each warning line to standard error. An answer flag that covers the
// level answers yes, and confirm reads nothing. With no terminal on
// standard input, the answer is no, and confirm reads nothing. Otherwise
// confirm prints the question to standard error and reads one line from
// standard input. Only "y" or "yes" and then Enter continues.
//
// command is the command name, such as "disc verified". It names the
// command in the refusal of a critical confirmation that got only --yes.
//
// confirm returns true when the command continues. When it returns
// false, it has printed "nothing changed" to standard output, and the
// command changes nothing and exits 1.
func (e *env) confirm(level confirmLevel, command string, warning []string) bool {
	for _, line := range warning {
		_, _ = fmt.Fprintln(e.stderr, line)
	}
	if e.global.forceYes || (level == confirmOrdinary && e.global.yes) {
		return true
	}
	if !e.stdinTTY {
		if level == confirmCritical && e.global.yes {
			_, _ = fmt.Fprintf(e.stdout, "nothing changed; %s needs --force-yes\n", command)
		} else {
			_, _ = fmt.Fprintln(e.stdout, "nothing changed")
		}
		return false
	}
	_, _ = fmt.Fprint(e.stderr, confirmQuestion)
	answer, complete := readLine(e.stdin)
	if !complete {
		// An end of input before Enter answers no, also after "y". The
		// next line of standard error starts on a new line.
		_, _ = fmt.Fprintln(e.stderr)
	} else if a := strings.TrimSpace(answer); a == "y" || a == "yes" {
		return true
	}
	_, _ = fmt.Fprintln(e.stdout, "nothing changed")
	return false
}

// readLine reads one line from r, one byte at a time, so that r keeps
// the input after the line for a later read. It returns the line without
// its line end. complete is false when the input ends, or a read fails,
// before a newline.
func readLine(r io.Reader) (line string, complete bool) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return strings.TrimSuffix(b.String(), "\r"), true
			}
			b.WriteByte(buf[0])
			continue
		}
		if err != nil {
			return b.String(), false
		}
	}
}
