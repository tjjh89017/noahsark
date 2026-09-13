package fec

import (
	"math/rand"
	"testing"
)

func randomStripe(rng *rand.Rand, k, blockLen int) [][]byte {
	data := make([][]byte, k)
	for i := range data {
		b := make([]byte, blockLen)
		rng.Read(b)
		data[i] = b
	}
	return data
}

// allShards concatenates encoded data and parity into one k+m shard set,
// indexed 0..k-1 for data and k..k+m-1 for parity.
func allShards(data, parity [][]byte) map[int][]byte {
	shards := make(map[int][]byte, len(data)+len(parity))
	for i, d := range data {
		shards[i] = d
	}
	for j, p := range parity {
		shards[len(data)+j] = p
	}
	return shards
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	codec, err := NewCodec(K, M)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	rng := rand.New(rand.NewSource(42))
	data := randomStripe(rng, K, 64)

	parity, err := codec.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(parity) != M {
		t.Fatalf("got %d parity shards, want %d", len(parity), M)
	}

	shards := allShards(data, parity)

	// Erase a random set of up to M shards.
	total := K + M
	perm := rng.Perm(total)
	erase := perm[:M]
	for _, idx := range erase {
		delete(shards, idx)
	}

	gotData, gotParity, err := codec.Decode(shards)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i := range data {
		if string(gotData[i]) != string(data[i]) {
			t.Fatalf("data shard %d mismatch after recovery", i)
		}
	}
	for j := range parity {
		if string(gotParity[j]) != string(parity[j]) {
			t.Fatalf("parity shard %d mismatch after recovery", j)
		}
	}
}

func TestDecodeTooManyErasures(t *testing.T) {
	codec, err := NewCodec(K, M)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	rng := rand.New(rand.NewSource(7))
	data := randomStripe(rng, K, 32)
	parity, err := codec.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	shards := allShards(data, parity)

	total := K + M
	perm := rng.Perm(total)
	erase := perm[:M+1] // one erasure past what the code tolerates
	for _, idx := range erase {
		delete(shards, idx)
	}

	if _, _, err := codec.Decode(shards); err == nil {
		t.Error("Decode with more than m erasures must return an error")
	}
}

func TestWorkedExampleEncode(t *testing.T) {
	// The k=3, m=2 example from FORMAT.md. Not a version 1 geometry; it
	// exists to check the arithmetic by hand.
	codec, err := NewCodec(3, 2)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	data := [][]byte{{0x53}, {0xA7}, {0x0C}}
	parity, err := codec.Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if parity[0][0] != 0xE0 {
		t.Errorf("p_0 = %#x, want 0xE0", parity[0][0])
	}
	if parity[1][0] != 0xAD {
		t.Errorf("p_1 = %#x, want 0xAD", parity[1][0])
	}

	// Lose d_0 and d_1; recover from d_2, p_0, p_1.
	shards := map[int][]byte{
		2: data[2],
		3: parity[0],
		4: parity[1],
	}
	gotData, _, err := codec.Decode(shards)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if gotData[0][0] != 0x53 || gotData[1][0] != 0xA7 {
		t.Errorf("recovered d = (%#x, %#x), want (0x53, 0xA7)", gotData[0][0], gotData[1][0])
	}
}

func BenchmarkEncodeStripe(b *testing.B) {
	codec, err := NewCodec(K, M)
	if err != nil {
		b.Fatalf("NewCodec: %v", err)
	}
	rng := rand.New(rand.NewSource(1))
	data := randomStripe(rng, K, BlockSize)

	b.ResetTimer()
	b.SetBytes(int64(K * BlockSize))
	for i := 0; i < b.N; i++ {
		if _, err := codec.Encode(data); err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}
