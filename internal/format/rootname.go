package format

import "strings"

// EncodeRootName escapes a source root's absolute path into the one path
// component a root tree entry's name must be: '/' becomes "%2F", '\'
// becomes "%5C", NUL becomes "%00", and '%' becomes "%25". Every other
// byte is kept as it is. The source root path is stored this one time;
// no TLV and no snapshot metadata record repeats it.
func EncodeRootName(path string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		switch c := path[i]; c {
		case '/':
			b.WriteString("%2F")
		case '\\':
			b.WriteString("%5C")
		case 0:
			b.WriteString("%00")
		case '%':
			b.WriteString("%25")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// DecodeRootName reverses EncodeRootName. It replaces every '%' and the
// two hexadecimal digits after it by the one byte those digits name, in
// either case. When a '%' is not followed by two hexadecimal digits, the
// whole name is returned undecoded and ok is false, so the caller can
// report it.
func DecodeRootName(name string) (path string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(name) {
			return name, false
		}
		hi, hiOK := hexDigit(name[i+1])
		lo, loOK := hexDigit(name[i+2])
		if !hiOK || !loOK {
			return name, false
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), true
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}
