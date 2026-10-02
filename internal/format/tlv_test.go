package format

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func testTLV() TLV {
	return TLV{
		Type:    TLVTypeGroupName,
		Flags:   0,
		Payload: []byte("staff"),
	}
}

func TestTLVGolden(t *testing.T) {
	tlv := testTLV()
	buf := make([]byte, tlv.EncodedLen())
	n, err := tlv.Encode(buf)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("encode wrote %d bytes, want %d", n, len(buf))
	}
	compareGolden(t, "tlv.golden", buf)

	golden := readGolden(t, "tlv.golden")
	var got TLV
	n, err = got.Decode(golden)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(golden) {
		t.Fatalf("decode read %d bytes, want %d", n, len(golden))
	}
	if got.Type != tlv.Type || got.Flags != tlv.Flags || string(got.Payload) != string(tlv.Payload) {
		t.Fatalf("decoded mismatch: got %+v, want %+v", got, tlv)
	}
}

func TestTLVDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "tlv.golden")
	var tlv TLV
	if _, err := tlv.Decode(golden[:TLVHeaderLen-1]); err != ErrShort {
		t.Fatalf("decode short prefix: got %v, want %v", err, ErrShort)
	}
	if _, err := tlv.Decode(golden[:len(golden)-1]); err != ErrShort {
		t.Fatalf("decode short body: got %v, want %v", err, ErrShort)
	}
}

func TestTLVDecodeRefusesNonzeroPadding(t *testing.T) {
	buf := append([]byte(nil), readGolden(t, "tlv.golden")...)
	buf[len(buf)-1] = 1
	var got TLV
	if _, err := got.Decode(buf); !errors.Is(err, ErrReserved) || !strings.Contains(err.Error(), "padding") {
		t.Fatalf("decode nonzero padding: got %v, want %v that names the padding", err, ErrReserved)
	}
}

func TestTLVDecodeRefusesReservedFlagBit(t *testing.T) {
	buf := append([]byte(nil), readGolden(t, "tlv.golden")...)
	binary.LittleEndian.PutUint16(buf[2:4], binary.LittleEndian.Uint16(buf[2:4])|1<<3)
	var got TLV
	if _, err := got.Decode(buf); !errors.Is(err, ErrReserved) || !strings.Contains(err.Error(), "tlv_flags") {
		t.Fatalf("decode reserved flag bit: got %v, want %v that names tlv_flags", err, ErrReserved)
	}
}
