package format

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func testRun() Run {
	r := Run{
		Common: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicRun,
			VersionMajor: 1,
			HeaderLen:    RunLen,
		},
		RunSeq:      1,
		DiscSeq:     0,
		FECScheme:   FECSchemeNone,
		HashAlgo:    HashAlgoSHA256,
		IndexBytes:  65536,
		CreatedSec:  1700000000,
		CreatedNsec: 500000000,
		ToolVersion: 0x01000001,
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

	if got.ReservedA != ([10]byte{}) || got.ReservedB != ([32]byte{}) {
		t.Errorf("reserved field not zero: %x %x", got.ReservedA, got.ReservedB)
	}
	for i, b := range got.ReservedC {
		if b != 0 {
			t.Errorf("reserved_c[%d] not zero: 0x%02x", i, b)
		}
	}
	for i, b := range got.ReservedFinal {
		if b != 0 {
			t.Errorf("reserved_final[%d] not zero: 0x%02x", i, b)
		}
	}
}

func TestRunDecodeRefusesReservedByte(t *testing.T) {
	cases := []struct {
		name  string
		off   int
		field string
	}{
		{"reserved_c", 192, "reserved_c byte 0 is 0xff"},
		{"reserved_d", 136, "reserved_d is 0xff"},
		{"reserved_final outside the CRC", 508, "reserved_final byte 0 is 0xff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := append([]byte(nil), readGolden(t, "run.golden")...)
			buf[c.off] = 0xFF
			binary.LittleEndian.PutUint32(buf[504:508], crc32c(buf[0:504]))

			var got Run
			err := got.Decode(buf)
			if !errors.Is(err, ErrReserved) || !strings.Contains(err.Error(), c.field) {
				t.Fatalf("decode: got %v, want %v that names %q", err, ErrReserved, c.field)
			}
		})
	}
}

func TestRunDecodeRefusesHeaderLen(t *testing.T) {
	buf := append([]byte(nil), readGolden(t, "run.golden")...)
	binary.LittleEndian.PutUint16(buf[20:22], RunLen+8)
	binary.LittleEndian.PutUint32(buf[504:508], crc32c(buf[0:504]))

	var got Run
	if err := got.Decode(buf); !errors.Is(err, ErrHeaderLen) {
		t.Fatalf("decode other header_len: got %v, want %v", err, ErrHeaderLen)
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

func TestRunDecodeRejectsFieldValue(t *testing.T) {
	cases := []struct {
		name  string
		set   func(buf []byte)
		field string
	}{
		{"fec_k", func(buf []byte) { binary.LittleEndian.PutUint16(buf[80:82], 231) }, "fec_k is 231"},
		{"fec_m", func(buf []byte) { binary.LittleEndian.PutUint16(buf[82:84], 23) }, "fec_m is 23"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := append([]byte(nil), readGolden(t, "run.golden")...)
			c.set(buf)
			binary.LittleEndian.PutUint32(buf[504:508], crc32c(buf[0:504]))
			var r Run
			err := r.Decode(buf)
			if !errors.Is(err, ErrBadField) {
				t.Fatalf("decode: got %v, want %v", err, ErrBadField)
			}
			if !strings.Contains(err.Error(), c.field) {
				t.Fatalf("decode: error %q does not name %q", err, c.field)
			}
		})
	}
}

func TestRunDecodeReadsUnknownFECScheme(t *testing.T) {
	buf := append([]byte(nil), readGolden(t, "run.golden")...)
	buf[84] = 1
	binary.LittleEndian.PutUint16(buf[80:82], 231)
	binary.LittleEndian.PutUint16(buf[82:84], 23)
	binary.LittleEndian.PutUint32(buf[504:508], crc32c(buf[0:504]))
	var r Run
	if err := r.Decode(buf); err != nil {
		t.Fatalf("decode fec_scheme 1: %v", err)
	}
	if r.FECScheme != 1 {
		t.Fatalf("fec_scheme: got %d, want 1", r.FECScheme)
	}
}
