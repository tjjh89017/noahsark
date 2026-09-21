package format

import "encoding/binary"

// BlobEntryLen is the encoded size of one BlobEntry.
const BlobEntryLen = 40

// blobBodyLen is the fixed part of the blob payload, before the entries.
const blobBodyLen = 8

// blobFixedLen is the common header, the object header, and the fixed
// blob body, before the entries.
const blobFixedLen = CommonHeaderLen + ObjectHeaderLen + blobBodyLen

// BlobEntry is one chunk id of a file's content. An entry stores no file
// offset: the offset of an entry is the sum of Length over the entries
// before it.
type BlobEntry struct {
	ContentID [32]byte
	Length    uint64
}

// Blob is the ordered chunk ids of one file, held outside the tree entry.
type Blob struct {
	Header       CommonHeader
	ObjectHeader ObjectHeader
	EntryCount   uint64
	Entries      []BlobEntry
}

// EncodedLen is the blob's encoded length: the fixed header plus every
// entry.
func (b *Blob) EncodedLen() int {
	return blobFixedLen + len(b.Entries)*BlobEntryLen
}

// Encode writes b into buf and returns the number of bytes written,
// EncodedLen(). Encode computes header_crc32c itself; any value in
// b.ObjectHeader.HeaderCRC32C is overwritten.
func (b *Blob) Encode(buf []byte) (int, error) {
	n := b.EncodedLen()
	if len(buf) < n {
		return 0, ErrShort
	}
	if err := b.Header.Encode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	off := CommonHeaderLen
	if err := b.ObjectHeader.Encode(buf[off : off+ObjectHeaderLen]); err != nil {
		return 0, err
	}
	off += ObjectHeaderLen
	binary.LittleEndian.PutUint64(buf[off:off+8], b.EntryCount)

	crc := crc32c(buf[0:objectHeaderCRCOffset])
	b.ObjectHeader.HeaderCRC32C = crc
	binary.LittleEndian.PutUint32(buf[objectHeaderCRCOffset:objectHeaderCRCOffset+4], crc)

	entOff := blobFixedLen
	for _, e := range b.Entries {
		copy(buf[entOff:entOff+32], e.ContentID[:])
		binary.LittleEndian.PutUint64(buf[entOff+32:entOff+40], e.Length)
		entOff += BlobEntryLen
	}
	return n, nil
}

// Decode reads a Blob from buf and returns the number of bytes read. It
// rejects a short buffer, a magic_kind mismatch, a header_crc32c mismatch,
// a header_len below the fixed part this build knows, and an entry
// count that does not agree with payload_len. The entries start at
// header_len, so a larger fixed part from a later writer is skipped.
func (b *Blob) Decode(buf []byte) (int, error) {
	if len(buf) < blobFixedLen {
		return 0, ErrShort
	}
	if err := b.Header.Decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if b.Header.MagicKind != MagicBlob {
		return 0, ErrBadMagic
	}
	entriesOff, err := b.Header.fixedPartEnd(blobFixedLen)
	if err != nil {
		return 0, err
	}
	off := CommonHeaderLen
	if err := b.ObjectHeader.Decode(buf[off : off+ObjectHeaderLen]); err != nil {
		return 0, err
	}
	off += ObjectHeaderLen
	if crc32c(buf[0:objectHeaderCRCOffset]) != b.ObjectHeader.HeaderCRC32C {
		return 0, ErrCRC
	}

	b.EntryCount = binary.LittleEndian.Uint64(buf[off : off+8])

	n := entriesOff + int(b.EntryCount)*BlobEntryLen
	if len(buf) < n {
		return 0, ErrShort
	}
	if uint64(entriesOff-CommonHeaderLen-ObjectHeaderLen)+b.EntryCount*BlobEntryLen != b.ObjectHeader.PayloadLen {
		return 0, ErrBadField
	}
	b.Entries = make([]BlobEntry, 0, b.EntryCount)
	entOff := entriesOff
	for i := uint64(0); i < b.EntryCount; i++ {
		var e BlobEntry
		copy(e.ContentID[:], buf[entOff:entOff+32])
		e.Length = binary.LittleEndian.Uint64(buf[entOff+32 : entOff+40])
		b.Entries = append(b.Entries, e)
		entOff += BlobEntryLen
	}
	return n, nil
}

// BlobOffsets returns the file offset of each entry: the sum of the
// lengths of the entries before it. An entry stores no offset of its
// own, and the entries are in file order.
func BlobOffsets(entries []BlobEntry) []uint64 {
	offsets := make([]uint64, len(entries))
	var off uint64
	for i, e := range entries {
		offsets[i] = off
		off += e.Length
	}
	return offsets
}
