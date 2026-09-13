package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tjjh89017/noahsark/internal/image"
)

// parseCapacity reads a --capacity value. A bare integer is a sector
// count. An integer followed by GiB, MiB or KiB is a binary byte size;
// followed by GB, MB or KB is a decimal byte size, the marketing
// convention optical media capacities are named in. Either byte form is
// converted to whole sectors at FORMAT.md's 2048-byte sector size,
// rounding up so the requested size always fits.
//
// Reading: docs/decisions.md, "16. CLI reference".
func parseCapacity(s string) (uint64, error) {
	units := []struct {
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
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			numPart := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
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
		return 0, fmt.Errorf("capacity: invalid value %q, expected a sector count or a size like 25GB", s)
	}
	return n, nil
}
