package format

import "encoding/binary"

// CommonHeaderLen is the encoded size of CommonHeader.
const CommonHeaderLen = 32

// ObjectHeaderLen is the encoded size of ObjectHeader.
const ObjectHeaderLen = 32

// CommonHeader begins every structure. It carries identity and versioning
// in one place, so a scan of the raw medium can find a structure by its
// magic and read enough to find the structure's own end.
type CommonHeader struct {
	MagicProject Magic
	MagicKind    Magic
	VersionMajor uint16
	VersionMinor uint16
	HeaderLen    uint16
	ReservedU16  uint16
	ReservedU64  uint64
}

// Encode writes h into buf[0:CommonHeaderLen]. buf must be at least
// CommonHeaderLen bytes.
func (h *CommonHeader) Encode(buf []byte) error {
	if len(buf) < CommonHeaderLen {
		return ErrShort
	}
	copy(buf[0:8], h.MagicProject[:])
	copy(buf[8:16], h.MagicKind[:])
	binary.LittleEndian.PutUint16(buf[16:18], h.VersionMajor)
	binary.LittleEndian.PutUint16(buf[18:20], h.VersionMinor)
	binary.LittleEndian.PutUint16(buf[20:22], h.HeaderLen)
	binary.LittleEndian.PutUint16(buf[22:24], h.ReservedU16)
	binary.LittleEndian.PutUint64(buf[24:32], h.ReservedU64)
	return nil
}

// Decode reads a CommonHeader from buf. It rejects a short buffer, a
// magic_project mismatch, and an unknown version_major.
func (h *CommonHeader) Decode(buf []byte) error {
	if len(buf) < CommonHeaderLen {
		return ErrShort
	}
	var magicProject, magicKind Magic
	copy(magicProject[:], buf[0:8])
	copy(magicKind[:], buf[8:16])
	if magicProject != ProjectMagic {
		return ErrBadMagic
	}
	versionMajor := binary.LittleEndian.Uint16(buf[16:18])
	if versionMajor != 1 {
		return ErrVersion
	}
	h.MagicProject = magicProject
	h.MagicKind = magicKind
	h.VersionMajor = versionMajor
	h.VersionMinor = binary.LittleEndian.Uint16(buf[18:20])
	h.HeaderLen = binary.LittleEndian.Uint16(buf[20:22])
	h.ReservedU16 = binary.LittleEndian.Uint16(buf[22:24])
	h.ReservedU64 = binary.LittleEndian.Uint64(buf[24:32])
	return nil
}

// ObjectHeader follows the common header in every object file. An object
// file carries no magic or version fields of its own beyond the common
// header.
type ObjectHeader struct {
	Kind         ObjectKind
	HashAlgo     HashAlgo
	DigestLen    uint8
	Compression  Compression
	Crypto       uint8
	ReservedU8   uint8
	ReservedU16  uint16
	PayloadLen   uint64
	StoredLen    uint64
	HeaderCRC32C uint32
	ReservedU32  uint32
}

// Encode writes h into buf[0:ObjectHeaderLen]. buf must be at least
// ObjectHeaderLen bytes.
func (h *ObjectHeader) Encode(buf []byte) error {
	if len(buf) < ObjectHeaderLen {
		return ErrShort
	}
	buf[0] = byte(h.Kind)
	buf[1] = byte(h.HashAlgo)
	buf[2] = h.DigestLen
	buf[3] = byte(h.Compression)
	buf[4] = h.Crypto
	buf[5] = h.ReservedU8
	binary.LittleEndian.PutUint16(buf[6:8], h.ReservedU16)
	binary.LittleEndian.PutUint64(buf[8:16], h.PayloadLen)
	binary.LittleEndian.PutUint64(buf[16:24], h.StoredLen)
	binary.LittleEndian.PutUint32(buf[24:28], h.HeaderCRC32C)
	binary.LittleEndian.PutUint32(buf[28:32], h.ReservedU32)
	return nil
}

// Decode reads an ObjectHeader from buf. It rejects a short buffer. The
// object header carries no magic or version of its own; the common header
// that precedes it carries those.
func (h *ObjectHeader) Decode(buf []byte) error {
	if len(buf) < ObjectHeaderLen {
		return ErrShort
	}
	h.Kind = ObjectKind(buf[0])
	h.HashAlgo = HashAlgo(buf[1])
	h.DigestLen = buf[2]
	h.Compression = Compression(buf[3])
	h.Crypto = buf[4]
	h.ReservedU8 = buf[5]
	h.ReservedU16 = binary.LittleEndian.Uint16(buf[6:8])
	h.PayloadLen = binary.LittleEndian.Uint64(buf[8:16])
	h.StoredLen = binary.LittleEndian.Uint64(buf[16:24])
	h.HeaderCRC32C = binary.LittleEndian.Uint32(buf[24:28])
	h.ReservedU32 = binary.LittleEndian.Uint32(buf[28:32])
	return nil
}
