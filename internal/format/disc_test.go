package format

import (
	"encoding/binary"
	"testing"
)

func testDisc() Disc {
	d := Disc{
		Common: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicDisc,
			VersionMajor: 1,
			HeaderLen:    DiscLen,
		},
		DiscSeq:         0,
		CapacitySectors: 12219392,
		CreatedSec:      1700000000,
		CreatedNsec:     500000000,
		TzOffsetSec:     -25200,
		LabelLen:        14,
		ToolVersion:     0x01000001,
	}
	for i := range d.DiscUUID {
		d.DiscUUID[i] = byte(i + 1)
	}
	for i := range d.RepoUUID {
		d.RepoUUID[i] = byte(i + 101)
	}
	copy(d.Label[:], "Archive Disc 1")
	return d
}

func TestDiscGolden(t *testing.T) {
	d := testDisc()
	buf := make([]byte, DiscLen)
	if err := d.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "disc.golden", buf)

	golden := readGolden(t, "disc.golden")
	var got Disc
	if err := got.Decode(golden); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != d {
		t.Fatalf("decoded disc mismatch: got %+v, want %+v", got, d)
	}

	if got.ReservedA != ([40]byte{}) || got.ReservedB != ([8]byte{}) {
		t.Errorf("reserved field not zero: %x %x", got.ReservedA, got.ReservedB)
	}
	for i, b := range got.ReservedC {
		if b != 0 {
			t.Errorf("reserved_c[%d] not zero: 0x%02x", i, b)
		}
	}
}

func TestDiscDecodeIgnoresReservedByte(t *testing.T) {
	golden := readGolden(t, "disc.golden")
	buf := append([]byte(nil), golden...)
	buf[216] = 0xFF
	binary.LittleEndian.PutUint32(buf[2044:2048], crc32c(buf[0:2044]))

	var got Disc
	if err := got.Decode(buf); err != nil {
		t.Fatalf("decode nonzero reserved byte: %v", err)
	}
	want := testDisc()
	want.ReservedC[0] = 0xFF
	want.SuperCRC32C = crc32c(buf[0:2044])
	if got != want {
		t.Fatalf("decoded disc mismatch: got %+v, want %+v", got, want)
	}
}

func TestDiscDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "disc.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var d Disc
	if err := d.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestDiscDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "disc.golden")
	buf := append([]byte(nil), golden...)
	buf[64] ^= 0xFF
	var d Disc
	if err := d.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad crc: got %v, want %v", err, ErrCRC)
	}
}

func TestDiscDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "disc.golden")
	var d Disc
	if err := d.Decode(golden[:DiscLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
	if err := d.Encode(make([]byte, DiscLen-1)); err != ErrShort {
		t.Fatalf("encode short buffer: got %v, want %v", err, ErrShort)
	}
}
