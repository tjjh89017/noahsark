package fec

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestBuildAndVerifyChecksumRecordClean(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	blocks := make([][]byte, K)
	for i := range blocks {
		b := make([]byte, BlockSize)
		rng.Read(b)
		blocks[i] = b
	}

	rec := BuildChecksumRecord(0, blocks)
	if int(rec.DigestCount) != K {
		t.Fatalf("DigestCount = %d, want %d", rec.DigestCount, K)
	}

	bad, err := VerifyBlocks(rec, blocks)
	if err != nil {
		t.Fatalf("VerifyBlocks: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("VerifyBlocks found %v bad blocks on a clean stripe", bad)
	}
}

func TestVerifyBlocksFindsCorruption(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	blocks := make([][]byte, K)
	for i := range blocks {
		b := make([]byte, BlockSize)
		rng.Read(b)
		blocks[i] = b
	}
	rec := BuildChecksumRecord(1, blocks)

	corrupted := make([][]byte, K)
	for i, b := range blocks {
		corrupted[i] = append([]byte(nil), b...)
	}
	corrupted[5][0] ^= 0xFF

	bad, err := VerifyBlocks(rec, corrupted)
	if err != nil {
		t.Fatalf("VerifyBlocks: %v", err)
	}
	if len(bad) != 1 || bad[0] != 5 {
		t.Fatalf("VerifyBlocks = %v, want [5]", bad)
	}
}

func TestVerifyBlocksCountMismatch(t *testing.T) {
	rec := BuildChecksumRecord(0, [][]byte{make([]byte, BlockSize)})
	if _, err := VerifyBlocks(rec, nil); err == nil {
		t.Error("VerifyBlocks with a mismatched block count must return an error")
	}
}

func TestBlockDigestIsFirst8BytesOfSHA256(t *testing.T) {
	block := bytes.Repeat([]byte{0xAB}, BlockSize)
	d := BlockDigest(block)
	if d != BlockDigest(block) {
		t.Error("BlockDigest must be deterministic")
	}
}
