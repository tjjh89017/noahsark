package format

import (
	"encoding/binary"
	"testing"
)

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
			VersionMinor: 0,
			HeaderLen:    DiscsHeaderLen,
		},
		RecordCount: 2,
		RecordSize:  DiscsRowLen,
		HashAlgo:    HashAlgoSHA256,
		DigestLen:   32,
		Rows: []DiscsRow{
			{
				RunSeq: 1, DiscSeq: 0, DiscUUID: discUUIDN(1), RunHash: runHashN(1),
				CreatedSec: 1700000000, LastVerifySec: 1700003600,
				CapacitySectors: 12219392, UsedSectors: 500000,
				RunStatus: 1, Health: 1, RsMarginPercent: 95,
				LabelLen: 8, Label: labelArray("BACKUP01"),
				StateFlags: DiscsStateClosed,
			},
			{
				RunSeq: 2, DiscSeq: 0, DiscUUID: discUUIDN(1), RunHash: [32]byte{},
				CreatedSec: 1700100000, LastVerifySec: 0,
				CapacitySectors: 12219392, UsedSectors: 900000,
				RunStatus: 2, Health: 6, RsMarginPercent: 0,
				LabelLen: 8, Label: labelArray("BACKUP01"),
				StateFlags: 0,
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
	if _, err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.RepoUUID != tbl.RepoUUID || got.RecordCount != tbl.RecordCount {
		t.Fatalf("decoded fixed fields mismatch: got %+v", got)
	}
	for i := range tbl.Rows {
		if got.Rows[i] != tbl.Rows[i] {
			t.Errorf("row %d mismatch: got %+v, want %+v", i, got.Rows[i], tbl.Rows[i])
		}
	}
	if got.Reserved != ([4]byte{}) {
		t.Errorf("reserved not zero: %x", got.Reserved)
	}
	for i, r := range got.Rows {
		if r.Reserved != 0 {
			t.Errorf("row %d reserved not zero: %d", i, r.Reserved)
		}
	}
}

func TestDiscsTableDecodeIgnoresReservedFields(t *testing.T) {
	golden := readGolden(t, "discs.golden")
	buf := append([]byte(nil), golden...)
	buf[60] = 0xFF                 // the table's reserved field
	buf[DiscsHeaderLen+167] = 0xFF // the first row's reserved byte

	total := len(buf)
	binary.LittleEndian.PutUint32(buf[64:68], crc32c(buf[DiscsHeaderLen:total]))
	binary.LittleEndian.PutUint32(buf[68:72], crc32c(buf[0:68]))

	var got DiscsTable
	if _, err := got.Decode(buf); err != nil {
		t.Fatalf("decode nonzero reserved fields: %v", err)
	}
	if got.Reserved[0] != 0xFF {
		t.Fatalf("table reserved byte not preserved: %x", got.Reserved)
	}
	if got.Rows[0].Reserved != 0xFF {
		t.Fatalf("row reserved byte not preserved: %x", got.Rows[0].Reserved)
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

func TestDiscsTableDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "discs.golden")
	buf := append([]byte(nil), golden...)
	buf[len(buf)-1] ^= 0xFF
	var tbl DiscsTable
	if _, err := tbl.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad body crc: got %v, want %v", err, ErrCRC)
	}
}
