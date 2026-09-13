package format

import "testing"

func testRun() Run {
	r := Run{
		Common: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicRun,
			VersionMajor: 1,
			VersionMinor: 0,
			HeaderLen:    CommonHeaderLen + 480,
		},
		RunSeq:          1,
		DiscSeq:         0,
		FECK:            231,
		FECM:            23,
		FECScheme:       FECSchemeRS255GF8,
		HashAlgo:        HashAlgoSHA256,
		ChunkerProfile:  ChunkerProfileP3,
		Compression:     CompressionZstd,
		FSProfile:       DiscFSProfileOneshot,
		RunKind:         RunKindData,
		RunFlags:        RunFlagClosing,
		IndexBytes:      65536,
		StreamBytes:     471859200,
		CreatedSec:      1700000000,
		CreatedNsec:     500000000,
		ToolVersion:     0x01000001,
		DiscObjectCount: 1000,
		DiscRunIndex:    0,
		HeaderCRC32C:    0x17AF43B4,
	}
	for i := range r.DiscUUID {
		r.DiscUUID[i] = byte(i + 1)
	}
	for i := range r.RepoUUID {
		r.RepoUUID[i] = byte(i + 101)
	}
	for i := range r.IndexHash {
		r.IndexHash[i] = byte(i + 1)
	}
	return r
}

func TestRunGolden(t *testing.T) {
	r := testRun()
	buf := make([]byte, RunLen)
	if err := r.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "run.golden", buf)

	golden := readGolden(t, "run.golden")
	var got Run
	if err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != r {
		t.Fatalf("decoded run mismatch: got %+v, want %+v", got, r)
	}

	if got.ReservedU8 != 0 {
		t.Errorf("reserved_u8 not zero: 0x%02x", got.ReservedU8)
	}
	if got.ReservedU32a != 0 {
		t.Errorf("reserved_u32a not zero: 0x%08x", got.ReservedU32a)
	}
	if got.ReservedU32b != 0 {
		t.Errorf("reserved_u32b not zero: 0x%08x", got.ReservedU32b)
	}
	for i, b := range got.Reserved {
		if b != 0 {
			t.Errorf("reserved[%d] not zero: 0x%02x", i, b)
		}
	}
	for i, b := range got.ReservedFinal {
		if b != 0 {
			t.Errorf("reserved_final[%d] not zero: 0x%02x", i, b)
		}
	}
}

func TestRunDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "run.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var r Run
	if err := r.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestRunDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "run.golden")
	buf := append([]byte(nil), golden...)
	buf[64] ^= 0xFF
	var r Run
	if err := r.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad crc: got %v, want %v", err, ErrCRC)
	}
}

func TestRunDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "run.golden")
	var r Run
	if err := r.Decode(golden[:RunLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
	if err := r.Encode(make([]byte, RunLen-1)); err != ErrShort {
		t.Fatalf("encode short buffer: got %v, want %v", err, ErrShort)
	}
}
