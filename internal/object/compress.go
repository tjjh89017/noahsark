package object

import (
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
