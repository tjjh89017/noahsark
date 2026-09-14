package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tjjh89017/noahsark/internal/image"
)

// capacityPresets names the real, drive-reported sector count of common
// write-once optical media, keyed by lower-case preset name. A marketing
// size such as "25 GB" is decimal-rounded and is not the sector count a
// drive actually reports; these values are the real counts, checked
// against dvd+rw-mediainfo output. See docs/decisions.md,
// "16. CLI reference".
var capacityPresets = map[string]uint64{
	"dvd+r": 2_295_104,
	"dvd-r": 2_298_496,
	"bd25":  12_219_392,
	"bd50":  24_438_784,
	"bd100": 48_878_592,
	"bd128": 62_500_864,
}

// parseCapacity reads a --capacity value. A name from capacityPresets
// (case insensitive) is the real sector count of that media. A bare
// integer is a sector count. An integer followed by GiB, MiB or KiB is a
// binary byte size; followed by GB, MB or KB is a decimal byte size, the
// marketing convention optical media capacities are named in. Either byte
// form is converted to whole sectors at FORMAT.md's 2048-byte sector
// size, rounding up so the requested size always fits.
//
// Reading: docs/decisions.md, "16. CLI reference".
func parseCapacity(s string) (uint64, error) {
	if sectors, ok := capacityPresets[strings.ToLower(s)]; ok {
		return sectors, nil
	}
	for _, u := range byteSizeUnits {
		if numPart, ok := strings.CutSuffix(s, u.suffix); ok {
			numPart = strings.TrimSpace(numPart)
			n, err := strconv.ParseFloat(numPart, 64)
			if err != nil {
				return 0, fmt.Errorf("capacity: invalid size %q", s)
			}
			bytes := uint64(n * float64(u.scale))
			return (bytes + image.SectorSize - 1) / image.SectorSize, nil
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("capacity: invalid value %q, expected a preset name, a sector count, or a size like 25GB", s)
	}
	return n, nil
}

// byteSizeUnits lists the unit suffixes --capacity and --staging-budget
// both accept on a size value: a binary byte size (GiB, MiB, KiB) or a
// decimal one (GB, MB, KB), the marketing convention optical media
// capacities are named in.
var byteSizeUnits = []struct {
	suffix string
	scale  uint64
}{
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"GB", 1_000_000_000},
	{"MB", 1_000_000},
	{"KB", 1_000},
}

// parseByteSize parses a plain byte count, or a number followed by one of
// byteSizeUnits' suffixes, into a byte count. Unlike parseCapacity, a bare
// integer here is bytes, not sectors: --staging-budget and
// restore.staging_budget are plain byte quantities, not media capacities.
func parseByteSize(s string) (uint64, error) {
	for _, u := range byteSizeUnits {
		if numPart, ok := strings.CutSuffix(s, u.suffix); ok {
			numPart = strings.TrimSpace(numPart)
			n, err := strconv.ParseFloat(numPart, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			return uint64(n * float64(u.scale)), nil
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q, expected a byte count or a size like 4GiB", s)
	}
	return n, nil
}

// capacityPresetNames lists capacityPresets' keys, sorted, for help text
// and error messages.
func capacityPresetNames() []string {
	names := make([]string, 0, len(capacityPresets))
	for name := range capacityPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// capacityUnitSuffixes lists the byte-size unit suffixes parseCapacity
// accepts, for help text and error messages.
var capacityUnitSuffixes = []string{"GiB", "MiB", "KiB", "GB", "MB", "KB"}

// capacityHelpText describes the accepted --capacity and --physical-capacity
// spellings: the preset names and the unit suffixes. It is shared by the
// flags' own usage text and by an error that rejects a parsed value.
func capacityHelpText() string {
	return fmt.Sprintf("a preset (%s), a plain sector count, or a size with a unit (%s)",
		strings.Join(capacityPresetNames(), ", "), strings.Join(capacityUnitSuffixes, ", "))
}

// packFixedFileCount is the number of files every run writes before any
// snapshot or staged object is counted: INDEX, RUN, DISC, README,
// FORMAT, decoder, REFS and DISCS, plus the RUN2.bin header copy that
// every run carries whether or not it has FEC.
const packFixedFileCount = 8 + 1

// checkCapacityMinimum refuses a capacity too small to hold even an
// empty run's own fixed files and header copies. pack hits this same
// floor as ErrCapacityTooSmall once it counts real objects; init checks
// it up front, with no objects yet to count, so a too-small --capacity
// is refused before it is written into the config instead of failing
// every later pack.
func checkCapacityMinimum(capacitySectors uint64) error {
	if err := image.CheckCapacity(0, 0, 0, 2*image.RunFileLen, packFixedFileCount, capacitySectors); err != nil {
		return fmt.Errorf("--capacity=%d sectors (%d bytes) is too small: %s", capacitySectors, capacitySectors*image.SectorSize, capacityHelpText())
	}
	return nil
}
