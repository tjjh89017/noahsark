package object

import (
	"crypto/sha256"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"

	"github.com/tjjh89017/noahsark/internal/format"
)

// MinGain is the configured minimum compression gain, the fraction of the
// payload compression must save before a writer stores it compressed.
const MinGain = 0.05

// zstdLevel is the configured zstd level, applied after chunking and
// hashing, never before.
var zstdLevel = zstd.SpeedDefault

var (
	encoderOnce sync.Once
	encoder     *zstd.Encoder
)

// getEncoder returns the shared zstd encoder, built once with the frame
// parameters this project's writer must emit: a single segment, the
// content size present, no checksum, no dictionary.
func getEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		enc, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstdLevel),
			zstd.WithEncoderCRC(false),
			zstd.WithSingleSegment(true),
		)
		if err != nil {
			// The option set above is always valid; a failure here is a
			// programming error, not a runtime condition to recover from.
			panic(err)
		}
		encoder = enc
	})
	return encoder
}

// Compress applies the minimum-gain rule to payload and returns the bytes
// to store, the compression code to record, and the stored length. It
// returns the payload unchanged with CompressionNone when zstd does not
// save at least MinGain of the payload length.
func Compress(payload []byte) (stored []byte, code format.Compression, storedLen uint64) {
	if len(payload) == 0 {
		return payload, format.CompressionNone, 0
	}
	z := getEncoder().EncodeAll(payload, nil)
	gain := 1 - float64(len(z))/float64(len(payload))
	if gain < MinGain {
		return payload, format.CompressionNone, uint64(len(payload))
	}
	return z, format.CompressionZstd, uint64(len(z))
}

// Decompress reverses Compress. It decodes stored zstd bytes into
// payloadLen bytes of payload, or returns stored as is when code is
// CompressionNone.
func Decompress(stored []byte, code format.Compression, payloadLen uint64) ([]byte, error) {
	if code == format.CompressionNone {
		return stored, nil
	}
	d, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	out, err := d.DecodeAll(stored, make([]byte, 0, payloadLen))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HashStreamed reads exactly storedLen bytes of stored payload from r,
// decompressing when code says so, and returns the sha256 content id of
// the decompressed payload. It streams the whole way through a hasher:
// it never holds the payload whole in memory, whatever the object's own
// size, unlike Decompress which returns the full decoded payload.
func HashStreamed(r io.Reader, code format.Compression, storedLen uint64) (id [32]byte, err error) {
	lr := io.LimitReader(r, int64(storedLen))
	h := sha256.New()
	if code == format.CompressionNone {
		if _, err := io.Copy(h, lr); err != nil {
			return id, err
		}
		copy(id[:], h.Sum(nil))
		return id, nil
	}
	d, err := zstd.NewReader(lr)
	if err != nil {
		return id, err
	}
	defer d.Close()
	if _, err := io.Copy(h, d); err != nil {
		return id, err
	}
	copy(id[:], h.Sum(nil))
	return id, nil
}
