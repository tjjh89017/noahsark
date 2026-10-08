package image

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// updateReadmeGolden rewrites testdata/readme_golden.txt from buildReadme.
// Run `go test ./internal/image -run TestReadmeGolden -update` only after a
// deliberate change of the README.txt text, and read the diff before you
// commit it.
var updateReadmeGolden = flag.Bool("update", false, "rewrite the README.txt golden file in testdata")

const readmeGoldenPath = "testdata/readme_golden.txt"

// TestReadmeGolden renders README.txt for one fixed set of inputs and
// compares the full text with the golden file. The label holds a byte
// outside the printable range, so the golden also covers the '?' rule.
// The pack time has a negative zone offset with minutes, so the golden
// also covers the {created} offset text.
func TestReadmeGolden(t *testing.T) {
	opts := BuildOptions{
		RepoUUID: [16]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe},
		DiscUUID: [16]byte{0xa0, 0xa1, 0xa2, 0xa3, 0xb0, 0xb1, 0xc0, 0xc1, 0xd0, 0xd1, 0xe0, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5},
	}
	packTime := time.Date(2026, time.October, 8, 14, 30, 5, 0, time.FixedZone("", -(3*3600+30*60)))
	label := []byte("photos-2026\x07")
	const discSeq = 7

	got := buildReadme(opts, packTime, label, discSeq)

	if *updateReadmeGolden {
		if err := os.MkdirAll(filepath.Dir(readmeGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(readmeGoldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(readmeGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("rendered README.txt differs from %s\n--- got ---\n%s", readmeGoldenPath, got)
	}
}
