package image

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

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
		t.Fatalf("no fenced block found after heading %q", heading)
	}
	rest = rest[open+len("\n```\n"):]
	close := strings.Index(rest, "\n```")
	if close < 0 {
		t.Fatalf("the fenced block after heading %q is not closed", heading)
	}
	return rest[:close+1]
}

// TestFormatTxtMatchesRepo checks that the embedded FORMAT.txt is
// byte-identical to FORMAT.md. go:embed cannot reach outside the package
// directory, so internal/image/format.txt is a copy; this test catches a
// stale one.
func TestFormatTxtMatchesRepo(t *testing.T) {
	want, err := os.ReadFile("../../FORMAT.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, FormatTxt) {
		t.Fatal("internal/image/format.txt is out of date with FORMAT.md")
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
