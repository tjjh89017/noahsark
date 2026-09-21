package image

import (
	"bytes"
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
	return fencedBlockN(t, formatMD, heading, 0)
}

// fencedBlockN returns the text inside fenced code block n, counted from
// zero, after heading in FORMAT.md, without the fence lines themselves.
func fencedBlockN(t *testing.T, formatMD, heading string, n int) string {
	t.Helper()
	i := strings.Index(formatMD, heading)
	if i < 0 {
		t.Fatalf("heading %q not found in FORMAT.md", heading)
	}
	rest := formatMD[i:]
	for {
		open := strings.Index(rest, "\n```\n")
		if open < 0 {
			t.Fatalf("fenced block %d not found after heading %q", n, heading)
		}
		rest = rest[open+len("\n```\n"):]
		close := strings.Index(rest, "\n```")
		if close < 0 {
			t.Fatalf("a fenced block after heading %q is not closed", heading)
		}
		if n == 0 {
			return rest[:close+1]
		}
		n--
		rest = rest[close+len("\n```"):]
	}
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

// TestParitySlotValuesMatchFormatMD checks that the three parity slot
// values the writer substitutes are the exact texts FORMAT.md's
// README.txt section prints beside the template.
func TestParitySlotValuesMatchFormatMD(t *testing.T) {
	formatMD, err := os.ReadFile("../../FORMAT.md")
	if err != nil {
		t.Fatal(err)
	}
	const heading = "### 8.4 README.txt"
	cases := []struct {
		name  string
		block int
		got   string
	}{
		{"{parity_files}", 1, parityFilesValue},
		{"{parity_repair} with parity", 2, parityRepairFEC},
		{"{parity_repair} with no parity", 3, parityRepairNone},
	}
	for _, c := range cases {
		want := fencedBlockN(t, string(formatMD), heading, c.block)
		if want != c.got {
			t.Errorf("the value of %s is out of date with FORMAT.md", c.name)
		}
	}
}
