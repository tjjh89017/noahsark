package format

import (
	"fmt"
	"os"
	"testing"
)

// readGolden reads a golden file from testdata.
func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return data
}

// compareGolden compares got against a golden file's bytes and reports the
// first differing offset.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want := readGolden(t, name)
	if len(got) != len(want) {
		t.Fatalf("%s: length mismatch: got %d, want %d", name, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: %s", name, firstDiff(i, got[i], want[i]))
		}
	}
}

func firstDiff(offset int, got, want byte) string {
	return fmt.Sprintf("first differing byte at offset %d: got 0x%02x, want 0x%02x", offset, got, want)
}
