package format

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// TestSnapshotMetaRefusesReservedValues checks the flags and the padding
// of a snapshot metadata record.
func TestSnapshotMetaRefusesReservedValues(t *testing.T) {
	m := SnapshotMeta{Tag: SnapshotMetaHost, Value: []byte("ark")}
	plain := make([]byte, m.EncodedLen())
	if err := m.Encode(plain); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		set   func(buf []byte)
		field string
	}{
		{"reserved flag bit", func(buf []byte) { buf[2] |= 1 << 1 }, "flags is 0x2"},
		{"padding", func(buf []byte) { buf[len(buf)-1] = 0x5A }, "padding byte 0 is 0x5a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := append([]byte(nil), plain...)
			c.set(buf)
			var got SnapshotMeta
			if _, err := got.Decode(buf); !errors.Is(err, ErrReserved) || !strings.Contains(err.Error(), c.field) {
				t.Fatalf("decode: got %v, want %v that names %q", err, ErrReserved, c.field)
			}
		})
	}
}

// TestRefsDecodeRefusesNamePadding sets a byte after the name of the
// first ref record.
func TestRefsDecodeRefusesNamePadding(t *testing.T) {
	buf := append([]byte(nil), readGolden(t, "refs.golden")...)
	row := RefsHeaderLen
	nameLen := int(binary.LittleEndian.Uint16(buf[row+44 : row+46]))
	buf[row+48+nameLen] = 'x'
	var got RefsTable
	if _, err := got.Decode(buf); !errors.Is(err, ErrReserved) || !strings.Contains(err.Error(), "REFS record 0: ") {
		t.Fatalf("decode: got %v, want %v that names REFS record 0", err, ErrReserved)
	}
}

// TestDiscsDecodeRefusesLabelPadding sets a byte after the label of the
// first DISCS row.
func TestDiscsDecodeRefusesLabelPadding(t *testing.T) {
	buf := append([]byte(nil), readGolden(t, "discs.golden")...)
	row := DiscsHeaderLen
	labelLen := int(binary.LittleEndian.Uint16(buf[row+100 : row+102]))
	buf[row+102+labelLen] = 'x'
	var got DiscsTable
	if _, err := got.Decode(buf); !errors.Is(err, ErrReserved) || !strings.Contains(err.Error(), "label padding") {
		t.Fatalf("decode: got %v, want %v that names the label padding", err, ErrReserved)
	}
}

// TestObjectFileHeaderChecks checks the faults that DecodeObjectFileHeader
// refuses after the CRC: a kind that disagrees with magic_kind, an
// unknown hash_algo, an unknown compression, and a header_len that is
// not the value of the kind.
func TestObjectFileHeaderChecks(t *testing.T) {
	plain := readGolden(t, "chunk.golden")[:CommonHeaderLen+ObjectHeaderLen]
	if _, _, err := DecodeObjectFileHeader(plain); err != nil {
		t.Fatalf("decode the plain chunk header: %v", err)
	}
	cases := []struct {
		name string
		set  func(buf []byte)
		want error
		text string
	}{
		{"kind", func(buf []byte) { buf[CommonHeaderLen] = byte(ObjectKindBlob) }, ErrBadField, "kind is 2"},
		{"hash_algo", func(buf []byte) { buf[CommonHeaderLen+1] = 0x13 }, ErrBadField, "hash_algo is 0x13"},
		{"compression", func(buf []byte) { buf[CommonHeaderLen+3] = 7 }, ErrBadField, "compression is 7"},
		{"header_len", func(buf []byte) { binary.LittleEndian.PutUint16(buf[20:22], BlobHeaderLen) }, ErrHeaderLen, "header_len is 72, want 64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := append([]byte(nil), plain...)
			c.set(buf)
			objectFixup(buf)
			if _, _, err := DecodeObjectFileHeader(buf); !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.text) {
				t.Fatalf("decode: got %v, want %v that names %q", err, c.want, c.text)
			}
		})
	}
}
