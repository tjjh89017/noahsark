package format

import (
	"bytes"
	"encoding/binary"
)

// treeBodyFixedLen is the fixed part of the tree payload, before the
// entries: entry_count and a reserved u32.
const treeBodyFixedLen = 8

// treeFixedLen is the common header, the object header, and the fixed
// tree body, before the entries.
const treeFixedLen = CommonHeaderLen + ObjectHeaderLen + treeBodyFixedLen

// TreeEntryHeaderLen is the fixed header length of one tree entry.
const TreeEntryHeaderLen = 112

// Entry type values of TreeEntry.EntryType.
const (
	EntryTypeRegular   uint8 = 1
	EntryTypeDirectory uint8 = 2
	EntryTypeSymlink   uint8 = 3
	EntryTypeCharDev   uint8 = 4
	EntryTypeBlockDev  uint8 = 5
	EntryTypeFIFO      uint8 = 6
	EntryTypeSocket    uint8 = 7
)

// Entry flag bits of TreeEntry.EntryFlags.
const (
	EntryFlagHardlinkMember uint8 = 1 << 0
	EntryFlagAtimeAbsent    uint8 = 1 << 1
	EntryFlagCtimeAbsent    uint8 = 1 << 2
	EntryFlagBtimeAbsent    uint8 = 1 << 3
	EntryFlagSparse         uint8 = 1 << 4
	EntryFlagMetadataPart   uint8 = 1 << 5
	EntryFlagUnstable       uint8 = 1 << 7
)

// Tree is one directory, with one entry per child.
type Tree struct {
	Header       CommonHeader
	ObjectHeader ObjectHeader
	EntryCount   uint32
	ReservedU32  uint32
	Entries      []TreeEntry
}

// TreeEntry is one child of a tree, its fixed header, name, content
// reference, and extension TLVs.
type TreeEntry struct {
	EntryType     uint8
	EntryFlags    uint8
	Size          uint64
	HardlinkGroup uint64
	MtimeSec      int64
	AtimeSec      int64
	CtimeSec      int64
	BtimeSec      int64
	MtimeNsec     uint32
	AtimeNsec     uint32
	CtimeNsec     uint32
	BtimeNsec     uint32
	Mode          uint32
	UID           uint32
	GID           uint32
	RdevMajor     uint32
	RdevMinor     uint32
	Name          []byte
	// ContentID holds the blob id (regular file) or tree id (directory).
	// It is unused for every other entry type.
	ContentID [32]byte
	TLVs      []TLV
}

// contentLen is 32 for a regular file or a directory, since only those
// entry types carry a content reference; every other entry type has none.
func (e *TreeEntry) contentLen() int {
	switch e.EntryType {
	case EntryTypeRegular, EntryTypeDirectory:
		return 32
	default:
		return 0
	}
}

// sortKey is the raw name bytes used for canonical ordering, with a
// trailing '/' appended for a directory.
func (e *TreeEntry) sortKey() []byte {
	if e.EntryType == EntryTypeDirectory {
		return append(append([]byte(nil), e.Name...), '/')
	}
	return e.Name
}

// EncodedLen is the entry's length on the medium, the fixed header, the
// name, the content reference area, and the TLV area, each 8-byte
// aligned, rounded up to a multiple of 8.
func (e *TreeEntry) EncodedLen() int {
	pos := TreeEntryHeaderLen + len(e.Name)
	if cl := e.contentLen(); cl > 0 {
		pos = align8(pos) + cl
	}
	extLen := 0
	for i := range e.TLVs {
		extLen += e.TLVs[i].EncodedLen()
	}
	if extLen > 0 {
		pos = align8(pos) + extLen
	}
	return align8(pos)
}

// Encode writes e into buf and returns the number of bytes written,
// EncodedLen(). buf must be at least that many bytes.
func (e *TreeEntry) Encode(buf []byte) (int, error) {
	nameLen := len(e.Name)
	if nameLen < 1 || nameLen > 4095 {
		return 0, ErrBadField
	}
	contentLen := e.contentLen()
	extLen := 0
	for i := range e.TLVs {
		extLen += e.TLVs[i].EncodedLen()
	}

	pos := TreeEntryHeaderLen + nameLen
	contentOff := 0
	if contentLen > 0 {
		contentOff = align8(pos)
		pos = contentOff + contentLen
	}
	extOff := 0
	if extLen > 0 {
		extOff = align8(pos)
		pos = extOff + extLen
	}
	entryLen := align8(pos)
	if entryLen > len(buf) {
		return 0, ErrShort
	}

	binary.LittleEndian.PutUint32(buf[0:4], uint32(entryLen))
	binary.LittleEndian.PutUint16(buf[4:6], TreeEntryHeaderLen)
	buf[6] = e.EntryType
	buf[7] = e.EntryFlags
	binary.LittleEndian.PutUint64(buf[8:16], e.Size)
	binary.LittleEndian.PutUint64(buf[16:24], e.HardlinkGroup)
	binary.LittleEndian.PutUint64(buf[24:32], uint64(e.MtimeSec))
	binary.LittleEndian.PutUint64(buf[32:40], uint64(e.AtimeSec))
	binary.LittleEndian.PutUint64(buf[40:48], uint64(e.CtimeSec))
	binary.LittleEndian.PutUint64(buf[48:56], uint64(e.BtimeSec))
	binary.LittleEndian.PutUint32(buf[56:60], e.MtimeNsec)
	binary.LittleEndian.PutUint32(buf[60:64], e.AtimeNsec)
	binary.LittleEndian.PutUint32(buf[64:68], e.CtimeNsec)
	binary.LittleEndian.PutUint32(buf[68:72], e.BtimeNsec)
	binary.LittleEndian.PutUint32(buf[72:76], e.Mode)
	binary.LittleEndian.PutUint32(buf[76:80], e.UID)
	binary.LittleEndian.PutUint32(buf[80:84], e.GID)
	binary.LittleEndian.PutUint32(buf[84:88], e.RdevMajor)
	binary.LittleEndian.PutUint32(buf[88:92], e.RdevMinor)
	binary.LittleEndian.PutUint32(buf[92:96], uint32(contentOff))
	binary.LittleEndian.PutUint32(buf[96:100], uint32(contentLen))
	binary.LittleEndian.PutUint32(buf[100:104], uint32(extOff))
	binary.LittleEndian.PutUint32(buf[104:108], uint32(extLen))
	binary.LittleEndian.PutUint16(buf[108:110], TreeEntryHeaderLen)
	binary.LittleEndian.PutUint16(buf[110:112], uint16(nameLen))

	for i := TreeEntryHeaderLen; i < entryLen; i++ {
		buf[i] = 0
	}
	copy(buf[TreeEntryHeaderLen:TreeEntryHeaderLen+nameLen], e.Name)
	if contentLen > 0 {
		copy(buf[contentOff:contentOff+contentLen], e.ContentID[:])
	}
	if extLen > 0 {
		p := extOff
		for i := range e.TLVs {
			n, err := e.TLVs[i].Encode(buf[p:])
			if err != nil {
				return 0, err
			}
			p += n
		}
	}
	return entryLen, nil
}

// Decode reads one TreeEntry from buf and returns the number of bytes
// read. It rejects a short buffer, an invalid name, a nonzero reserved
// byte, an unknown critical TLV, and a TLV area out of canonical order.
func (e *TreeEntry) Decode(buf []byte) (int, error) {
	if len(buf) < TreeEntryHeaderLen {
		return 0, ErrShort
	}
	entryLen := int(binary.LittleEndian.Uint32(buf[0:4]))
	if entryLen < TreeEntryHeaderLen || entryLen%8 != 0 || entryLen > len(buf) {
		return 0, ErrBadField
	}
	headerLen := int(binary.LittleEndian.Uint16(buf[4:6]))
	if headerLen < TreeEntryHeaderLen {
		return 0, ErrBadField
	}
	entryType := buf[6]
	if entryType < EntryTypeRegular || entryType > EntryTypeSocket {
		return 0, ErrBadField
	}
	entryFlags := buf[7]
	size := binary.LittleEndian.Uint64(buf[8:16])
	hardlinkGroup := binary.LittleEndian.Uint64(buf[16:24])
	mtimeSec := int64(binary.LittleEndian.Uint64(buf[24:32]))
	atimeSec := int64(binary.LittleEndian.Uint64(buf[32:40]))
	ctimeSec := int64(binary.LittleEndian.Uint64(buf[40:48]))
	btimeSec := int64(binary.LittleEndian.Uint64(buf[48:56]))
	mtimeNsec := binary.LittleEndian.Uint32(buf[56:60])
	atimeNsec := binary.LittleEndian.Uint32(buf[60:64])
	ctimeNsec := binary.LittleEndian.Uint32(buf[64:68])
	btimeNsec := binary.LittleEndian.Uint32(buf[68:72])
	mode := binary.LittleEndian.Uint32(buf[72:76])
	uid := binary.LittleEndian.Uint32(buf[76:80])
	gid := binary.LittleEndian.Uint32(buf[80:84])
	rdevMajor := binary.LittleEndian.Uint32(buf[84:88])
	rdevMinor := binary.LittleEndian.Uint32(buf[88:92])
	contentOff := int(binary.LittleEndian.Uint32(buf[92:96]))
	contentLen := int(binary.LittleEndian.Uint32(buf[96:100]))
	extOff := int(binary.LittleEndian.Uint32(buf[100:104]))
	extLen := int(binary.LittleEndian.Uint32(buf[104:108]))
	nameOff := int(binary.LittleEndian.Uint16(buf[108:110]))
	nameLen := int(binary.LittleEndian.Uint16(buf[110:112]))

	if nameLen < 1 || nameLen > 4095 || nameOff+nameLen > entryLen {
		return 0, ErrBadField
	}
	name := append([]byte(nil), buf[nameOff:nameOff+nameLen]...)
	if err := validateEntryName(name); err != nil {
		return 0, err
	}

	last := nameOff + nameLen
	var contentID [32]byte
	if contentLen > 0 {
		if contentLen != 32 || contentOff+contentLen > entryLen {
			return 0, ErrBadField
		}
		if err := checkZero(buf, last, contentOff); err != nil {
			return 0, err
		}
		copy(contentID[:], buf[contentOff:contentOff+contentLen])
		last = contentOff + contentLen
	} else if contentOff != 0 {
		return 0, ErrBadField
	}

	var tlvs []TLV
	if extLen > 0 {
		if extOff+extLen > entryLen {
			return 0, ErrBadField
		}
		if err := checkZero(buf, last, extOff); err != nil {
			return 0, err
		}
		p := extOff
		end := extOff + extLen
		var prev *TLV
		for p < end {
			var t TLV
			n, err := t.Decode(buf[p:end])
			if err != nil {
				return 0, err
			}
			if t.Flags&TLVFlagCritical != 0 && !tlvKnownTypes[t.Type] && !tlvIsVendor(t.Type) {
				return 0, ErrBadField
			}
			if prev != nil {
				cmp := bytes.Compare(prev.sortBytes(), t.sortBytes())
				if cmp > 0 {
					return 0, ErrBadField
				}
				if cmp == 0 && prev.Type == t.Type && !tlvIsVendor(t.Type) {
					return 0, ErrBadField
				}
			}
			tlvs = append(tlvs, t)
			prevCopy := t
			prev = &prevCopy
			p += n
		}
		last = extOff + extLen
	} else if extOff != 0 {
		return 0, ErrBadField
	}

	if err := checkZero(buf, last, entryLen); err != nil {
		return 0, err
	}

	e.EntryType = entryType
	e.EntryFlags = entryFlags
	e.Size = size
	e.HardlinkGroup = hardlinkGroup
	e.MtimeSec = mtimeSec
	e.AtimeSec = atimeSec
	e.CtimeSec = ctimeSec
	e.BtimeSec = btimeSec
	e.MtimeNsec = mtimeNsec
	e.AtimeNsec = atimeNsec
	e.CtimeNsec = ctimeNsec
	e.BtimeNsec = btimeNsec
	e.Mode = mode
	e.UID = uid
	e.GID = gid
	e.RdevMajor = rdevMajor
	e.RdevMinor = rdevMinor
	e.Name = name
	e.ContentID = contentID
	e.TLVs = tlvs
	return entryLen, nil
}

// sortBytes is the tlv_type, big-endian so byte order matches numeric
// order, followed by the payload, the canonical TLV ordering key.
func (t *TLV) sortBytes() []byte {
	key := make([]byte, 2+len(t.Payload))
	binary.BigEndian.PutUint16(key[0:2], t.Type)
	copy(key[2:], t.Payload)
	return key
}

// validateEntryName rejects an empty name, ".", "..", and a name
// containing '/', '\' or NUL.
func validateEntryName(name []byte) error {
	if len(name) == 0 {
		return ErrBadField
	}
	if string(name) == "." || string(name) == ".." {
		return ErrBadField
	}
	for _, b := range name {
		if b == '/' || b == '\\' || b == 0 {
			return ErrBadField
		}
	}
	return nil
}

// EncodedLen is the tree's encoded length: the fixed header plus the sum
// of each entry's EncodedLen().
func (t *Tree) EncodedLen() int {
	n := treeFixedLen
	for i := range t.Entries {
		n += t.Entries[i].EncodedLen()
	}
	return n
}

// Encode writes t into buf and returns the number of bytes written,
// EncodedLen(). Encode computes header_crc32c itself; any value in
// t.ObjectHeader.HeaderCRC32C is overwritten.
func (t *Tree) Encode(buf []byte) (int, error) {
	n := t.EncodedLen()
	if len(buf) < n {
		return 0, ErrShort
	}
	if err := t.Header.Encode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	off := CommonHeaderLen
	if err := t.ObjectHeader.Encode(buf[off : off+ObjectHeaderLen]); err != nil {
		return 0, err
	}
	off += ObjectHeaderLen
	binary.LittleEndian.PutUint32(buf[off:off+4], t.EntryCount)
	binary.LittleEndian.PutUint32(buf[off+4:off+8], t.ReservedU32)

	crc := crc32c(buf[0:objectHeaderCRCOffset])
	binary.LittleEndian.PutUint32(buf[objectHeaderCRCOffset:objectHeaderCRCOffset+4], crc)

	pos := treeFixedLen
	for i := range t.Entries {
		written, err := t.Entries[i].Encode(buf[pos:])
		if err != nil {
			return 0, err
		}
		pos += written
	}
	return n, nil
}

// Decode reads a Tree from buf and returns the number of bytes read. It
// rejects a short buffer, a magic_kind mismatch, a header_crc32c
// mismatch, a nonzero reserved field, and tree entries not in ascending
// canonical order.
func (t *Tree) Decode(buf []byte) (int, error) {
	if len(buf) < treeFixedLen {
		return 0, ErrShort
	}
	if err := t.Header.Decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if t.Header.MagicKind != MagicTree {
		return 0, ErrBadMagic
	}
	off := CommonHeaderLen
	if err := t.ObjectHeader.Decode(buf[off : off+ObjectHeaderLen]); err != nil {
		return 0, err
	}
	off += ObjectHeaderLen
	if crc32c(buf[0:objectHeaderCRCOffset]) != t.ObjectHeader.HeaderCRC32C {
		return 0, ErrCRC
	}

	entryCount := binary.LittleEndian.Uint32(buf[off : off+4])
	reservedU32 := binary.LittleEndian.Uint32(buf[off+4 : off+8])
	if reservedU32 != 0 {
		return 0, ErrReserved
	}

	t.EntryCount = entryCount
	t.ReservedU32 = reservedU32

	pos := treeFixedLen
	t.Entries = nil
	var prev *TreeEntry
	for range entryCount {
		if pos >= len(buf) {
			return 0, ErrShort
		}
		var e TreeEntry
		n, err := e.Decode(buf[pos:])
		if err != nil {
			return 0, err
		}
		if prev != nil && bytes.Compare(prev.sortKey(), e.sortKey()) >= 0 {
			return 0, ErrBadField
		}
		t.Entries = append(t.Entries, e)
		prevCopy := e
		prev = &prevCopy
		pos += n
	}
	return pos, nil
}
