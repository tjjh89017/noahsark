package format

import "testing"

func testDisc() Disc {
	d := Disc{
		Common: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicDisc,
			VersionMajor: 1,
			VersionMinor: 0,
			HeaderLen:    CommonHeaderLen + 2012,
		},
		DiscSeq:               0,
		CapacitySectors:       12219392,
		CapacityForcedSectors: 12000000,
		CreatedSec:            1700000000,
		CreatedNsec:           500000000,
		TzOffsetSec:           -25200,
		MediaType:             MediaTypeBDRSL25GB,
		FSProfile:             DiscFSProfileOneshot,
		FanoutLevels:          1,
		CapacityIsForced:      1,
		Sealed:                1,
		LabelLen:              14,
		ToolVersion:           0x01000001,
		SuperCRC32C:           0xF9BA7142,
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

	for i, b := range got.ReservedU8 {
		if b != 0 {
			t.Errorf("reserved_u8[%d] not zero: 0x%02x", i, b)
		}
	}
	if got.ReservedU32 != 0 {
		t.Errorf("reserved_u32 not zero: 0x%08x", got.ReservedU32)
	}
	for i, b := range got.Reserved {
		if b != 0 {
			t.Errorf("reserved[%d] not zero: 0x%02x", i, b)
		}
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
