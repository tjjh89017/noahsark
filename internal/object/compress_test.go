package object

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	mrand "math/rand"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

func TestCompressIncompressibleStaysRaw(t *testing.T) {
	payload := make([]byte, 64*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	stored, code, storedLen := Compress(payload)
	if code != format.CompressionNone {
		t.Fatalf("code = %v, want CompressionNone", code)
	}
	if storedLen != uint64(len(payload)) {
		t.Fatalf("storedLen = %d, want %d", storedLen, len(payload))
	}
	if !bytes.Equal(stored, payload) {
		t.Fatal("stored bytes differ from the raw payload")
	}
}

func TestCompressCompressibleBecomesZstd(t *testing.T) {
	payload := bytes.Repeat([]byte("noahsark backup test data "), 4096)
	stored, code, storedLen := Compress(payload)
	if code != format.CompressionZstd {
		t.Fatalf("code = %v, want CompressionZstd", code)
	}
	if storedLen != uint64(len(stored)) {
		t.Fatalf("storedLen = %d, want %d", storedLen, len(stored))
	}
	if len(stored) >= len(payload) {
		t.Fatalf("stored length %d not smaller than payload length %d", len(stored), len(payload))
	}
}

func TestCompressDecompressRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("round trip content "), 8192)
	stored, code, _ := Compress(payload)
	got, err := Decompress(stored, code, uint64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("round trip did not reproduce the payload")
	}
}

func TestCompressEmptyPayload(t *testing.T) {
	stored, code, storedLen := Compress(nil)
	if code != format.CompressionNone || storedLen != 0 || len(stored) != 0 {
		t.Fatalf("Compress(nil) = %v, %v, %d, want none, 0", stored, code, storedLen)
	}
}

// goldenCompressedSHA256 is the sha256 of Compress's output over
// goldenPayload (below), fixed to the encoder's option set: any change
// to the zstd level, window, or another encoder option that shifts the
// compressed bytes must update this constant deliberately, not by
// accident.
const goldenCompressedSHA256 = "88010f656cb1bb424354120b19d7f0e91cd06e3b68be84dd65e4d7537fc0fffa"

// goldenPayload builds a fixed 4 MiB pseudo-random-but-compressible
// input: a 256 KiB pseudo-random block, seeded and repeated, so zstd has
// real repetition to exploit while the source itself stays
// non-trivial.
func goldenPayload() []byte {
	const size = 4 << 20
	block := make([]byte, 256*1024)
	mrand.New(mrand.NewSource(7)).Read(block)
	data := make([]byte, 0, size)
	for len(data) < size {
		data = append(data, block...)
	}
	return data[:size]
}

// TestCompressGoldenBytes pins Compress's output bytes for a fixed
// input, so a future change to the encoder's option set (concurrency,
// memory mode, level, or anything else) is caught here rather than
// silently changing what every future disc's chunks decode to.
func TestCompressGoldenBytes(t *testing.T) {
	stored, code, storedLen := Compress(goldenPayload())
	if code != format.CompressionZstd {
		t.Fatalf("code = %v, want CompressionZstd", code)
	}
	if storedLen != uint64(len(stored)) {
		t.Fatalf("storedLen = %d, want %d", storedLen, len(stored))
	}
	sum := sha256.Sum256(stored)
	if got := hex.EncodeToString(sum[:]); got != goldenCompressedSHA256 {
		t.Fatalf("compressed bytes changed: sha256 = %s, want %s", got, goldenCompressedSHA256)
	}
}
