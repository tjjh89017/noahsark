package format

import "encoding/binary"

// chunkFixedLen is the common header plus the object header. A chunk
// carries no fixed body of its own; the payload follows directly.
const chunkFixedLen = CommonHeaderLen + ObjectHeaderLen

// Chunk is opaque file content bytes, addressed by the hash of those
// bytes. A chunk carries no reference to another object.
type Chunk struct {
	Header       CommonHeader
	ObjectHeader ObjectHeader
	Payload      []byte
}

// EncodedLen is the chunk's encoded length: the fixed header plus the
// payload.
func (c *Chunk) EncodedLen() int {
	return chunkFixedLen + len(c.Payload)
}

// Encode writes c into buf and returns the number of bytes written,
// EncodedLen(). Encode computes header_crc32c itself; any value in
// c.ObjectHeader.HeaderCRC32C is overwritten.
func (c *Chunk) Encode(buf []byte) (int, error) {
	n := c.EncodedLen()
	if len(buf) < n {
		return 0, ErrShort
	}
	if err := c.Header.Encode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if err := c.ObjectHeader.Encode(buf[CommonHeaderLen:chunkFixedLen]); err != nil {
		return 0, err
	}
	crc := crc32c(buf[0:objectHeaderCRCOffset])
	binary.LittleEndian.PutUint32(buf[objectHeaderCRCOffset:objectHeaderCRCOffset+4], crc)
	copy(buf[chunkFixedLen:n], c.Payload)
	return n, nil
}

// Decode reads a Chunk from buf and returns the number of bytes read. It
// rejects a short buffer, a magic_kind mismatch, and a header_crc32c
// mismatch.
func (c *Chunk) Decode(buf []byte) (int, error) {
	if len(buf) < chunkFixedLen {
		return 0, ErrShort
	}
	if err := c.Header.Decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if c.Header.MagicKind != MagicChunk {
		return 0, ErrBadMagic
	}
	if c.Header.HeaderLen != chunkFixedLen {
		return 0, ErrHeaderLen
	}
	if err := c.ObjectHeader.Decode(buf[CommonHeaderLen:chunkFixedLen]); err != nil {
		return 0, err
	}
	if crc32c(buf[0:objectHeaderCRCOffset]) != c.ObjectHeader.HeaderCRC32C {
		return 0, ErrCRC
	}
	stored := int(c.ObjectHeader.StoredLen)
	n := chunkFixedLen + stored
	if len(buf) < n {
		return 0, ErrShort
	}
	c.Payload = append([]byte(nil), buf[chunkFixedLen:n]...)
	return n, nil
}
