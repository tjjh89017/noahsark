package image

import (
	"os"
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
