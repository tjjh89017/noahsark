package fec

import (
	"math/rand"
	"runtime"
	"testing"
)

// decodePatternCount is the number of stripes that TestDecodeMemoryIsBounded
// decodes, each with its own erasure pattern. An inverted matrix of the
// full geometry takes about 60 to 100 KB, thus a codec that keeps one for
// each pattern grows by about 30 to 50 MB over this many stripes.
const decodePatternCount = 500

// decodeHeapBudget is the heap growth that TestDecodeMemoryIsBounded
// accepts after all the stripes are decoded.
const decodeHeapBudget = 16 << 20

func liveHeap() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse
}

// TestDecodeMemoryIsBounded decodes many stripes, each with a different
// set of m erased data shards, through one codec, as heal does over a
// damaged disc. The live heap after the decodes must not grow with the
// number of erasure patterns.
func TestDecodeMemoryIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes many stripes")
	}
	codec, err := NewCodec(K, M)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(7))
	data := randomStripe(rng, K, 64)
	parity, err := codec.Encode(data)
	if err != nil {
		t.Fatal(err)
	}

	before := liveHeap()
	for range decodePatternCount {
		shards := allShards(data, parity)
		for _, idx := range rng.Perm(K)[:M] {
			delete(shards, idx)
		}
		if _, _, err := codec.Decode(shards); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	}
	after := liveHeap()
	runtime.KeepAlive(codec)

	var growth uint64
	if after > before {
		growth = after - before
	}
	t.Logf("heap growth after %d erasure patterns: %d bytes", decodePatternCount, growth)
	if growth > decodeHeapBudget {
		t.Fatalf("heap grew by %d bytes over %d erasure patterns, want at most %d", growth, decodePatternCount, decodeHeapBudget)
	}
}

// BenchmarkDecodeSamePattern decodes full-size stripes with one fixed
// erasure pattern, the case where a cache of inverted matrices helps.
func BenchmarkDecodeSamePattern(b *testing.B) {
	codec, err := NewCodec(K, M)
	if err != nil {
		b.Fatal(err)
	}
	rng := rand.New(rand.NewSource(8))
	data := randomStripe(rng, K, BlockSize)
	parity, err := codec.Encode(data)
	if err != nil {
		b.Fatal(err)
	}
	erase := rng.Perm(K)[:M]
	b.SetBytes(int64(K * BlockSize))
	for b.Loop() {
		shards := allShards(data, parity)
		for _, idx := range erase {
			delete(shards, idx)
		}
		if _, _, err := codec.Decode(shards); err != nil {
			b.Fatal(err)
		}
	}
}
