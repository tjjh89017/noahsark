package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

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
