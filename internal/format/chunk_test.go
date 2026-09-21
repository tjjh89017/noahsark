package format

import "testing"

func testChunk() Chunk {
	payload := []byte("hello world chunk payload")
	return Chunk{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicChunk,
			VersionMajor: 1,
			HeaderLen:    chunkFixedLen,
		},
		ObjectHeader: ObjectHeader{
			Kind:        ObjectKindChunk,
			HashAlgo:    HashAlgoSHA256,
			Compression: CompressionNone,
			PayloadLen:  uint64(len(payload)),
			StoredLen:   uint64(len(payload)),
		},
		Payload: payload,
	}
}

func TestChunkGolden(t *testing.T) {
	c := testChunk()
	buf := make([]byte, c.EncodedLen())
	if _, err := c.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "chunk.golden", buf)

	golden := readGolden(t, "chunk.golden")
	var got Chunk
	n, err := got.Decode(golden)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(golden) {
		t.Fatalf("decode consumed %d bytes, want %d", n, len(golden))
	}
	if got.Header != c.Header {
		t.Fatalf("header mismatch: got %+v, want %+v", got.Header, c.Header)
	}
	if got.ObjectHeader.HeaderCRC32C == 0 {
		t.Fatalf("header_crc32c not set")
	}
	if string(got.Payload) != string(c.Payload) {
		t.Fatalf("payload mismatch: got %q, want %q", got.Payload, c.Payload)
	}
	if got.ObjectHeader.ReservedU8 != 0 || got.ObjectHeader.ReservedA != ([4]byte{}) || got.ObjectHeader.ReservedU32 != 0 {
		t.Fatalf("reserved field not zero: %+v", got.ObjectHeader)
	}
}

func TestChunkDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "chunk.golden")
	var c Chunk
	if _, err := c.Decode(golden[:chunkFixedLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
}

func TestChunkDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "chunk.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var c Chunk
	if _, err := c.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestChunkDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "chunk.golden")
	buf := append([]byte(nil), golden...)
	buf[objectHeaderCRCOffset] ^= 0xFF
	var c Chunk
	if _, err := c.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad crc: got %v, want %v", err, ErrCRC)
	}
}
