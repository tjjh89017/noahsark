package format

import (
	"bytes"
	"io"
)

// carveReadChunk is how many bytes the carver pulls from the source at a
// time while it grows its scan window.
const carveReadChunk = 64 * 1024

// carveMaxStructure bounds how large a single carved structure may grow
// before the carver gives up on it and treats the magic hit as false.
const carveMaxStructure = 64 * 1024 * 1024

// Carved is one structure a Carver found in a stream.
type Carved struct {
	// Offset is the byte offset of the structure's magic_project within
	// the stream the Carver reads.
	Offset int64
	// Value is the decoded structure, the same type Dispatch returns.
	Value any
	// Length is the number of bytes the structure occupies.
	Length int
}

// Carver scans a byte stream for every occurrence of the project magic and
// yields the structure found there, the recovery path of FORMAT.md's
// recovery-by-carving rules. It reads its source incrementally, so a large
// stream is never held in memory at once; only the bytes of the structure
// currently being cut out are buffered.
type Carver struct {
	r      io.Reader
	buf    []byte
	offset int64
	eof    bool
}

// NewCarver returns a Carver that scans r from its current position.
func NewCarver(r io.Reader) *Carver {
	return &Carver{r: r}
}

// fill grows c.buf to at least n bytes, or until the source is exhausted.
func (c *Carver) fill(n int) error {
	for len(c.buf) < n && !c.eof {
		chunk := make([]byte, carveReadChunk)
		m, err := c.r.Read(chunk)
		if m > 0 {
			c.buf = append(c.buf, chunk[:m]...)
		}
		if err != nil {
			if err == io.EOF {
				c.eof = true
				break
			}
			return err
		}
	}
	return nil
}

// drop discards the first n bytes of c.buf, advancing c.offset past them.
func (c *Carver) drop(n int) {
	c.offset += int64(n)
	c.buf = c.buf[n:]
}

// Next returns the next structure the scan finds. It returns io.EOF once
// no further candidate remains in the stream.
//
// A magic hit that does not decode, whether a false match or a structure
// this reader cannot parse, is not returned; the scan resumes at the byte
// after the hit, as FORMAT.md's carving rules require.
func (c *Carver) Next() (*Carved, error) {
	for {
		idx, err := c.findMagic()
		if err != nil {
			return nil, err
		}
		c.drop(idx)

		value, n, err := c.tryDecode()
		if err == nil {
			carved := &Carved{Offset: c.offset, Value: value, Length: n}
			c.drop(n)
			return carved, nil
		}
		// A false magic hit, or a structure that fails to decode: resume
		// scanning at the next byte after the magic that was found.
		c.drop(1)
	}
}

// findMagic returns the index of the next occurrence of ProjectMagic in
// c.buf, filling from the source as needed. It returns io.EOF when the
// stream holds no further occurrence.
func (c *Carver) findMagic() (int, error) {
	scanned := 0
	for {
		if err := c.fill(scanned + CommonHeaderLen); err != nil {
			return 0, err
		}
		if idx := bytes.Index(c.buf[scanned:], ProjectMagic[:]); idx >= 0 {
			return scanned + idx, nil
		}
		if c.eof {
			// Nothing left can hold a magic; drop everything scanned so
			// far except the tail that could still start one, in case a
			// later call resumes the search.
			keep := len(ProjectMagic) - 1
			if len(c.buf) > keep {
				c.drop(len(c.buf) - keep)
			}
			return 0, io.EOF
		}
		// Keep the trailing bytes that could be the start of a magic that
		// straddles the next read, and keep searching from there.
		scanned = max(len(c.buf)-(len(ProjectMagic)-1), 0)
	}
}

// tryDecode grows c.buf until Dispatch either succeeds, fails for a
// reason other than a short buffer, or the candidate structure has grown
// past carveMaxStructure or the source has run out of bytes.
func (c *Carver) tryDecode() (any, int, error) {
	for {
		value, n, err := Dispatch(c.buf)
		if err == nil {
			return value, n, nil
		}
		if err != ErrShort {
			return nil, 0, err
		}
		if len(c.buf) >= carveMaxStructure || c.eof {
			return nil, 0, err
		}
		if err := c.fill(len(c.buf) + carveReadChunk); err != nil {
			return nil, 0, err
		}
	}
}
