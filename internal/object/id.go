// Package object builds chunk, blob, tree and snapshot objects from a
// source directory and writes them into a staging directory.
package object

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ID is a content id: the SHA-256 digest of an object's uncompressed
// payload bytes. Nothing else enters the id.
type ID [32]byte

// ComputeID returns the content id of payload, the object's uncompressed
// payload bytes.
func ComputeID(payload []byte) ID {
	return sha256.Sum256(payload)
}

// multihashSHA256Code and multihashDigestLen are the two multihash varint
// fields ahead of the digest. Both fit in one byte.
const (
	multihashSHA256Code = 0x12
	multihashDigestLen  = 32
)

// TextForm returns the id's on-disc file name: the lowercase hex of its
// multihash bytes, algorithm code varint, digest length varint, digest.
func (id ID) TextForm() string {
	buf := make([]byte, 0, 2+len(id))
	buf = appendVarint(buf, multihashSHA256Code)
	buf = appendVarint(buf, multihashDigestLen)
	buf = append(buf, id[:]...)
	return hex.EncodeToString(buf)
}

// ParseID parses an id in the text form TextForm produces: lowercase hex
// of the multihash varint prefix (algorithm code 0x12, length 32)
// followed by the 32-byte digest.
func ParseID(s string) (ID, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return ID{}, fmt.Errorf("object: id %q: %w", s, err)
	}
	if len(raw) != 2+multihashDigestLen || raw[0] != multihashSHA256Code || raw[1] != multihashDigestLen {
		return ID{}, fmt.Errorf("object: id %q: not a sha256 multihash id", s)
	}
	var id ID
	copy(id[:], raw[2:])
	return id, nil
}

// FanoutByte returns the two lowercase hex digits of the digest's first
// byte, the fan-out directory name under objects/.
func (id ID) FanoutByte() string {
	return hex.EncodeToString(id[0:1])
}

// appendVarint appends v as an unsigned LEB128 varint.
func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}
