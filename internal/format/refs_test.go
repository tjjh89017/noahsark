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
			HeaderLen:    RefsHeaderLen,
		},
		RecordCount: 2,
		Records: []RefRecord{
			{SnapshotID: snapshotIDN(1), TimeSec: 1700000000, TimeNsec: 123456, NameLen: 10, Name: nameArray("2026-09-13")},
			{SnapshotID: snapshotIDN(2), TimeSec: 1700000100, TimeNsec: 0, NameLen: 5, Name: nameArray("daily")},
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
	n, err := got.Decode(golden)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(golden) {
		t.Fatalf("decode consumed %d bytes, want %d", n, len(golden))
	}

	if got.RepoUUID != tbl.RepoUUID || got.RecordCount != tbl.RecordCount {
		t.Fatalf("decoded fixed fields mismatch: got %+v", got)
	}
	for i := range tbl.Records {
		if got.Records[i] != tbl.Records[i] {
			t.Errorf("record %d mismatch: got %+v, want %+v", i, got.Records[i], tbl.Records[i])
		}
	}
	for i, r := range got.Records {
		if r.ReservedU16 != 0 {
			t.Errorf("record %d reserved_u16 not zero: %d", i, r.ReservedU16)
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
