package format

import "testing"

func testCommonHeader() CommonHeader {
	return CommonHeader{
		MagicProject: ProjectMagic,
		MagicKind:    MagicChunk,
		VersionMajor: 1,
		VersionMinor: 0,
		HeaderLen:    CommonHeaderLen + ObjectHeaderLen,
		ReservedU16:  0,
		ReservedU64:  0,
	}
}

func TestCommonHeaderGolden(t *testing.T) {
	h := testCommonHeader()
	buf := make([]byte, CommonHeaderLen)
	if err := h.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "common_header.golden", buf)

	golden := readGolden(t, "common_header.golden")
	var got CommonHeader
	if err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != h {
		t.Fatalf("decoded header mismatch: got %+v, want %+v", got, h)
	}

	if got.ReservedU16 != 0 {
		t.Errorf("reserved_u16 not zero: 0x%04x", got.ReservedU16)
	}
	if got.ReservedU64 != 0 {
		t.Errorf("reserved_u64 not zero: 0x%016x", got.ReservedU64)
	}
}

func TestCommonHeaderDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "common_header.golden")
	buf := append([]byte(nil), golden...)
	buf[0] = 'X'
	var h CommonHeader
	if err := h.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestCommonHeaderDecodeRejectsVersion(t *testing.T) {
	golden := readGolden(t, "common_header.golden")
	buf := append([]byte(nil), golden...)
	buf[16] = 2
	buf[17] = 0
	var h CommonHeader
	if err := h.Decode(buf); err != ErrVersion {
		t.Fatalf("decode bad version: got %v, want %v", err, ErrVersion)
	}
}

func TestCommonHeaderDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "common_header.golden")
	var h CommonHeader
	if err := h.Decode(golden[:CommonHeaderLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
	if err := h.Encode(make([]byte, CommonHeaderLen-1)); err != ErrShort {
		t.Fatalf("encode short buffer: got %v, want %v", err, ErrShort)
	}
}

func testObjectHeader() ObjectHeader {
	return ObjectHeader{
		Kind:         ObjectKindChunk,
		HashAlgo:     HashAlgoSHA256,
		DigestLen:    32,
		Compression:  CompressionZstd,
		Crypto:       0,
		ReservedU8:   0,
		ReservedU16:  0,
		PayloadLen:   1024,
		StoredLen:    512,
		HeaderCRC32C: 0xDEADBEEF,
		ReservedU32:  0,
	}
}

func TestObjectHeaderGolden(t *testing.T) {
	h := testObjectHeader()
	buf := make([]byte, ObjectHeaderLen)
	if err := h.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "object_header.golden", buf)

	golden := readGolden(t, "object_header.golden")
	var got ObjectHeader
	if err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != h {
		t.Fatalf("decoded header mismatch: got %+v, want %+v", got, h)
	}

	if got.ReservedU8 != 0 {
		t.Errorf("reserved_u8 not zero: 0x%02x", got.ReservedU8)
	}
	if got.ReservedU16 != 0 {
		t.Errorf("reserved_u16 not zero: 0x%04x", got.ReservedU16)
	}
	if got.ReservedU32 != 0 {
		t.Errorf("reserved_u32 not zero: 0x%08x", got.ReservedU32)
	}
}

func TestObjectHeaderDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "object_header.golden")
	var h ObjectHeader
	if err := h.Decode(golden[:ObjectHeaderLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
	if err := h.Encode(make([]byte, ObjectHeaderLen-1)); err != ErrShort {
		t.Fatalf("encode short buffer: got %v, want %v", err, ErrShort)
	}
}
