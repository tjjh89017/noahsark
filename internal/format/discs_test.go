package format

import "testing"

func discUUIDN(n int) [16]byte {
	var h [16]byte
	for i := range h {
		h[i] = byte((n*11 + i) % 256)
	}
	return h
}

func runHashN(n int) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = byte((n*13 + i) % 256)
	}
	return h
}

func labelArray(s string) [DiscsLabelLen]byte {
	var a [DiscsLabelLen]byte
	copy(a[:], s)
	return a
}

func testDiscsTable() DiscsTable {
	t := DiscsTable{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicDiscs,
			VersionMajor: 1,
			HeaderLen:    DiscsHeaderLen,
		},
		RecordCount: 2,
		Rows: []DiscsRow{
			{
				RunSeq: 1, DiscSeq: 0, DiscUUID: discUUIDN(1), RunHash: runHashN(1),
				CreatedSec: 1700000000, CapacitySectors: 12219392,
				LabelLen: 8, Label: labelArray("BACKUP01"),
			},
			{
				RunSeq: 1, DiscSeq: 1, DiscUUID: discUUIDN(2), RunHash: [32]byte{},
				CreatedSec: 1700100000, CapacitySectors: 12219392,
				LabelLen: 8, Label: labelArray("BACKUP02"),
			},
		},
	}
	for i := range t.RepoUUID {
		t.RepoUUID[i] = byte(16 + i)
	}
	return t
}

func TestDiscsTableGolden(t *testing.T) {
	tbl := testDiscsTable()
	buf := make([]byte, tbl.EncodedLen())
	if _, err := tbl.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "discs.golden", buf)

	golden := readGolden(t, "discs.golden")
	var got DiscsTable
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
	for i := range tbl.Rows {
		if got.Rows[i] != tbl.Rows[i] {
			t.Errorf("row %d mismatch: got %+v, want %+v", i, got.Rows[i], tbl.Rows[i])
		}
	}
	for i, r := range got.Rows {
		if r.ReservedU64a != 0 || r.ReservedU64b != 0 || r.ReservedU32 != 0 || r.Reserved != ([10]byte{}) {
			t.Errorf("row %d reserved not zero: %+v", i, r)
		}
	}
}

// TestDiscsRowsShareARunSeq checks the case the disc_uuid field fixes:
// two rows of one run number, told apart by their uuid.
func TestDiscsRowsShareARunSeq(t *testing.T) {
	tbl := testDiscsTable()
	if tbl.Rows[0].RunSeq != tbl.Rows[1].RunSeq {
		t.Fatalf("the vector must hold two rows of one run_seq")
	}
	if tbl.Rows[0].DiscUUID == tbl.Rows[1].DiscUUID {
		t.Fatalf("the two rows must hold different uuids")
	}
}

func TestDiscsTableDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "discs.golden")
	var tbl DiscsTable
	if _, err := tbl.Decode(golden[:DiscsHeaderLen-1]); err != ErrShort {
		t.Fatalf("decode short header: got %v, want %v", err, ErrShort)
	}
	if _, err := tbl.Decode(golden[:len(golden)-1]); err != ErrShort {
		t.Fatalf("decode short body: got %v, want %v", err, ErrShort)
	}
}

func TestDiscsTableDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "discs.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var tbl DiscsTable
	if _, err := tbl.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}
