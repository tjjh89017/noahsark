package format

import "encoding/binary"

const (
	// ChecksumRecordLen is the encoded size of one checksum column block.
	ChecksumRecordLen = 2048
	// ChecksumDigestsAreaLen is the size of the digests area of a record.
	ChecksumDigestsAreaLen = 1848
	// ChecksumDigestSize is the size of one block digest.
	ChecksumDigestSize = 8
	// checksumReservedLen is the size of the record's trailing reserved area.
	checksumReservedLen = 180
	// checksumHeaderLen is the size of the record's fixed header, before
	// the digests area.
	checksumHeaderLen = 20
)

// ChecksumRecord is one 2048-byte block of a run's checksum column. It
// carries the magic_kind as a record magic; it does not carry the rest of
// the common header, because it is a record of the column, not a structure
// of its own.
type ChecksumRecord struct {
	StripeIndex  uint32
	DigestCount  uint16
	DigestBytes  uint8
	HashAlgo     HashAlgo
	HeaderCRC32C uint32
	// Digests holds DigestCount entries, in data column order. Encode
	// zero-pads the digests area past the last entry.
	Digests  [][8]byte
	Reserved [checksumReservedLen]byte
}

// Encode writes r into buf[0:ChecksumRecordLen]. It computes
// header_crc32c over bytes 0 to 15 and overwrites r.HeaderCRC32C with it.
// buf must be at least ChecksumRecordLen bytes.
func (r *ChecksumRecord) Encode(buf []byte) error {
	if len(buf) < ChecksumRecordLen {
		return ErrShort
	}
	if len(r.Digests)*ChecksumDigestSize > ChecksumDigestsAreaLen {
		return ErrShort
	}
	copy(buf[0:8], MagicChecksum[:])
	binary.LittleEndian.PutUint32(buf[8:12], r.StripeIndex)
	binary.LittleEndian.PutUint16(buf[12:14], r.DigestCount)
	buf[14] = r.DigestBytes
	buf[15] = byte(r.HashAlgo)

	digests := buf[checksumHeaderLen : checksumHeaderLen+ChecksumDigestsAreaLen]
	for i := range digests {
		digests[i] = 0
	}
	for i, d := range r.Digests {
		copy(digests[i*ChecksumDigestSize:(i+1)*ChecksumDigestSize], d[:])
	}

	headerCRC := crc32c(buf[0:16])
	r.HeaderCRC32C = headerCRC
	binary.LittleEndian.PutUint32(buf[16:20], headerCRC)

	reserved := buf[checksumHeaderLen+ChecksumDigestsAreaLen : ChecksumRecordLen]
	for i := range reserved {
		reserved[i] = 0
	}
	return nil
}

// Decode reads a ChecksumRecord from buf. It rejects a short buffer, a
// magic_kind mismatch, a header_crc32c mismatch, and a nonzero reserved
// byte.
func (r *ChecksumRecord) Decode(buf []byte) error {
	if len(buf) < ChecksumRecordLen {
		return ErrShort
	}
	var magicKind Magic
	copy(magicKind[:], buf[0:8])
	if magicKind != MagicChecksum {
		return ErrBadMagic
	}
	headerCRC := binary.LittleEndian.Uint32(buf[16:20])
	if headerCRC != crc32c(buf[0:16]) {
		return ErrCRC
	}

	r.StripeIndex = binary.LittleEndian.Uint32(buf[8:12])
	r.DigestCount = binary.LittleEndian.Uint16(buf[12:14])
	r.DigestBytes = buf[14]
	r.HashAlgo = HashAlgo(buf[15])
	r.HeaderCRC32C = headerCRC

	digestCount := int(r.DigestCount)
	if digestCount*ChecksumDigestSize > ChecksumDigestsAreaLen {
		return ErrShort
	}
	digests := buf[checksumHeaderLen : checksumHeaderLen+ChecksumDigestsAreaLen]
	r.Digests = make([][8]byte, digestCount)
	for i := range r.Digests {
		copy(r.Digests[i][:], digests[i*ChecksumDigestSize:(i+1)*ChecksumDigestSize])
	}
	for _, b := range digests[digestCount*ChecksumDigestSize:] {
		if b != 0 {
			return ErrReserved
		}
	}

	copy(r.Reserved[:], buf[checksumHeaderLen+ChecksumDigestsAreaLen:ChecksumRecordLen])
	for _, b := range r.Reserved {
		if b != 0 {
			return ErrReserved
		}
	}
	return nil
}
