package format

import "testing"

func snapshotIDN(n int) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = byte((n*5 + i) % 256)
	}
	return h
}

func nameArray(s string) [RefNameLen]byte {
	var a [RefNameLen]byte
	copy(a[:], s)
	return a
}

func testRefsTable() RefsTable {
	t := RefsTable{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicRefs,
			VersionMajor: 1,
			VersionMinor: 0,
			HeaderLen:    RefsHeaderLen,
		},
		RecordCount: 2,
		RecordSize:  RefRecordLen,
		HashAlgo:    HashAlgoSHA256,
		DigestLen:   32,
		Records: []RefRecord{
			{SnapshotID: snapshotIDN(1), TimeSec: 1700000000, TimeNsec: 123456, NameLen: 6, HashAlgo: HashAlgoSHA256, Name: nameArray("LATEST"), RunSeq: 3},
			{SnapshotID: snapshotIDN(2), TimeSec: 1700000100, TimeNsec: 0, NameLen: 5, HashAlgo: HashAlgoSHA256, Name: nameArray("daily"), RunSeq: 4},
		},
	}
	for i := range t.RepoUUID {
		t.RepoUUID[i] = byte(i)
	}
	return t
}

func TestRefsTableGolden(t *testing.T) {
	tbl := testRefsTable()
	buf := make([]byte, tbl.EncodedLen())
	if _, err := tbl.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "refs.golden", buf)

	golden := readGolden(t, "refs.golden")
	var got RefsTable
	if _, err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.RepoUUID != tbl.RepoUUID || got.RecordCount != tbl.RecordCount {
		t.Fatalf("decoded fixed fields mismatch: got %+v", got)
	}
	for i := range tbl.Records {
		if got.Records[i] != tbl.Records[i] {
			t.Errorf("record %d mismatch: got %+v, want %+v", i, got.Records[i], tbl.Records[i])
		}
	}
	if got.Reserved != ([4]byte{}) {
		t.Errorf("reserved not zero: %x", got.Reserved)
	}
	for i, r := range got.Records {
		if r.ReservedU8 != 0 {
			t.Errorf("record %d reserved_u8 not zero: %d", i, r.ReservedU8)
		}
	}
}

func TestRefsTableDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "refs.golden")
	var tbl RefsTable
	if _, err := tbl.Decode(golden[:RefsHeaderLen-1]); err != ErrShort {
		t.Fatalf("decode short header: got %v, want %v", err, ErrShort)
	}
	if _, err := tbl.Decode(golden[:len(golden)-1]); err != ErrShort {
		t.Fatalf("decode short body: got %v, want %v", err, ErrShort)
	}
}

func TestRefsTableDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "refs.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var tbl RefsTable
	if _, err := tbl.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestRefsTableDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "refs.golden")
	buf := append([]byte(nil), golden...)
	buf[len(buf)-1] ^= 0xFF
	var tbl RefsTable
	if _, err := tbl.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad body crc: got %v, want %v", err, ErrCRC)
	}
}
