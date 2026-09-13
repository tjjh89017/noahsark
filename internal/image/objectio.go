package image

import (
	"crypto/sha256"
	"io"
	"os"

	"github.com/tjjh89017/noahsark/internal/format"
)

// hashFile returns sha256 of path's whole bytes, read in fixed-size
// chunks so a large object never sits in memory whole.
func hashFile(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// readObjectHeaderFile reads only the common and object header prefix of
// the object file at path, the storedLen, payloadLen and compression id
// an object row needs, without reading the object's payload.
func readObjectHeaderFile(path string) (storedLen, payloadLen uint64, compression format.Compression, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = f.Close() }()
	headerLen := format.CommonHeaderLen + format.ObjectHeaderLen
	buf := make([]byte, headerLen)
	if _, err := io.ReadFull(f, buf); err != nil {
		return 0, 0, 0, err
	}
	var h format.CommonHeader
	if err := h.Decode(buf); err != nil {
		return 0, 0, 0, err
	}
	var oh format.ObjectHeader
	if err := oh.Decode(buf[format.CommonHeaderLen:]); err != nil {
		return 0, 0, 0, err
	}
	return oh.StoredLen, oh.PayloadLen, oh.Compression, nil
}

// copyFileStream copies the whole file at src to dst, creating dst's
// parent directories, without reading src's bytes whole into memory.
// When sink is not nil, every byte read from src is also written to it
// in the same pass, so a caller can feed the bytes onward (for example
// to a blockDigester) without a second read of src or dst.
func copyFileStream(src, dst string, mode os.FileMode, sink io.Writer) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	w := io.Writer(out)
	if sink != nil {
		w = io.MultiWriter(out, sink)
	}
	if _, err := io.Copy(w, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
