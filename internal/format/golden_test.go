package format

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// updateGolden rewrites every golden file from the encoder. Run
// `go test ./internal/format -update` after a deliberate format change,
// and read the diff before you commit it.
var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// readGolden reads a golden file from testdata.
func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return data
}

// writeGolden writes a golden file into testdata.
func writeGolden(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile("testdata/"+name, data, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", name, err)
	}
}

// compareGolden compares got against a golden file's bytes and reports the
// first differing offset.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	if *updateGolden {
		writeGolden(t, name, got)
		return
	}
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
