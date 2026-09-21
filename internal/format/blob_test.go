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
		{ContentID: id1, Length: 4096},
		{ContentID: id2, Length: 2048},
	}
	payloadLen := blobBodyLen + len(entries)*BlobEntryLen
	return Blob{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicBlob,
			VersionMajor: 1,
			HeaderLen:    blobFixedLen,
		},
		ObjectHeader: ObjectHeader{
			Kind:        ObjectKindBlob,
			HashAlgo:    HashAlgoSHA256,
			Compression: CompressionNone,
			PayloadLen:  uint64(payloadLen),
			StoredLen:   uint64(payloadLen),
		},
		EntryCount: uint64(len(entries)),
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
	if got.EntryCount != b.EntryCount {
		t.Fatalf("entry_count mismatch: got %d, want %d", got.EntryCount, b.EntryCount)
	}
	if len(got.Entries) != len(b.Entries) {
		t.Fatalf("entry count mismatch: got %d, want %d", len(got.Entries), len(b.Entries))
	}
	for i := range b.Entries {
		if got.Entries[i] != b.Entries[i] {
			t.Fatalf("entry %d mismatch: got %+v, want %+v", i, got.Entries[i], b.Entries[i])
		}
	}
}

// TestBlobOffsetsAreRunningSums checks the rule that replaced the stored
// offset: the offset of an entry is the sum of the lengths before it.
func TestBlobOffsetsAreRunningSums(t *testing.T) {
	b := testBlob()
	var off uint64
	want := []uint64{0, 4096}
	for i, e := range b.Entries {
		if off != want[i] {
			t.Fatalf("entry %d offset: got %d, want %d", i, off, want[i])
		}
		off += e.Length
	}
	if off != 6144 {
		t.Fatalf("file size: got %d, want 6144", off)
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
