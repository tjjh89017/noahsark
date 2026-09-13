package image

import (
	"bufio"
	"crypto/sha256"
	"io"
	"os"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
)

func sha256sum(b []byte) [32]byte { return sha256.Sum256(b) }

// streamSource is one FEC stream file, in stream order: either its bytes
// already in memory (a small, fixed-size structure) or the path of the
// file to read its block range from on disk.
type streamSource struct {
	path string
	data []byte
	size uint64
}

// columnCursor reads one FEC stream data column's blocks in order,
// advancing forward only. It opens a source file only while it is
// reading that file's own blocks, so it never holds more than one open
// file handle and one block of data.
type columnCursor struct {
	sources []streamSource
	idx     int
	blk     uint64
	f       *os.File
}

// newColumnCursor returns a cursor positioned at startBlock, the first
// block of one FEC data column.
func newColumnCursor(sources []streamSource, startBlock uint64) *columnCursor {
	c := &columnCursor{sources: sources}
	rem := startBlock
	for c.idx < len(sources) {
		n := blockCount(c.sources[c.idx].size)
		if rem < n {
			c.blk = rem
			return c
		}
		rem -= n
		c.idx++
	}
	return c
}

// readBlock fills buf, which must be fec.BlockSize bytes long, with the
// cursor's next block and advances the cursor. A block past the last
// source file, or past a source file's own length, reads as zero.
func (c *columnCursor) readBlock(buf []byte) error {
	for c.idx < len(c.sources) {
		src := c.sources[c.idx]
		n := blockCount(src.size)
		if c.blk >= n {
			if err := c.closeCurrent(); err != nil {
				return err
			}
			c.idx++
			c.blk = 0
			continue
		}
		start := c.blk * fec.BlockSize
		for i := range buf {
			buf[i] = 0
		}
		if src.data != nil {
			if start < uint64(len(src.data)) {
				end := min(start+fec.BlockSize, uint64(len(src.data)))
				copy(buf, src.data[start:end])
			}
		} else if start < src.size {
			if c.f == nil {
				f, err := os.Open(src.path)
				if err != nil {
					return err
				}
				c.f = f
			}
			if _, err := c.f.ReadAt(buf, int64(start)); err != nil && err != io.EOF {
				return err
			}
		}
		c.blk++
		return nil
	}
	for i := range buf {
		buf[i] = 0
	}
	return nil
}

func (c *columnCursor) closeCurrent() error {
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}

// buildFECToDisk computes the checksum column and the m parity files
// over the FEC stream sources describe, and writes them straight to
// checksumPath and parityPaths, one stripe at a time. It holds at most
// one stripe of k data blocks and the m parity blocks in memory, never
// the stream itself. runHeaderCopy is the 2048-byte run header copy
// every parity file starts with.
func buildFECToDisk(sources []streamSource, layout *fec.StreamLayout, runHeaderCopy []byte, checksumPath string, parityPaths []string) error {
	codec, err := fec.NewCodec(fec.K, fec.M)
	if err != nil {
		return err
	}
	L := layout.StripeCount()

	cols := make([]*columnCursor, fec.K)
	for c := range fec.K {
		cols[c] = newColumnCursor(sources, uint64(c)*L)
	}
	defer func() {
		for _, c := range cols {
			_ = c.closeCurrent()
		}
	}()

	checksumFile, err := os.Create(checksumPath)
	if err != nil {
		return err
	}
	defer func() { _ = checksumFile.Close() }()
	checksumW := bufio.NewWriter(checksumFile)

	parityW := make([]*bufio.Writer, fec.M)
	for j, p := range parityPaths {
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w := bufio.NewWriter(f)
		if _, err := w.Write(runHeaderCopy); err != nil {
			return err
		}
		parityW[j] = w
	}

	data := make([][]byte, fec.K)
	for c := range data {
		data[c] = make([]byte, fec.BlockSize)
	}
	recBuf := make([]byte, format.ChecksumRecordLen)

	for i := range L {
		for c := range fec.K {
			if err := cols[c].readBlock(data[c]); err != nil {
				return err
			}
		}
		rec := fec.BuildChecksumRecord(uint32(i), data)
		if err := rec.Encode(recBuf); err != nil {
			return err
		}
		if _, err := checksumW.Write(recBuf); err != nil {
			return err
		}
		parityBlocks, err := codec.Encode(data)
		if err != nil {
			return err
		}
		for j := range parityBlocks {
			if _, err := parityW[j].Write(parityBlocks[j]); err != nil {
				return err
			}
		}
	}

	if err := checksumW.Flush(); err != nil {
		return err
	}
	for _, w := range parityW {
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}
