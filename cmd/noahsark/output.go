package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tjjh89017/noahsark/internal/object"
)

// nextStatusLine ends the output of a command that changed state when
// the command cannot read the repository for the next block.
const nextStatusLine = "next: noahsark status"

// printNext ends the output of a command that changed state: the advice
// lines and the next block that status would print now. It reads the
// repository at repoDir again, after the change. The notes of that read
// were printed by the command already, thus they are dropped. When the
// read fails, it points to status.
func printNext(e *env, repoDir string) {
	lines := []string{nextStatusLine}
	if cfg, err := readConfig(configPath(repoDir)); err == nil {
		if v, err := readStatusView("status", repoDir, cfg, io.Discard); err == nil {
			lines = v.nextLines()
		}
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(e.stdout, line)
	}
}

// plainShellWordRe matches a word that the shell reads as it is.
var plainShellWordRe = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// quoteShellWord returns s as one shell word. It puts s in single quotes
// when s holds a character that the shell would change.
func quoteShellWord(s string) string {
	if plainShellWordRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// statusDate is the DATE of status and gc: the local date, YYYY-MM-DD.
func statusDate(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

// shortID gives the print form of a snapshot id: the first 12
// hexadecimal characters of its digest, without the multihash prefix.
// A file of state/ and a damaged line use the full text form.
func shortID(id object.ID) string {
	return hex.EncodeToString(id[:6])
}

// discNameShort names a disc the way an operator reads it off the
// sleeve: the number and the label. Every command uses this one form.
func discNameShort(seq uint64, label string) string {
	return fmt.Sprintf("disc %d %q", seq, label)
}

// discName is discNameShort with the uuid, the exact name of a disc. A
// message that must tell two discs apart uses this form.
func discName(seq uint64, label string, uuid [16]byte) string {
	return fmt.Sprintf("disc %d %q (%s)", seq, label, uuidText(uuid))
}

// escapeField gives the print form of a path, a source path or a message
// in one tab-separated field of ls and log. A backslash becomes `\\`, a
// tab `\t` and a newline `\n`. Each other byte below 0x20, the byte 0x7F
// and each byte that is not part of valid UTF-8 becomes `\xHH`. Every
// other byte prints as it is.
func escapeField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c < 0x20 || c == 0x7F:
			_, _ = fmt.Fprintf(&b, `\x%02x`, c)
		case c < utf8.RuneSelf:
			b.WriteByte(c)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				_, _ = fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteString(s[i : i+size])
			}
			i += size
			continue
		}
		i++
	}
	return b.String()
}
