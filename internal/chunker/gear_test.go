package chunker

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

// TestGearTable recomputes the table from the normative rule and compares
// every entry against the vendored literal.
func TestGearTable(t *testing.T) {
	seed := []byte("noahsark/gear/v1")
	if len(seed) != 16 {
		t.Fatalf("seed length = %d, want 16", len(seed))
	}

	for i := 0; i < 256; i++ {
		input := append(append([]byte{}, seed...), byte(i))
		digest := sha256.Sum256(input)
		want := binary.LittleEndian.Uint64(digest[0:8])
		if got := gearTable[i]; got != want {
			t.Errorf("gearTable[%d] = 0x%016x, want 0x%016x", i, got, want)
		}
	}
}
