package image

import (
	"crypto/sha256"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
)

func sha256sum(b []byte) [32]byte { return sha256.Sum256(b) }

// zeroBlock is one all-zero FEC block, reused for padding past the end of
// the stream.
var zeroBlock = make([]byte, fec.BlockSize)

// blockAt returns block index blk of stream, or zeroBlock when blk falls
// past the end of stream.
func blockAt(stream []byte, blk uint64) []byte {
	start := blk * fec.BlockSize
	if start >= uint64(len(stream)) {
		return zeroBlock
	}
	end := start + fec.BlockSize
	if end > uint64(len(stream)) {
		b := make([]byte, fec.BlockSize)
		copy(b, stream[start:])
		return b
	}
	return stream[start:end]
}

// buildFEC computes the checksum column and the m parity files over
// stream, the finished FEC stream bytes. runHeaderCopy is the 2048-byte
// run header copy every parity file starts with.
func buildFEC(stream []byte, layout *fec.StreamLayout, runHeaderCopy []byte) (checksum []byte, parity [][]byte, err error) {
	codec, err := fec.NewCodec(fec.K, fec.M)
	if err != nil {
		return nil, nil, err
	}
	L := layout.StripeCount()

	checksum = make([]byte, 0, L*fec.BlockSize)
	parity = make([][]byte, fec.M)
	for j := range parity {
		p := make([]byte, fec.BlockSize, (L+1)*fec.BlockSize)
		copy(p, runHeaderCopy)
		parity[j] = p
	}

	for i := uint64(0); i < L; i++ {
		data := make([][]byte, fec.K)
		for c := 0; c < fec.K; c++ {
			data[c] = blockAt(stream, uint64(c)*L+i)
		}
		rec := fec.BuildChecksumRecord(uint32(i), data)
		buf := make([]byte, format.ChecksumRecordLen)
		if err := rec.Encode(buf); err != nil {
			return nil, nil, err
		}
		checksum = append(checksum, buf...)

		parityBlocks, err := codec.Encode(data)
		if err != nil {
			return nil, nil, err
		}
		for j := range parityBlocks {
			parity[j] = append(parity[j], parityBlocks[j]...)
		}
	}
	return checksum, parity, nil
}
