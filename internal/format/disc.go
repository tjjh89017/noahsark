package format

import "encoding/binary"

// DiscLen is the encoded size of the disc superblock. It is one sector.
const DiscLen = 2048

// Disc is the disc superblock: the immutable facts of one physical disc.
// Every per-run parameter lives in the run header, never here.
type Disc struct {
	Common          CommonHeader
	DiscUUID        [16]byte
	RepoUUID        [16]byte
	DiscSeq         uint64
	CapacitySectors uint64
	ReservedA       [40]byte
	CreatedSec      int64
	CreatedNsec     uint32
	TzOffsetSec     int32
	ReservedB       [8]byte
	LabelLen        uint32
	Label           [64]byte
	ToolVersion     uint32
	ReservedC       [1828]byte
	SuperCRC32C     uint32
}

// Encode writes d into buf[0:DiscLen]. buf must be at least DiscLen bytes.
// Encode computes super_crc32c itself, over bytes 0 to 2043; any value in
// d.SuperCRC32C is overwritten.
func (d *Disc) Encode(buf []byte) error {
	if len(buf) < DiscLen {
		return ErrShort
	}
	if err := d.Common.Encode(buf[0:CommonHeaderLen]); err != nil {
		return err
	}
	copy(buf[32:48], d.DiscUUID[:])
	copy(buf[48:64], d.RepoUUID[:])
	binary.LittleEndian.PutUint64(buf[64:72], d.DiscSeq)
	binary.LittleEndian.PutUint64(buf[72:80], d.CapacitySectors)
	copy(buf[80:120], d.ReservedA[:])
	binary.LittleEndian.PutUint64(buf[120:128], uint64(d.CreatedSec))
	binary.LittleEndian.PutUint32(buf[128:132], d.CreatedNsec)
	binary.LittleEndian.PutUint32(buf[132:136], uint32(d.TzOffsetSec))
	copy(buf[136:144], d.ReservedB[:])
	binary.LittleEndian.PutUint32(buf[144:148], d.LabelLen)
	copy(buf[148:212], d.Label[:])
	binary.LittleEndian.PutUint32(buf[212:216], d.ToolVersion)
	copy(buf[216:2044], d.ReservedC[:])
	crc := crc32c(buf[0:2044])
	d.SuperCRC32C = crc
	binary.LittleEndian.PutUint32(buf[2044:2048], crc)
	return nil
}

// Decode reads a Disc from buf. It rejects a short buffer, a magic_kind
// mismatch, and a super_crc32c mismatch. It does not interpret a reserved
// field.
func (d *Disc) Decode(buf []byte) error {
	if len(buf) < DiscLen {
		return ErrShort
	}
	if err := d.Common.Decode(buf[0:CommonHeaderLen]); err != nil {
		return err
	}
	if d.Common.MagicKind != MagicDisc {
		return ErrBadMagic
	}
	copy(d.DiscUUID[:], buf[32:48])
	copy(d.RepoUUID[:], buf[48:64])
	d.DiscSeq = binary.LittleEndian.Uint64(buf[64:72])
	d.CapacitySectors = binary.LittleEndian.Uint64(buf[72:80])
	copy(d.ReservedA[:], buf[80:120])
	d.CreatedSec = int64(binary.LittleEndian.Uint64(buf[120:128]))
	d.CreatedNsec = binary.LittleEndian.Uint32(buf[128:132])
	d.TzOffsetSec = int32(binary.LittleEndian.Uint32(buf[132:136]))
	copy(d.ReservedB[:], buf[136:144])
	d.LabelLen = binary.LittleEndian.Uint32(buf[144:148])
	copy(d.Label[:], buf[148:212])
	d.ToolVersion = binary.LittleEndian.Uint32(buf[212:216])
	copy(d.ReservedC[:], buf[216:2044])
	d.SuperCRC32C = binary.LittleEndian.Uint32(buf[2044:2048])
	if crc32c(buf[0:2044]) != d.SuperCRC32C {
		return ErrCRC
	}
	return nil
}
