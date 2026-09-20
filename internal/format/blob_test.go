package format

import "testing"

func testBlob() Blob {
	var id1, id2 [32]byte
	for i := range id1 {
		id1[i] = byte(i)
	}
	for i := range id2 {
		id2[i] = byte((i*7 + 3) % 256)
	}
	entries := []BlobEntry{
		{ContentID: id1, Length: 4096, FileOffset: 0},
		{ContentID: id2, Length: 2048, FileOffset: 4096},
	}
	payloadLen := blobBodyLen + len(entries)*BlobEntryLen
	return Blob{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicBlob,
			VersionMajor: 1,
			VersionMinor: 0,
			HeaderLen:    blobFixedLen,
		},
		ObjectHeader: ObjectHeader{
			Kind:        ObjectKindBlob,
			HashAlgo:    HashAlgoSHA256,
			DigestLen:   32,
			Compression: CompressionNone,
			PayloadLen:  uint64(payloadLen),
			StoredLen:   uint64(payloadLen),
		},
		EntryCount: uint64(len(entries)),
		TotalSize:  4096 + 2048,
		EntrySize:  BlobEntryLen,
		HashAlgo:   HashAlgoSHA256,
		DigestLen:  32,
		Level:      0,
		Entries:    entries,
	}
}

func TestBlobGolden(t *testing.T) {
	b := testBlob()
	buf := make([]byte, b.EncodedLen())
	if _, err := b.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "blob.golden", buf)

	golden := readGolden(t, "blob.golden")
	var got Blob
	n, err := got.Decode(golden)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(golden) {
		t.Fatalf("decode consumed %d bytes, want %d", n, len(golden))
	}
	if got.Header != b.Header {
		t.Fatalf("header mismatch: got %+v, want %+v", got.Header, b.Header)
	}
	if got.EntryCount != b.EntryCount || got.TotalSize != b.TotalSize || got.EntrySize != b.EntrySize {
		t.Fatalf("body mismatch: got %+v, want %+v", got, b)
	}
	if len(got.Entries) != len(b.Entries) {
		t.Fatalf("entry count mismatch: got %d, want %d", len(got.Entries), len(b.Entries))
	}
	for i := range b.Entries {
		if got.Entries[i] != b.Entries[i] {
			t.Fatalf("entry %d mismatch: got %+v, want %+v", i, got.Entries[i], b.Entries[i])
		}
	}
	if got.Reserved != ([3]byte{}) {
		t.Fatalf("reserved not zero: %v", got.Reserved)
	}
}

func TestBlobDecodeIgnoresReservedByte(t *testing.T) {
	golden := readGolden(t, "blob.golden")
	buf := append([]byte(nil), golden...)
	buf[CommonHeaderLen+ObjectHeaderLen+21] = 0xFF // the body's reserved byte

	var got Blob
	if _, err := got.Decode(buf); err != nil {
		t.Fatalf("decode nonzero reserved byte: %v", err)
	}
	if got.Reserved[0] != 0xFF {
		t.Fatalf("reserved byte not preserved: %v", got.Reserved)
	}
	b := testBlob()
	if got.EntryCount != b.EntryCount || got.TotalSize != b.TotalSize {
		t.Fatalf("body mismatch: got %+v, want %+v", got, b)
	}
}

func TestBlobDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "blob.golden")
	var b Blob
	if _, err := b.Decode(golden[:blobFixedLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
}

func TestBlobDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "blob.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var b Blob
	if _, err := b.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestBlobDecodeRejectsLevelAboveOne(t *testing.T) {
	golden := readGolden(t, "blob.golden")
	buf := append([]byte(nil), golden...)
	// level lies after the CRC-covered bytes, so the header CRC still matches.
	buf[CommonHeaderLen+ObjectHeaderLen+20] = 2
	var b Blob
	if _, err := b.Decode(buf); err != ErrBadField {
		t.Fatalf("decode bad level: got %v, want %v", err, ErrBadField)
	}
}
