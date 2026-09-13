package image

import (
	"bufio"
	"crypto/sha256"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/progress"
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

// blockDigester takes the FEC stream's bytes in stream order, one file
// at a time, and computes the SHA-256 block digest of every 2048-byte
// block as its bytes complete. It is the write side of the fused pass:
// the object placement loop and the fixed-file writes feed it as they
// write each file's bytes, so every stream byte is read once for
// hashing, during the same pass that writes it to the run tree.
//
// The checksum column groups digests by data column (blocks c*L..c*L+L-1
// for column c, see FORMAT.md's Forward error correction section), while
// a digester receives blocks in stream order. A stripe's k digests are
// therefore not known as a set until nearly the whole stream has been
// fed, so blockDigester holds every digest (8 bytes each, not the block
// data) rather than one stripe's worth; buildFECToDisk still reads the
// actual block bytes needed for parity back from the placed files,
// through columnCursor, one stripe at a time.
type blockDigester struct {
	digests []([8]byte)
	next    uint64
	partial []byte
	sem     chan struct{}
	wg      sync.WaitGroup
}

// zeroBlockDigest is the digest of one all-zero block: the padding
// columnCursor.readBlock returns past the stream's own bytes, and past
// the last column's own data when stream_blocks is not a multiple of
// fec.K's stripe count.
var zeroBlockDigest = fec.BlockDigest(make([]byte, fec.BlockSize))

// newBlockDigester returns a digester sized for totalBlocks slots,
// hashing across a worker pool bounded by runtime.NumCPU so disk I/O
// and SHA-256 overlap instead of running single-threaded. totalBlocks
// must cover every column's full L blocks (fec.K*L), not just the
// stream's own block count, since the last column's padding past the
// stream's own bytes still needs a digest.
func newBlockDigester(totalBlocks uint64) *blockDigester {
	workers := max(runtime.NumCPU(), 1)
	return &blockDigester{
		digests: make([][8]byte, totalBlocks),
		partial: make([]byte, 0, fec.BlockSize),
		sem:     make(chan struct{}, workers),
	}
}

// Write implements io.Writer so a digester can sit behind an
// io.MultiWriter alongside a file copy, or take a fixed row's bytes
// directly. It never returns an error.
func (d *blockDigester) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := fec.BlockSize - len(d.partial)
		take := min(room, len(p))
		d.partial = append(d.partial, p[:take]...)
		p = p[take:]
		if len(d.partial) == fec.BlockSize {
			d.dispatch(d.partial)
			d.partial = make([]byte, 0, fec.BlockSize)
		}
	}
	return n, nil
}

// FinishFile closes out the block in progress, zero-padding it to a
// full block. It must run once after every stream file's bytes are fed,
// whether or not the file's own length lands on a block boundary,
// because each stream file starts its own fresh block.
func (d *blockDigester) FinishFile() {
	if len(d.partial) == 0 {
		return
	}
	block := make([]byte, fec.BlockSize)
	copy(block, d.partial)
	d.dispatch(block)
	d.partial = d.partial[:0]
}

func (d *blockDigester) dispatch(block []byte) {
	idx := d.next
	d.next++
	d.sem <- struct{}{}
	d.wg.Go(func() {
		defer func() { <-d.sem }()
		d.digests[idx] = fec.BlockDigest(block)
	})
}

// Wait blocks until every block dispatched so far has been digested.
// The caller must call Wait before reading d.digests.
func (d *blockDigester) Wait() { d.wg.Wait() }

// PadRemaining fills every digest slot past the last one fed with
// zeroBlockDigest, the digest columnCursor's own zero padding would
// produce there. The caller must call Wait before PadRemaining.
func (d *blockDigester) PadRemaining() {
	for i := d.next; i < uint64(len(d.digests)); i++ {
		d.digests[i] = zeroBlockDigest
	}
}

// stripeRead is one stripe's k freshly read data blocks, produced by
// the read-ahead goroutine in buildFECToDisk.
type stripeRead struct {
	index uint64
	data  [][]byte
}

// buildFECToDisk computes the checksum column and the m parity files
// over the FEC stream sources describe, and writes them straight to
// checksumPath and parityPaths, one stripe at a time. It holds at most
// a couple of stripes of k data blocks and the m parity blocks in
// memory, never the stream itself. runHeaderCopy is the 2048-byte run
// header copy every parity file starts with. digests, when not nil, is
// the stream's block digests precomputed while the stream's bytes were
// placed (see blockDigester); passing nil makes this pass compute each
// stripe's digests itself, as if no earlier pass had read the stream.
// prog reports stripes encoded; a nil prog reports nothing.
func buildFECToDisk(sources []streamSource, layout *fec.StreamLayout, runHeaderCopy []byte, checksumPath string, parityPaths []string, digests [][8]byte, prog *progress.Reporter) error {
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

	// The read-ahead goroutine fills one stripe while the main loop
	// encodes and writes the previous one, so disk reads for parity and
	// the RS encode overlap. The channel's capacity of 1 keeps at most
	// two stripes of raw data resident: the one being encoded and the
	// one being read.
	reads := make(chan stripeRead, 1)
	readErr := make(chan error, 1)
	go func() {
		defer close(reads)
		for i := range L {
			data := make([][]byte, fec.K)
			for c := range fec.K {
				buf := make([]byte, fec.BlockSize)
				if err := cols[c].readBlock(buf); err != nil {
					readErr <- err
					return
				}
				data[c] = buf
			}
			reads <- stripeRead{index: i, data: data}
		}
	}()

	recBuf := make([]byte, format.ChecksumRecordLen)
	prog.Start("pack: fec stripes encoded", int64(L))
	for sr := range reads {
		var rec *format.ChecksumRecord
		if digests != nil {
			stripeDigests := make([][8]byte, fec.K)
			for c := range fec.K {
				stripeDigests[c] = digests[uint64(c)*L+sr.index]
			}
			rec = fec.BuildChecksumRecordFromDigests(uint32(sr.index), stripeDigests)
		} else {
			rec = fec.BuildChecksumRecord(uint32(sr.index), sr.data)
		}
		if err := rec.Encode(recBuf); err != nil {
			return err
		}
		if _, err := checksumW.Write(recBuf); err != nil {
			return err
		}
		parityBlocks, err := codec.Encode(sr.data)
		if err != nil {
			return err
		}
		for j := range parityBlocks {
			if _, err := parityW[j].Write(parityBlocks[j]); err != nil {
				return err
			}
		}
		prog.Add(1)
	}
	select {
	case err := <-readErr:
		if err != nil {
			return err
		}
	default:
	}
	prog.Done()

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
