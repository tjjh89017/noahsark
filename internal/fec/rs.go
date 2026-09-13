// Package fec implements the rs255-gf8 forward error correction code:
// the Cauchy code, the stream-to-stripe mapping, and the checksum column.
// Codec encodes and decodes with the klauspost/reedsolomon backend built
// with the Cauchy matrix option, so its output matches this package's own
// GF(2^8) arithmetic byte for byte; see docs/decisions.md. Build with the
// fecref tag to add the pure Go reference implementation and its
// cross-check test.
package fec

import "github.com/klauspost/reedsolomon"

// K and M are the version 1 code geometry: 231 data shards and 23 parity
// shards per stripe.
const (
	K = 231
	M = 23
	// BlockSize is the size of one shard: one 2048-byte block of the FEC
	// stream.
	BlockSize = 2048
)

// Codec holds the encoder for one (k, m) pair and encodes or decodes
// stripes against it.
type Codec struct {
	k, m int
	enc  reedsolomon.Encoder
}

// NewCodec builds a Codec for k data shards and m parity shards.
func NewCodec(k, m int) (*Codec, error) {
	enc, err := reedsolomon.New(k, m, reedsolomon.WithCauchyMatrix())
	if err != nil {
		return nil, err
	}
	return &Codec{k: k, m: m, enc: enc}, nil
}

// K returns the codec's data shard count.
func (c *Codec) K() int { return c.k }

// M returns the codec's parity shard count.
func (c *Codec) M() int { return c.m }

// Encode computes the m parity shards for one stripe of k data shards.
// Every shard must be the same length. Encode does not modify data.
func (c *Codec) Encode(data [][]byte) ([][]byte, error) {
	if len(data) != c.k {
		return nil, ErrShardCount
	}
	blockLen := len(data[0])
	for _, d := range data {
		if len(d) != blockLen {
			return nil, ErrBlockLen
		}
	}

	shards := make([][]byte, c.k+c.m)
	copy(shards, data)
	for j := c.k; j < c.k+c.m; j++ {
		shards[j] = make([]byte, blockLen)
	}
	if err := c.enc.Encode(shards); err != nil {
		return nil, err
	}
	return shards[c.k:], nil
}

// Decode reconstructs the k data shards and the m parity shards of a
// stripe from any k of its k+m shards. shards keys are 0..k-1 for data
// shards and k..k+m-1 for parity shards.
func (c *Codec) Decode(shards map[int][]byte) (data [][]byte, parity [][]byte, err error) {
	if len(shards) < c.k {
		return nil, nil, ErrTooFewShards
	}

	total := c.k + c.m
	all := make([][]byte, total)
	blockLen := -1
	for idx, s := range shards {
		if idx < 0 || idx >= total {
			return nil, nil, ErrShardCount
		}
		if blockLen == -1 {
			blockLen = len(s)
		} else if len(s) != blockLen {
			return nil, nil, ErrBlockLen
		}
		all[idx] = s
	}

	if err := c.enc.Reconstruct(all); err != nil {
		return nil, nil, ErrTooFewShards
	}

	data = all[:c.k]
	parity = all[c.k:]
	return data, parity, nil
}
