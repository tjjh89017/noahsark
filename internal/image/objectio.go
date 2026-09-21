package image

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
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
	buf, err := readObjectHeaderPrefix(f)
	if err != nil {
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

// errShortStagedHeader marks an object file too short to hold even the
// common and object header prefix. The caller knows the object's id and
// kind, so it turns this into the one damaged-staged-object error.
var errShortStagedHeader = errors.New("object file is shorter than its own header")

// readObjectHeaderPrefix reads the common and object header prefix of an
// object file from r. A file too short to hold even that prefix is a
// corrupt staging copy, not an internal error, so it says so.
func readObjectHeaderPrefix(r io.Reader) ([]byte, error) {
	buf := make([]byte, format.CommonHeaderLen+format.ObjectHeaderLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errShortStagedHeader
		}
		return nil, err
	}
	return buf, nil
}

// copyFileStream copies the whole staged object file at src to dst,
// without reading src's bytes whole into memory, and confirms in that
// same pass that the object really holds the content want names. When
// sink is not nil, every byte read from src is also written to it, so a
// caller can feed the bytes onward (for example to a blockDigester)
// without a second read of src or dst.
//
// The copy is the only pass that reads a staged chunk's payload, so it
// is also the only place a truncated or corrupt staging file can be
// caught before its bytes reach a disc. wantLen is the length the
// caller sized the object by; a file that reads a different length is
// the same kind of fault as a payload that hashes to another id.
func copyFileStream(src, dst string, mode os.FileMode, sink io.Writer, want object.ID, wantLen uint64) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	hdr, err := readObjectHeaderPrefix(in)
	if err != nil {
		if errors.Is(err, errShortStagedHeader) {
			return stagedDamaged(want, format.ObjectKindChunk)
		}
		return err
	}
	var oh format.ObjectHeader
	if err := oh.Decode(hdr[format.CommonHeaderLen:]); err != nil {
		return stagedDamaged(want, format.ObjectKindChunk)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()

	w := io.Writer(out)
	if sink != nil {
		w = io.MultiWriter(out, sink)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}

	got, payloadLen, err := copyAndHashPayload(w, in, oh.Compression, oh.StoredLen)
	if err != nil {
		return stagedDamaged(want, format.ObjectKindChunk)
	}
	if payloadLen != oh.StoredLen || uint64(len(hdr))+payloadLen != wantLen {
		return stagedDamaged(want, format.ObjectKindChunk)
	}
	if object.ID(got) != want {
		return stagedDamaged(want, format.ObjectKindChunk)
	}
	return nil
}

// copyAndHashPayload writes the rest of in to w and returns the content
// id of the payload those same bytes carry, plus how many bytes it
// copied. The hash side runs through a pipe because the payload may be
// compressed and the decompressor pulls its input: the bytes therefore
// pass a decoder that holds one chunk's window, never the whole object
// and never the whole run.
func copyAndHashPayload(w io.Writer, in io.Reader, code format.Compression, storedLen uint64) (id [32]byte, n uint64, err error) {
	type hashResult struct {
		id  [32]byte
		err error
	}
	pr, pw := io.Pipe()
	done := make(chan hashResult, 1)
	go func() {
		got, hashErr := object.HashStreamed(pr, code, storedLen)
		if hashErr != nil {
			_ = pr.CloseWithError(hashErr)
		} else {
			// Drain the rest, so the copy never blocks once the hash has
			// taken every stored byte it needs.
			_, _ = io.Copy(io.Discard, pr)
		}
		_ = pr.Close()
		done <- hashResult{id: got, err: hashErr}
	}()

	written, copyErr := io.Copy(io.MultiWriter(w, pw), in)
	_ = pw.Close()
	res := <-done
	if res.err != nil {
		return id, 0, res.err
	}
	if copyErr != nil {
		return id, 0, copyErr
	}
	return res.id, uint64(written), nil
}

// syncTree flushes root, every file below it and every directory below
// it to stable storage, and reports the first error it meets. An
// ordinary write returns once the bytes reach the page cache, and a
// file's own flush does not make the name that points at it durable, so
// the run's files and the directories that hold them both go through
// here before pack records anything about the run.
//
// One pass at the end costs far less than a flush per file as each file
// closes: the kernel has already written most of the run back by the
// time this runs, so this pass mostly waits for the tail.
func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		return syncPath(path)
	})
}

// syncPath flushes one file or directory to stable storage.
func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
