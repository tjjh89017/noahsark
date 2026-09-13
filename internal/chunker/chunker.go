// Package chunker implements FastCDC content-defined chunking.
package chunker

import "io"

// Profile holds the FastCDC parameters of one chunker profile. Min, Avg and
// Max are byte counts. MaskS and MaskL are the small and large masks used
// below and above the Avg offset.
type Profile struct {
	Min   int
	Avg   int
	Max   int
	MaskS uint64
	MaskL uint64
}

// DefaultProfile is P4, the default chunker profile: min 1 MiB, avg 4 MiB,
// max 16 MiB, normalization level 2.
var DefaultProfile = Profile{
	Min:   1 << 20,
	Avg:   4 << 20,
	Max:   16 << 20,
	MaskS: 0xEEEEEEEE00000000,
	MaskL: 0xDADADADA00000000,
}

// Chunker splits a byte stream into content-defined chunks.
type Chunker struct {
	r       io.Reader
	profile Profile
	buf     []byte // working buffer, reused across calls, sized to Max
	filled  int    // valid bytes at the front of buf
	eof     bool   // the reader has returned io.EOF
}

// New returns a Chunker that reads r and cuts chunks under profile.
func New(r io.Reader, profile Profile) *Chunker {
	return &Chunker{
		r:       r,
		profile: profile,
		buf:     make([]byte, profile.Max),
	}
}

// Next returns the next chunk. It returns io.EOF once the input is
// exhausted and no chunk remains. The returned slice is a copy the caller
// owns; it stays valid across later calls to Next.
func (c *Chunker) Next() ([]byte, error) {
	if err := c.fill(); err != nil {
		return nil, err
	}
	if c.filled == 0 {
		return nil, io.EOF
	}

	cut := cutPoint(c.buf[:c.filled], c.profile)

	chunk := make([]byte, cut)
	copy(chunk, c.buf[:cut])

	remaining := c.filled - cut
	copy(c.buf, c.buf[cut:c.filled])
	c.filled = remaining

	return chunk, nil
}

// fill reads from the reader until the buffer holds Max bytes or the
// reader is exhausted. It never reads fewer bytes for the same input,
// whatever the caller's Read implementation returns per call, so the
// number of bytes examined before a cut decision does not depend on the
// reader's read pattern.
func (c *Chunker) fill() error {
	for c.filled < c.profile.Max && !c.eof {
		n, err := c.r.Read(c.buf[c.filled:c.profile.Max])
		c.filled += n
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

// cutPoint applies the FastCDC cut rule to data, the bytes available from
// the start of the current chunk. It returns the length of that chunk.
//
// The loop starts at offset Min and never hashes an earlier byte, so a
// chunk is never shorter than Min unless the input itself is shorter than
// Min, and a cut is never reported before offset Min+1.
func cutPoint(data []byte, p Profile) int {
	n := len(data)
	if n <= p.Min {
		return n
	}

	var fp uint64
	i := p.Min
	for i < n {
		fp = (fp << 1) + gearTable[data[i]]
		if i < p.Avg {
			if fp&p.MaskS == 0 {
				return i + 1
			}
		} else {
			if fp&p.MaskL == 0 {
				return i + 1
			}
		}
		i++
		if i >= p.Max {
			return p.Max
		}
	}
	return n
}
