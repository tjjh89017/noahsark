package format

import "encoding/binary"

// BlobEntryLen is the encoded size of one BlobEntry.
const BlobEntryLen = 48

// blobBodyLen is the fixed part of the blob payload, before the entries.
const blobBodyLen = 24

// blobFixedLen is the common header, the object header, and the fixed
// blob body, before the entries.
const blobFixedLen = CommonHeaderLen + ObjectHeaderLen + blobBodyLen

// BlobEntry is one chunk id, or child blob id, of a file's content.
type BlobEntry struct {
	ContentID  [32]byte
	Length     uint64
	FileOffset uint64
}

// Blob is the ordered chunk ids of one file, held outside the tree entry.
type Blob struct {
	Header       CommonHeader
	ObjectHeader ObjectHeader
	EntryCount   uint64
	TotalSize    uint64
	EntrySize    uint16
	HashAlgo     HashAlgo
	DigestLen    uint8
	// Level is 0 when entries are chunks, 1 when entries are blobs. A
	// version 1 writer never writes 1.
	Level    uint8
	Reserved [3]byte
	Entries  []BlobEntry
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
	binary.LittleEndian.PutUint64(buf[off+8:off+16], b.TotalSize)
	binary.LittleEndian.PutUint16(buf[off+16:off+18], b.EntrySize)
	buf[off+18] = byte(b.HashAlgo)
	buf[off+19] = b.DigestLen
	buf[off+20] = b.Level
	copy(buf[off+21:off+24], b.Reserved[:])

	crc := crc32c(buf[0:objectHeaderCRCOffset])
	binary.LittleEndian.PutUint32(buf[objectHeaderCRCOffset:objectHeaderCRCOffset+4], crc)

	entOff := blobFixedLen
	for _, e := range b.Entries {
		copy(buf[entOff:entOff+32], e.ContentID[:])
		binary.LittleEndian.PutUint64(buf[entOff+32:entOff+40], e.Length)
		binary.LittleEndian.PutUint64(buf[entOff+40:entOff+48], e.FileOffset)
		entOff += BlobEntryLen
	}
	return n, nil
}

// Decode reads a Blob from buf and returns the number of bytes read. It
// rejects a short buffer, a magic_kind mismatch, a header_crc32c
// mismatch, a nonzero reserved byte, and a level above 1.
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
	off := CommonHeaderLen
	if err := b.ObjectHeader.Decode(buf[off : off+ObjectHeaderLen]); err != nil {
		return 0, err
	}
	off += ObjectHeaderLen
	if crc32c(buf[0:objectHeaderCRCOffset]) != b.ObjectHeader.HeaderCRC32C {
		return 0, ErrCRC
	}

	b.EntryCount = binary.LittleEndian.Uint64(buf[off : off+8])
	b.TotalSize = binary.LittleEndian.Uint64(buf[off+8 : off+16])
	b.EntrySize = binary.LittleEndian.Uint16(buf[off+16 : off+18])
	b.HashAlgo = HashAlgo(buf[off+18])
	b.DigestLen = buf[off+19]
	b.Level = buf[off+20]
	copy(b.Reserved[:], buf[off+21:off+24])
	if b.Reserved != ([3]byte{}) {
		return 0, ErrReserved
	}
	if b.Level > 1 {
		return 0, ErrBadField
	}

	n := blobFixedLen + int(b.EntryCount)*BlobEntryLen
	if len(buf) < n {
		return 0, ErrShort
	}
	b.Entries = nil
	entOff := blobFixedLen
	for i := uint64(0); i < b.EntryCount; i++ {
		var e BlobEntry
		copy(e.ContentID[:], buf[entOff:entOff+32])
		e.Length = binary.LittleEndian.Uint64(buf[entOff+32 : entOff+40])
		e.FileOffset = binary.LittleEndian.Uint64(buf[entOff+40 : entOff+48])
		b.Entries = append(b.Entries, e)
		entOff += BlobEntryLen
	}
	return n, nil
}
