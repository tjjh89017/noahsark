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

// parseCapacity reads a capacity value. A name from capacityPresets
// (case insensitive) is the real sector count of that media. A number
// followed by a decimal unit suffix (k, M, G, T, kB, MB, GB or TB) or a
// binary one (Ki, Mi, Gi, Ti, KiB, MiB, GiB or TiB) is a byte size; see
// byteSizeUnits for which suffix means which. Optical media is marketed
// in decimal units, so 25G is 25,000,000,000 bytes, the same as 25GB; a
// preset like bd25 still names the exact real sector count of that
// disc, not a rounded marketing size. A byte size is converted to whole
// sectors at FORMAT.md's 2048-byte sector size, rounding up so the
// requested size always fits.
//
// A bare number is refused. It reads as a byte count and means sectors,
// a factor of 2048 apart, and the operator has no way to see which one
// the tool took.
//
// Reading: docs/decisions.md, "16. CLI reference".
func parseCapacity(s string) (uint64, error) {
	if sectors, ok := capacityPresets[strings.ToLower(s)]; ok {
		return sectors, nil
	}
	if numPart, scale, ok := cutSizeUnit(s); ok {
		n, err := strconv.ParseFloat(numPart, 64)
		if err != nil {
			return 0, fmt.Errorf("capacity: invalid size %q", s)
		}
		bytes := uint64(n * float64(scale))
		return (bytes + image.SectorSize - 1) / image.SectorSize, nil
	}
	if _, err := strconv.ParseUint(s, 10, 64); err == nil {
		return 0, fmt.Errorf("capacity: %q has no unit; give a preset (%s) or a size with a unit, for example 25GB",
			s, strings.Join(capacityPresetNames(), ", "))
	}
	return 0, fmt.Errorf("capacity: invalid value %q; give a preset (%s) or a size with a unit, for example 25GB",
		s, strings.Join(capacityPresetNames(), ", "))
}

// byteSizeUnits lists the unit suffixes --capacity accepts on a size
// value, matched case-insensitively. A suffix with
// no "i" (k, M, G, T, kB, MB, GB, TB) is decimal: a power of 10, the
// convention optical media capacities and disc drives are marketed in.
// A suffix with an "i" (Ki, Mi, Gi, Ti, KiB, MiB, GiB, TiB) is binary: a
// power of 2, the convention memory and file sizes are commonly given
// in. "G" and "Gi" therefore name different byte counts, and likewise
// for every other letter; a caller must not treat them as the same
// unit.
var byteSizeUnits = []struct {
	suffix string // lower-case
	scale  uint64
}{
	{"tb", 1_000_000_000_000},
	{"gb", 1_000_000_000},
	{"mb", 1_000_000},
	{"kb", 1_000},
	{"t", 1_000_000_000_000},
	{"g", 1_000_000_000},
	{"m", 1_000_000},
	{"k", 1_000},
	{"tib", 1 << 40},
	{"gib", 1 << 30},
	{"mib", 1 << 20},
	{"kib", 1 << 10},
	{"ti", 1 << 40},
	{"gi", 1 << 30},
	{"mi", 1 << 20},
	{"ki", 1 << 10},
}

// cutSizeUnit matches s against byteSizeUnits, case-insensitively, and
// returns the leading numeric text and the matched unit's scale. It
// reports false when s carries none of byteSizeUnits' suffixes.
func cutSizeUnit(s string) (numPart string, scale uint64, ok bool) {
	lower := strings.ToLower(s)
	for _, u := range byteSizeUnits {
		if strings.HasSuffix(lower, u.suffix) {
			return strings.TrimSpace(s[:len(s)-len(u.suffix)]), u.scale, true
		}
	}
	return "", 0, false
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
// accepts, for help text and error messages: decimal (power of 10) then
// binary (power of 2), matching byteSizeUnits.
var capacityUnitSuffixes = []string{"k", "M", "G", "T", "kB", "MB", "GB", "TB", "Ki", "Mi", "Gi", "Ti", "KiB", "MiB", "GiB", "TiB"}

// capacityHelpText describes the accepted capacity spellings: the
// preset names and the unit suffixes. It is shared by the flags' own
// usage text and by an error that rejects a parsed value.
func capacityHelpText() string {
	return fmt.Sprintf("a preset (%s), or a size with a decimal (%s) or binary (%s) unit; G is not Gi",
		strings.Join(capacityPresetNames(), ", "),
		strings.Join(capacityUnitSuffixes[:8], ", "),
		strings.Join(capacityUnitSuffixes[8:], ", "))
}
