package format

import "encoding/binary"

// DiscLen is the encoded size of the disc superblock. It is one sector.
const DiscLen = 2048

// Disc is the disc superblock: the immutable facts of one physical disc.
// A writer writes it once, as /NOAHSARK/DISC.bin in the first run, and
// never updates it.
type Disc struct {
	Common                CommonHeader
	DiscUUID              [16]byte
	RepoUUID              [16]byte
	DiscSeq               uint64
	CapacitySectors       uint64
	CapacityForcedSectors uint64
	PrevDiscSuperHash     [32]byte
	CreatedSec            int64
	CreatedNsec           uint32
	TzOffsetSec           int32
	MediaType             MediaType
	FSProfile             DiscFSProfile
	FanoutLevels          uint8
	CapacityIsForced      uint8
	Sealed                uint8
	ReservedU8            [3]byte
	LabelLen              uint32
	Label                 [64]byte
	ToolVersion           uint32
	ReservedU32           uint32
	Reserved              [1824]byte
	SuperCRC32C           uint32
}

// Encode writes d into buf[0:DiscLen]. buf must be at least DiscLen bytes.
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
	binary.LittleEndian.PutUint64(buf[80:88], d.CapacityForcedSectors)
	copy(buf[88:120], d.PrevDiscSuperHash[:])
	binary.LittleEndian.PutUint64(buf[120:128], uint64(d.CreatedSec))
	binary.LittleEndian.PutUint32(buf[128:132], d.CreatedNsec)
	binary.LittleEndian.PutUint32(buf[132:136], uint32(d.TzOffsetSec))
	buf[136] = byte(d.MediaType)
	buf[137] = byte(d.FSProfile)
	buf[138] = d.FanoutLevels
	buf[139] = d.CapacityIsForced
	buf[140] = d.Sealed
	copy(buf[141:144], d.ReservedU8[:])
	binary.LittleEndian.PutUint32(buf[144:148], d.LabelLen)
	copy(buf[148:212], d.Label[:])
	binary.LittleEndian.PutUint32(buf[212:216], d.ToolVersion)
	binary.LittleEndian.PutUint32(buf[216:220], d.ReservedU32)
	copy(buf[220:2044], d.Reserved[:])
	binary.LittleEndian.PutUint32(buf[2044:2048], d.SuperCRC32C)
	return nil
}

// Decode reads a Disc from buf. It rejects a short buffer, a magic_kind
// mismatch, and a nonzero reserved field.
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
	d.CapacityForcedSectors = binary.LittleEndian.Uint64(buf[80:88])
	copy(d.PrevDiscSuperHash[:], buf[88:120])
	d.CreatedSec = int64(binary.LittleEndian.Uint64(buf[120:128]))
	d.CreatedNsec = binary.LittleEndian.Uint32(buf[128:132])
	d.TzOffsetSec = int32(binary.LittleEndian.Uint32(buf[132:136]))
	d.MediaType = MediaType(buf[136])
	d.FSProfile = DiscFSProfile(buf[137])
	d.FanoutLevels = buf[138]
	d.CapacityIsForced = buf[139]
	d.Sealed = buf[140]
	copy(d.ReservedU8[:], buf[141:144])
	for _, b := range d.ReservedU8 {
		if b != 0 {
			return ErrReserved
		}
	}
	d.LabelLen = binary.LittleEndian.Uint32(buf[144:148])
	copy(d.Label[:], buf[148:212])
	d.ToolVersion = binary.LittleEndian.Uint32(buf[212:216])
	d.ReservedU32 = binary.LittleEndian.Uint32(buf[216:220])
	if d.ReservedU32 != 0 {
		return ErrReserved
	}
	copy(d.Reserved[:], buf[220:2044])
	for _, b := range d.Reserved {
		if b != 0 {
			return ErrReserved
		}
	}
	d.SuperCRC32C = binary.LittleEndian.Uint32(buf[2044:2048])
	return nil
}
