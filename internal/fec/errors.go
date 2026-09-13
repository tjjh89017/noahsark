package fec

import "errors"

// ErrTooFewShards reports fewer than k surviving shards, so decode cannot
// reconstruct the stripe.
var ErrTooFewShards = errors.New("fec: fewer than k shards available")

// ErrSingular reports a matrix Gaussian elimination could not invert.
var ErrSingular = errors.New("fec: matrix is singular")

// ErrBlockLen reports blocks of unequal length inside one stripe.
var ErrBlockLen = errors.New("fec: block lengths differ")

// ErrShardCount reports a data or parity shard slice of the wrong length.
var ErrShardCount = errors.New("fec: wrong shard count")

// ErrBlockRange reports a stream block index outside the stream.
var ErrBlockRange = errors.New("fec: block index out of range")

// ErrDigestCount reports a block count that does not match a checksum
// record's digest count.
var ErrDigestCount = errors.New("fec: block count does not match digest count")
