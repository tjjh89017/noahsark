package format

import "testing"

func testChecksumRecord() ChecksumRecord {
	digests := make([][8]byte, 3)
	for i := range digests {
		for j := range digests[i] {
			digests[i][j] = byte(i + 1)
		}
	}
	return ChecksumRecord{
		StripeIndex: 5,
		DigestCount: 3,
		DigestBytes: 8,
		HashAlgo:    HashAlgoSHA256,
		Digests:     digests,
	}
}

func TestChecksumRecordGolden(t *testing.T) {
	r := testChecksumRecord()
	buf := make([]byte, ChecksumRecordLen)
	if err := r.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "checksum.golden", buf)

	golden := readGolden(t, "checksum.golden")
	var got ChecksumRecord
	if err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.StripeIndex != r.StripeIndex || got.DigestCount != r.DigestCount ||
		got.DigestBytes != r.DigestBytes || got.HashAlgo != r.HashAlgo {
		t.Fatalf("decoded fields mismatch: got %+v, want %+v", got, r)
	}
	for i := range r.Digests {
		if got.Digests[i] != r.Digests[i] {
			t.Errorf("digest %d mismatch: got %x, want %x", i, got.Digests[i], r.Digests[i])
		}
	}
	if got.Reserved != ([180]byte{}) {
		t.Errorf("reserved not zero: %x", got.Reserved)
	}
}

func TestChecksumRecordDecodeIgnoresReservedFields(t *testing.T) {
	golden := readGolden(t, "checksum.golden")
	buf := append([]byte(nil), golden...)
	buf[checksumHeaderLen+3*ChecksumDigestSize] = 0xFF   // past digestCount, inside the digests area
	buf[checksumHeaderLen+ChecksumDigestsAreaLen] = 0xFF // the trailing reserved area

	var got ChecksumRecord
	if err := got.Decode(buf); err != nil {
		t.Fatalf("decode nonzero reserved fields: %v", err)
	}
	r := testChecksumRecord()
	if got.StripeIndex != r.StripeIndex || got.DigestCount != r.DigestCount {
		t.Fatalf("decoded fields mismatch: got %+v, want %+v", got, r)
	}
	for i := range r.Digests {
		if got.Digests[i] != r.Digests[i] {
			t.Errorf("digest %d mismatch: got %x, want %x", i, got.Digests[i], r.Digests[i])
		}
	}
	if got.Reserved[0] != 0xFF {
		t.Fatalf("reserved byte not preserved: %x", got.Reserved)
	}
}

func TestChecksumRecordDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "checksum.golden")
	var r ChecksumRecord
	if err := r.Decode(golden[:ChecksumRecordLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
	if err := r.Encode(make([]byte, ChecksumRecordLen-1)); err != ErrShort {
		t.Fatalf("encode short buffer: got %v, want %v", err, ErrShort)
	}
}

func TestChecksumRecordDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "checksum.golden")
	buf := append([]byte(nil), golden...)
	buf[0] = 'X'
	var r ChecksumRecord
	if err := r.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestChecksumRecordDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "checksum.golden")
	buf := append([]byte(nil), golden...)
	buf[8] ^= 0xFF
	var r ChecksumRecord
	if err := r.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad crc: got %v, want %v", err, ErrCRC)
	}
}
