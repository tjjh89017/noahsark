package object

import (
	"bytes"
	"crypto/rand"
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
