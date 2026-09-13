package main

import (
	"strings"
	"testing"
)

// TestParseCapacityPresets checks every named preset against its real,
// drive-reported sector count, and that the byte size documented for
// that media converts to the same count of 2048-byte sectors.
func TestParseCapacityPresets(t *testing.T) {
	cases := []struct {
		name    string
		sectors uint64
		bytes   uint64
	}{
		{"dvd+r", 2_295_104, 4_700_372_992},
		{"dvd-r", 2_298_496, 4_707_319_808},
		{"bd25", 12_219_392, 25_025_314_816},
		{"bd50", 24_438_784, 50_050_629_632},
		{"bd100", 48_878_592, 100_103_356_416},
		{"bd128", 62_500_864, 128_001_769_472},
	}
	for _, c := range cases {
		if c.sectors*2048 != c.bytes {
			t.Fatalf("%s: %d sectors is %d bytes, want %d", c.name, c.sectors, c.sectors*2048, c.bytes)
		}
		for _, name := range []string{c.name, strings.ToUpper(c.name)} {
			got, err := parseCapacity(name)
			if err != nil {
				t.Fatalf("parseCapacity(%q): %v", name, err)
			}
			if got != c.sectors {
				t.Errorf("parseCapacity(%q) = %d, want %d", name, got, c.sectors)
			}
		}
	}
}
