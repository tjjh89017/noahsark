package fec

import (
	"crypto/sha256"

	"github.com/tjjh89017/noahsark/internal/format"
)

// BlockDigest returns the first 8 bytes of the SHA-256 digest of one
// stream block.
func BlockDigest(block []byte) [8]byte {
	sum := sha256.Sum256(block)
	var d [8]byte
	copy(d[:], sum[:8])
	return d
}

// BuildChecksumRecord builds the checksum column record for one stripe
// from its k data blocks, in data column order.
func BuildChecksumRecord(stripeIndex uint32, dataBlocks [][]byte) *format.ChecksumRecord {
	digests := make([][8]byte, len(dataBlocks))
	for i, b := range dataBlocks {
		digests[i] = BlockDigest(b)
	}
	return &format.ChecksumRecord{
		StripeIndex: stripeIndex,
		DigestCount: uint16(len(dataBlocks)),
		DigestBytes: format.ChecksumDigestSize,
		HashAlgo:    format.HashAlgoSHA256,
		Digests:     digests,
	}
}

// VerifyBlocks compares dataBlocks against rec's digests, in data column
// order, and returns the indices of blocks whose digest does not match.
func VerifyBlocks(rec *format.ChecksumRecord, dataBlocks [][]byte) ([]int, error) {
	if len(dataBlocks) != len(rec.Digests) {
		return nil, ErrDigestCount
	}
	var bad []int
	for i, b := range dataBlocks {
		if BlockDigest(b) != rec.Digests[i] {
			bad = append(bad, i)
		}
	}
	return bad, nil
}
