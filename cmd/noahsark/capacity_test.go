package main

import (
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
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

// TestCutSizeUnitSuffixes checks every decimal and binary unit suffix
// byteSizeUnits accepts, in each case's own casing, upper case and
// lower case, against the exact scale that suffix must carry.
// parseCapacity is the one caller left of cutSizeUnit; this test covers
// the full suffix table directly, since parseCapacity's own tests below
// exercise only "G" and "Gi".
func TestCutSizeUnitSuffixes(t *testing.T) {
	cases := []struct {
		in    string
		scale uint64
	}{
		// Decimal: a power of 10, with or without the trailing "B".
		{"1k", 1_000},
		{"1M", 1_000_000},
		{"1G", 1_000_000_000},
		{"1T", 1_000_000_000_000},
		{"1kB", 1_000},
		{"1MB", 1_000_000},
		{"1GB", 1_000_000_000},
		{"1TB", 1_000_000_000_000},
		// Binary: a power of 2, with or without the trailing "B".
		{"1Ki", 1 << 10},
		{"1Mi", 1 << 20},
		{"1Gi", 1 << 30},
		{"1Ti", 1 << 40},
		{"1KiB", 1 << 10},
		{"1MiB", 1 << 20},
		{"1GiB", 1 << 30},
		{"1TiB", 1 << 40},
	}
	for _, c := range cases {
		for _, in := range []string{c.in, strings.ToUpper(c.in), strings.ToLower(c.in)} {
			numPart, scale, ok := cutSizeUnit(in)
			if !ok {
				t.Fatalf("cutSizeUnit(%q): no suffix matched", in)
			}
			if scale != c.scale {
				t.Errorf("cutSizeUnit(%q) scale = %d, want %d", in, scale, c.scale)
			}
			if numPart == "" {
				t.Errorf("cutSizeUnit(%q) numPart is empty", in)
			}
		}
	}
}

// TestParseCapacityDecimalIsNotBinary checks the rule this build fixes:
// a decimal suffix (no "i") means a power of 10, a binary suffix (with
// "i") means a power of 2, and the two must not collapse to the same
// sector count. It also checks that a preset like bd25 keeps naming the
// disc's exact real sector count, not a value derived by rounding a
// marketing size.
func TestParseCapacityDecimalIsNotBinary(t *testing.T) {
	decimalSectors, err := parseCapacity("25G")
	if err != nil {
		t.Fatalf("parseCapacity(25G): %v", err)
	}
	wantDecimalSectors := (uint64(25_000_000_000) + image.SectorSize - 1) / image.SectorSize
	if decimalSectors != wantDecimalSectors {
		t.Errorf("parseCapacity(25G) = %d sectors, want %d", decimalSectors, wantDecimalSectors)
	}

	binarySectors, err := parseCapacity("25Gi")
	if err != nil {
		t.Fatalf("parseCapacity(25Gi): %v", err)
	}
	wantBinarySectors := (uint64(25)*(1<<30) + image.SectorSize - 1) / image.SectorSize
	if binarySectors != wantBinarySectors {
		t.Errorf("parseCapacity(25Gi) = %d sectors, want %d", binarySectors, wantBinarySectors)
	}

	if decimalSectors == binarySectors {
		t.Fatal("25G and 25Gi must not parse to the same sector count")
	}

	bd25Sectors, err := parseCapacity("bd25")
	if err != nil {
		t.Fatalf("parseCapacity(bd25): %v", err)
	}
	if bd25Sectors != 12_219_392 {
		t.Errorf("parseCapacity(bd25) = %d, want the real drive-reported 12,219,392", bd25Sectors)
	}
	if bd25Sectors == decimalSectors {
		t.Fatal("bd25 must keep its own real sector count, not a marketing-rounded 25G value")
	}
}
