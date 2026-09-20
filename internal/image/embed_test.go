package image

import (
	"crypto/sha256"
	"encoding/hex"
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

// TestFormatTxtIsPinned checks the byte length and the SHA-256 digest of
// the embedded FORMAT.txt. internal/image/format.txt is the normative
// source of the on-disc text, so a change to it changes disc bytes and
// must be deliberate.
func TestFormatTxtIsPinned(t *testing.T) {
	const wantLen = 21718
	const wantSHA256 = "ed64d4fc7e673f568d67e83148cd1b572bb7b3fa2f46e651b33f69f4f754ad8c"
	if len(FormatTxt) != wantLen {
		t.Fatalf("FORMAT.txt: want %d bytes, got %d", wantLen, len(FormatTxt))
	}
	sum := sha256.Sum256(FormatTxt)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		t.Fatalf("FORMAT.txt: want SHA-256 %s, got %s", wantSHA256, got)
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
