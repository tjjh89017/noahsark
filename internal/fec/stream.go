package fec

import "slices"

// StreamLayout maps the FEC stream, the run's data files padded and
// concatenated in INDEX's file order, to blocks, columns and stripes.
type StreamLayout struct {
	fileStart   []uint64 // block index each file starts at
	blockCount  uint64   // stream_blocks
	k           int
	stripeCount uint64 // L
}

// blockCountForFile returns the number of 2048-byte blocks that hold size
// bytes, the file zero-padded up to a block boundary.
func blockCountForFile(size uint64) uint64 {
	return (size + BlockSize - 1) / BlockSize
}

// NewStreamLayout builds the block, column and stripe geometry for a
// stream of files with the given sizes, in stream order, over k data
// columns.
func NewStreamLayout(fileSizes []uint64, k int) (*StreamLayout, error) {
	if k <= 0 {
		return nil, ErrShardCount
	}
	starts := make([]uint64, len(fileSizes))
	var total uint64
	for i, size := range fileSizes {
		starts[i] = total
		total += blockCountForFile(size)
	}
	var stripeCount uint64
	if total > 0 {
		stripeCount = (total + uint64(k) - 1) / uint64(k)
	}
	return &StreamLayout{
		fileStart:   starts,
		blockCount:  total,
		k:           k,
		stripeCount: stripeCount,
	}, nil
}

// BlockCount returns stream_blocks, the stream's length in blocks.
func (s *StreamLayout) BlockCount() uint64 { return s.blockCount }

// StripeCount returns L, the number of blocks in one column.
func (s *StreamLayout) StripeCount() uint64 { return s.stripeCount }

// Locate returns the file index and the byte offset inside that file
// where block starts. When block covers a file's zero padding, offset
// still points past the file's own bytes; the caller supplies zeros
// there.
func (s *StreamLayout) Locate(block uint64) (fileIndex int, offset uint64, err error) {
	if block >= s.blockCount {
		return 0, 0, ErrBlockRange
	}
	idx := 0
	for i, v := range slices.Backward(s.fileStart) {
		if v <= block {
			idx = i
			break
		}
	}
	offset = (block - s.fileStart[idx]) * BlockSize
	return idx, offset, nil
}

// Column returns the data column block belongs to: block / L.
func (s *StreamLayout) Column(block uint64) uint64 {
	return block / s.stripeCount
}

// Stripe returns the stripe block belongs to within its column: block % L.
func (s *StreamLayout) Stripe(block uint64) uint64 {
	return block % s.stripeCount
}
