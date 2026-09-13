package fec

import "sort"

// K and M are the version 1 code geometry: 231 data shards and 23 parity
// shards per stripe.
const (
	K = 231
	M = 23
	// BlockSize is the size of one shard: one 2048-byte block of the FEC
	// stream.
	BlockSize = 2048
)

// Codec holds the Cauchy matrix for one (k, m) pair and encodes or
// decodes stripes against it.
type Codec struct {
	k, m   int
	matrix [][]byte // m x k
}

// NewCodec builds a Codec for k data shards and m parity shards.
func NewCodec(k, m int) (*Codec, error) {
	matrix, err := BuildCauchyMatrix(k, m)
	if err != nil {
		return nil, err
	}
	return &Codec{k: k, m: m, matrix: matrix}, nil
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

	parity := make([][]byte, c.m)
	for j := 0; j < c.m; j++ {
		p := make([]byte, blockLen)
		row := c.matrix[j]
		for i := 0; i < c.k; i++ {
			coeff := row[i]
			if coeff == 0 {
				continue
			}
			d := data[i]
			for t := range blockLen {
				p[t] ^= Mul(coeff, d[t])
			}
		}
		parity[j] = p
	}
	return parity, nil
}

// Decode reconstructs the k data shards and the m parity shards of a
// stripe from any k of its k+m shards. shards keys are 0..k-1 for data
// shards and k..k+m-1 for parity shards. When more than k shards are
// present, Decode uses the k with the lowest index, the normative choice
// so healing the same stripe always reproduces the same output.
func (c *Codec) Decode(shards map[int][]byte) (data [][]byte, parity [][]byte, err error) {
	if len(shards) < c.k {
		return nil, nil, ErrTooFewShards
	}

	indices := make([]int, 0, len(shards))
	for idx := range shards {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	indices = indices[:c.k]

	blockLen := len(shards[indices[0]])
	square := make([][]byte, c.k)
	present := make([][]byte, c.k)
	for row, idx := range indices {
		s := shards[idx]
		if len(s) != blockLen {
			return nil, nil, ErrBlockLen
		}
		present[row] = s
		if idx < c.k {
			unit := make([]byte, c.k)
			unit[idx] = 1
			square[row] = unit
		} else {
			square[row] = c.matrix[idx-c.k]
		}
	}

	invMatrix, err := InvertMatrix(square)
	if err != nil {
		return nil, nil, err
	}

	data = make([][]byte, c.k)
	for i := 0; i < c.k; i++ {
		out := make([]byte, blockLen)
		invRow := invMatrix[i]
		for row := 0; row < c.k; row++ {
			coeff := invRow[row]
			if coeff == 0 {
				continue
			}
			s := present[row]
			for t := range blockLen {
				out[t] ^= Mul(coeff, s[t])
			}
		}
		data[i] = out
	}

	parity, err = c.Encode(data)
	if err != nil {
		return nil, nil, err
	}
	return data, parity, nil
}
