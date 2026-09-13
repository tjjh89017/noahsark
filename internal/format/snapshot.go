package format

import "encoding/binary"

// SnapshotFixedLen is the encoded size of the snapshot kind body, the
// common header and the object header excluded.
const SnapshotFixedLen = 112

// SnapshotMetaTag is the snapshot metadata tag registry.
type SnapshotMetaTag uint16

const (
	SnapshotMetaAuthor         SnapshotMetaTag = 1
	SnapshotMetaHost           SnapshotMetaTag = 2
	SnapshotMetaMessage        SnapshotMetaTag = 3
	SnapshotMetaSourceRoot     SnapshotMetaTag = 4
	SnapshotMetaExcludeRules   SnapshotMetaTag = 5
	SnapshotMetaChecksumCommit SnapshotMetaTag = 6
)

// SnapshotMetaFlagCritical is bit 0 of a metadata record's flags field.
const SnapshotMetaFlagCritical uint16 = 1 << 0

// SnapshotSourceType is the source_type registry.
type SnapshotSourceType uint8

const (
	SnapshotSourceUnknown  SnapshotSourceType = 0
	SnapshotSourceLocal    SnapshotSourceType = 1
	SnapshotSourceSnapshot SnapshotSourceType = 2
	SnapshotSourceNFS      SnapshotSourceType = 3
	SnapshotSourceSMB      SnapshotSourceType = 4
	SnapshotSourceBundle   SnapshotSourceType = 5
)

// source_flags bits.
const (
	SnapshotFlagNoCtime         uint8 = 1 << 0
	SnapshotFlagNoSparse        uint8 = 1 << 2
	SnapshotFlagSyntheticIDs    uint8 = 1 << 3
	SnapshotFlagCaseInsensitive uint8 = 1 << 4
	SnapshotFlagMtimeSlack      uint8 = 1 << 5
)

// SnapshotMeta is one metadata TLV record that follows a snapshot's fixed
// body. meta_count of them follow, in the order they were written.
type SnapshotMeta struct {
	Tag   SnapshotMetaTag
	Flags uint16
	Value []byte
}

// EncodedLen is the record's encoded length: the 8-byte prefix, the value,
// and the padding to the next 4-byte boundary.
func (m *SnapshotMeta) EncodedLen() int {
	n := 8 + len(m.Value)
	if pad := n % 4; pad != 0 {
		n += 4 - pad
	}
	return n
}

// Encode writes m into buf. buf must be at least m.EncodedLen() bytes.
func (m *SnapshotMeta) Encode(buf []byte) error {
	n := m.EncodedLen()
	if len(buf) < n {
		return ErrShort
	}
	binary.LittleEndian.PutUint16(buf[0:2], uint16(m.Tag))
	binary.LittleEndian.PutUint16(buf[2:4], m.Flags)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(m.Value)))
	copy(buf[8:8+len(m.Value)], m.Value)
	for i := 8 + len(m.Value); i < n; i++ {
		buf[i] = 0
	}
	return nil
}

// Decode reads one metadata record from the start of buf and returns the
// number of bytes it consumed, padding included. It rejects a nonzero
// padding byte.
func (m *SnapshotMeta) Decode(buf []byte) (int, error) {
	if len(buf) < 8 {
		return 0, ErrShort
	}
	tag := binary.LittleEndian.Uint16(buf[0:2])
	flags := binary.LittleEndian.Uint16(buf[2:4])
	valueLen := binary.LittleEndian.Uint32(buf[4:8])
	total := 8 + int(valueLen)
	if pad := total % 4; pad != 0 {
		total += 4 - pad
	}
	if len(buf) < total {
		return 0, ErrShort
	}
	for i := 8 + int(valueLen); i < total; i++ {
		if buf[i] != 0 {
			return 0, ErrReserved
		}
	}
	m.Tag = SnapshotMetaTag(tag)
	m.Flags = flags
	m.Value = append([]byte(nil), buf[8:8+valueLen]...)
	return total, nil
}

// Snapshot is a snapshot object: one root tree pointer, a parent pointer,
// a generation number, and the snapshot's own metadata.
type Snapshot struct {
	Common               CommonHeader
	Object               ObjectHeader
	RootTree             [32]byte
	Parent               [32]byte
	Generation           uint64
	TimeSec              int64
	TimeNsec             uint32
	TzOffsetSec          int32
	TotalSize            uint64
	ReachableObjectCount uint64
	HashAlgo             HashAlgo
	ChunkerProfile       ChunkerProfile
	MetaCount            uint16
	SourceType           SnapshotSourceType
	SourceFlags          uint8
	ParentHashAlgo       HashAlgo
	ReservedU8           uint8
	Meta                 []SnapshotMeta
}

// EncodedLen is the snapshot's encoded length: both headers, the fixed
// body, and every metadata record.
func (s *Snapshot) EncodedLen() int {
	n := CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen
	for i := range s.Meta {
		n += s.Meta[i].EncodedLen()
	}
	return n
}

// Encode writes s into buf and returns the number of bytes written,
// EncodedLen().
func (s *Snapshot) Encode(buf []byte) (int, error) {
	n := s.EncodedLen()
	if len(buf) < n {
		return 0, ErrShort
	}
	if err := s.Common.Encode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if err := s.Object.Encode(buf[CommonHeaderLen : CommonHeaderLen+ObjectHeaderLen]); err != nil {
		return 0, err
	}
	body := buf[CommonHeaderLen+ObjectHeaderLen:]
	copy(body[0:32], s.RootTree[:])
	copy(body[32:64], s.Parent[:])
	binary.LittleEndian.PutUint64(body[64:72], s.Generation)
	binary.LittleEndian.PutUint64(body[72:80], uint64(s.TimeSec))
	binary.LittleEndian.PutUint32(body[80:84], s.TimeNsec)
	binary.LittleEndian.PutUint32(body[84:88], uint32(s.TzOffsetSec))
	binary.LittleEndian.PutUint64(body[88:96], s.TotalSize)
	binary.LittleEndian.PutUint64(body[96:104], s.ReachableObjectCount)
	body[104] = byte(s.HashAlgo)
	body[105] = byte(s.ChunkerProfile)
	binary.LittleEndian.PutUint16(body[106:108], s.MetaCount)
	body[108] = byte(s.SourceType)
	body[109] = s.SourceFlags
	body[110] = byte(s.ParentHashAlgo)
	body[111] = s.ReservedU8

	off := CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen
	for i := range s.Meta {
		if err := s.Meta[i].Encode(buf[off:]); err != nil {
			return 0, err
		}
		off += s.Meta[i].EncodedLen()
	}
	return n, nil
}

// Decode reads a Snapshot from the start of buf and returns the number of
// bytes it consumed. It rejects a short buffer, a magic_kind mismatch, and
// a nonzero reserved field.
func (s *Snapshot) Decode(buf []byte) (int, error) {
	if len(buf) < CommonHeaderLen+ObjectHeaderLen+SnapshotFixedLen {
		return 0, ErrShort
	}
	if err := s.Common.Decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if s.Common.MagicKind != MagicSnapshot {
		return 0, ErrBadMagic
	}
	if err := s.Object.Decode(buf[CommonHeaderLen : CommonHeaderLen+ObjectHeaderLen]); err != nil {
		return 0, err
	}
	body := buf[CommonHeaderLen+ObjectHeaderLen:]
	copy(s.RootTree[:], body[0:32])
	copy(s.Parent[:], body[32:64])
	s.Generation = binary.LittleEndian.Uint64(body[64:72])
	s.TimeSec = int64(binary.LittleEndian.Uint64(body[72:80]))
	s.TimeNsec = binary.LittleEndian.Uint32(body[80:84])
	s.TzOffsetSec = int32(binary.LittleEndian.Uint32(body[84:88]))
	s.TotalSize = binary.LittleEndian.Uint64(body[88:96])
	s.ReachableObjectCount = binary.LittleEndian.Uint64(body[96:104])
	s.HashAlgo = HashAlgo(body[104])
	s.ChunkerProfile = ChunkerProfile(body[105])
	s.MetaCount = binary.LittleEndian.Uint16(body[106:108])
	s.SourceType = SnapshotSourceType(body[108])
	s.SourceFlags = body[109]
	s.ParentHashAlgo = HashAlgo(body[110])
	s.ReservedU8 = body[111]
	if s.ReservedU8 != 0 {
		return 0, ErrReserved
	}

	off := CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen
	s.Meta = make([]SnapshotMeta, 0, s.MetaCount)
	for i := 0; i < int(s.MetaCount); i++ {
		var m SnapshotMeta
		n, err := m.Decode(buf[off:])
		if err != nil {
			return 0, err
		}
		s.Meta = append(s.Meta, m)
		off += n
	}
	return off, nil
}
