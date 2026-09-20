package format

import "encoding/binary"

// TLVHeaderLen is the encoded size of a TLV record's prefix, before the
// payload and its padding.
const TLVHeaderLen = 8

// TLVFlagCritical is tlv_flags bit 0. A reader that meets an unknown TLV
// with this bit set must refuse the entry.
const TLVFlagCritical uint16 = 1 << 0

// TLV type registry.
const (
	TLVTypeSymlinkTarget uint16 = 0x0001
	TLVTypeUserName      uint16 = 0x0002
	TLVTypeGroupName     uint16 = 0x0003
	TLVTypeRootPath      uint16 = 0x0004
	TLVTypeXattr         uint16 = 0x0010
	TLVTypeACLAccess     uint16 = 0x0011
	TLVTypeACLDefault    uint16 = 0x0012
	TLVTypeACLNFS4       uint16 = 0x0013
	TLVTypeLinuxAttr     uint16 = 0x0020
	TLVTypeBSDFlags      uint16 = 0x0021
	TLVTypeWinAttrs      uint16 = 0x0030
	TLVTypeWinSD         uint16 = 0x0031
	TLVTypeWinADS        uint16 = 0x0032
)

// tlvVendorLow and tlvVendorHigh bound the vendor range. A vendor type is
// never critical and may repeat inside one entry.
const (
	tlvVendorLow  uint16 = 0xF000
	tlvVendorHigh uint16 = 0xFFFF
)

// tlvKnownTypes is the registered, non-vendor type set. A type outside
// this set and outside the vendor range is unknown.
var tlvKnownTypes = map[uint16]bool{
	TLVTypeSymlinkTarget: true,
	TLVTypeUserName:      true,
	TLVTypeGroupName:     true,
	TLVTypeRootPath:      true,
	TLVTypeXattr:         true,
	TLVTypeACLAccess:     true,
	TLVTypeACLDefault:    true,
	TLVTypeACLNFS4:       true,
	TLVTypeLinuxAttr:     true,
	TLVTypeBSDFlags:      true,
	TLVTypeWinAttrs:      true,
	TLVTypeWinSD:         true,
	TLVTypeWinADS:        true,
}

// tlvIsVendor reports whether typ is in the vendor range, 0xF000 to
// 0xFFFF, the only range allowed to repeat inside one entry.
func tlvIsVendor(typ uint16) bool {
	return typ >= tlvVendorLow && typ <= tlvVendorHigh
}

// TLV is one extension record of a tree entry's TLV area. The payload is
// always inline; there is no spill form.
type TLV struct {
	Type    uint16
	Flags   uint16
	Payload []byte
}

// EncodedLen is the record's length on the medium: the 8-byte prefix, the
// payload, and zero padding to the next 8-byte boundary.
func (t *TLV) EncodedLen() int {
	return align8(TLVHeaderLen + len(t.Payload))
}

// Encode writes t into buf and returns the number of bytes written,
// EncodedLen(). buf must be at least that many bytes.
func (t *TLV) Encode(buf []byte) (int, error) {
	n := t.EncodedLen()
	if len(buf) < n {
		return 0, ErrShort
	}
	binary.LittleEndian.PutUint16(buf[0:2], t.Type)
	binary.LittleEndian.PutUint16(buf[2:4], t.Flags)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(t.Payload)))
	copy(buf[8:8+len(t.Payload)], t.Payload)
	for i := 8 + len(t.Payload); i < n; i++ {
		buf[i] = 0
	}
	return n, nil
}

// Decode reads one TLV from buf and returns the number of bytes read. It
// rejects a short buffer. It does not interpret a padding byte.
func (t *TLV) Decode(buf []byte) (int, error) {
	if len(buf) < TLVHeaderLen {
		return 0, ErrShort
	}
	payloadLen := binary.LittleEndian.Uint32(buf[4:8])
	n := align8(TLVHeaderLen + int(payloadLen))
	if len(buf) < n {
		return 0, ErrShort
	}
	t.Type = binary.LittleEndian.Uint16(buf[0:2])
	t.Flags = binary.LittleEndian.Uint16(buf[2:4])
	t.Payload = append([]byte(nil), buf[TLVHeaderLen:TLVHeaderLen+int(payloadLen)]...)
	return n, nil
}
