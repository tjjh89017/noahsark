package image

import (
	"os"
	"strings"
	"testing"
)

// TestDecoderPyMatchesRepo checks that the embedded decoder bytes are
// byte-identical to the checked-in reference/decoder.py. A writer must
// copy that file byte for byte; this test catches a stale copy.
func TestDecoderPyMatchesRepo(t *testing.T) {
	want, err := os.ReadFile("../../reference/decoder.py")
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(DecoderPy) {
		t.Fatal("internal/image/decoder.py is out of date with reference/decoder.py")
	}
}

// fencedBlock returns the text inside the first fenced code block found
// after heading in FORMAT.md, without the fence lines themselves.
func fencedBlock(t *testing.T, formatMD, heading string) string {
	t.Helper()
	i := strings.Index(formatMD, heading)
	if i < 0 {
		t.Fatalf("heading %q not found in FORMAT.md", heading)
	}
	rest := formatMD[i:]
	open := strings.Index(rest, "\n```\n")
	if open < 0 {
		t.Fatalf("no fenced block after heading %q", heading)
	}
	rest = rest[open+len("\n```\n"):]
	close := strings.Index(rest, "\n```")
	if close < 0 {
		t.Fatalf("fenced block after heading %q is not closed", heading)
	}
	return rest[:close+1]
}

// TestFormatTxtMatchesFormatMD checks that the embedded FORMAT.txt bytes
// are exactly the Appendix A fenced text in FORMAT.md. FORMAT.txt is
// fixed, normative text; a writer copies it byte for byte.
func TestFormatTxtMatchesFormatMD(t *testing.T) {
	formatMD, err := os.ReadFile("../../FORMAT.md")
	if err != nil {
		t.Fatal(err)
	}
	want := fencedBlock(t, string(formatMD), "## Appendix A. FORMAT.txt, format major 1 minor 0")
	if want != string(FormatTxt) {
		t.Fatal("internal/image/format.txt is out of date with FORMAT.md's Appendix A")
	}
	if len(FormatTxt) != 21718 {
		t.Fatalf("FORMAT.txt: want 21718 bytes, got %d", len(FormatTxt))
	}
}

// TestReadmeTemplateMatchesFormatMD checks that the embedded README.txt
// template is exactly the fenced text in FORMAT.md's README.txt section,
// slots and all.
func TestReadmeTemplateMatchesFormatMD(t *testing.T) {
	formatMD, err := os.ReadFile("../../FORMAT.md")
	if err != nil {
		t.Fatal(err)
	}
	want := fencedBlock(t, string(formatMD), "### 8.4 README.txt")
	if want != readmeTemplate {
		t.Fatal("internal/image/readme_template.txt is out of date with FORMAT.md's README.txt section")
	}
}
