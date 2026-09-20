package format

import (
	"encoding/binary"
	"testing"
)

func fileHashN(n int) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = byte((n*3 + i) % 256)
	}
	return h
}

func contentIDN(n int) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = byte((n*7 + i*2) % 256)
	}
	return h
}

func testIndex() Index {
	return Index{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicIndex,
			VersionMajor: 1,
			VersionMinor: 0,
			HeaderLen:    IndexHeaderLen,
		},
		RunSeq:           7,
		FileCount:        2,
		ObjectCount:      2,
		PrereqCount:      1,
		FileRecordSize:   IndexFileRecordLen,
		ObjectRecordSize: IndexObjectRecordLen,
		PrereqRecordSize: IndexPrereqRecordLen,
		HashAlgo:         HashAlgoSHA256,
		DigestLen:        32,
		Files: []IndexFileRecord{
			{FileHash: fileHashN(1), ByteLen: 8192, Role: FileRoleIndex},
			{FileHash: fileHashN(2), ByteLen: 512, Role: FileRoleRun},
		},
		Objects: []IndexObjectRecord{
			{ContentID: contentIDN(1), FileIndex: 0, Offset: 0, StoredLen: 4096, PayloadLen: 4096, Kind: ObjectKindChunk, Compression: CompressionZstd, Flags: 0},
			{ContentID: contentIDN(2), FileIndex: 1, Offset: 0, StoredLen: 2048, PayloadLen: 2048, Kind: ObjectKindTree, Compression: CompressionNone, Flags: 2},
		},
		Prereqs: []IndexPrereqRecord{
			{ContentID: contentIDN(3), RunSeq: 99},
		},
	}
}

func TestIndexGolden(t *testing.T) {
	idx := testIndex()
	buf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "index.golden", buf)

	golden := readGolden(t, "index.golden")
	var got Index
	if _, err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.RunSeq != idx.RunSeq || got.FileCount != idx.FileCount ||
		got.ObjectCount != idx.ObjectCount || got.PrereqCount != idx.PrereqCount ||
		got.ContainerLen != uint64(len(golden)) {
		t.Fatalf("decoded fixed fields mismatch: got %+v", got)
	}
	for i := range idx.Files {
		if got.Files[i] != idx.Files[i] {
			t.Errorf("file row %d mismatch: got %+v, want %+v", i, got.Files[i], idx.Files[i])
		}
	}
	for i := range idx.Objects {
		if got.Objects[i] != idx.Objects[i] {
			t.Errorf("object row %d mismatch: got %+v, want %+v", i, got.Objects[i], idx.Objects[i])
		}
	}
	for i := range idx.Prereqs {
		if got.Prereqs[i] != idx.Prereqs[i] {
			t.Errorf("prereq row %d mismatch: got %+v, want %+v", i, got.Prereqs[i], idx.Prereqs[i])
		}
	}

	if got.Reserved != ([4]byte{}) {
		t.Errorf("reserved not zero: %x", got.Reserved)
	}
	for i, f := range got.Files {
		if f.Reserved != ([7]byte{}) {
			t.Errorf("file row %d reserved not zero: %x", i, f.Reserved)
		}
	}
	for i, o := range got.Objects {
		if o.Reserved1 != 0 || o.Reserved2 != 0 {
			t.Errorf("object row %d reserved not zero: %d %d", i, o.Reserved1, o.Reserved2)
		}
	}
}

func TestIndexDecodeIgnoresReservedByte(t *testing.T) {
	golden := readGolden(t, "index.golden")
	buf := append([]byte(nil), golden...)
	buf[60] = 0xFF
	binary.LittleEndian.PutUint32(buf[76:80], crc32c(buf[0:76]))

	var got Index
	if _, err := got.Decode(buf); err != nil {
		t.Fatalf("decode nonzero reserved byte: %v", err)
	}
	idx := testIndex()
	if got.RunSeq != idx.RunSeq || got.FileCount != idx.FileCount ||
		got.ObjectCount != idx.ObjectCount || got.PrereqCount != idx.PrereqCount {
		t.Fatalf("decoded fixed fields mismatch: got %+v", got)
	}
	if got.Reserved[0] != 0xFF {
		t.Fatalf("reserved byte not preserved: %x", got.Reserved)
	}
}

func TestIndexDecodeIgnoresReservedRecordFields(t *testing.T) {
	golden := readGolden(t, "index.golden")
	buf := append([]byte(nil), golden...)
	fileRow := IndexHeaderLen
	buf[fileRow+41] = 0xFF // a Files row's reserved byte
	objectRow := IndexHeaderLen + 2*IndexFileRecordLen
	binary.LittleEndian.PutUint32(buf[objectRow+36:objectRow+40], 0xFFFFFFFF) // an Objects row's reserved1

	total := len(buf)
	binary.LittleEndian.PutUint32(buf[72:76], crc32c(buf[IndexHeaderLen:total]))
	binary.LittleEndian.PutUint32(buf[76:80], crc32c(buf[0:76]))

	var got Index
	if _, err := got.Decode(buf); err != nil {
		t.Fatalf("decode nonzero reserved record fields: %v", err)
	}
	if got.Files[0].Reserved[0] != 0xFF {
		t.Fatalf("file row reserved byte not preserved: %x", got.Files[0].Reserved)
	}
	if got.Objects[0].Reserved1 != 0xFFFFFFFF {
		t.Fatalf("object row reserved1 not preserved: %x", got.Objects[0].Reserved1)
	}
}

func TestIndexDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "index.golden")
	var idx Index
	if _, err := idx.Decode(golden[:IndexHeaderLen-1]); err != ErrShort {
		t.Fatalf("decode short header: got %v, want %v", err, ErrShort)
	}
	if _, err := idx.Decode(golden[:len(golden)-1]); err != ErrShort {
		t.Fatalf("decode short body: got %v, want %v", err, ErrShort)
	}
}

func TestIndexDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "index.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var idx Index
	if _, err := idx.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestIndexDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "index.golden")
	buf := append([]byte(nil), golden...)
	buf[len(buf)-1] ^= 0xFF
	var idx Index
	if _, err := idx.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad body crc: got %v, want %v", err, ErrCRC)
	}
}
