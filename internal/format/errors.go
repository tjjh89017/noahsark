package format

import "errors"

// ErrBadMagic reports a magic_project or magic_kind mismatch.
var ErrBadMagic = errors.New("format: bad magic")

// ErrShort reports a buffer too short for the structure it should hold.
var ErrShort = errors.New("format: buffer too short")

// ErrVersion reports an unknown version_major.
var ErrVersion = errors.New("format: unsupported version_major")

// ErrCRC reports a CRC-32C field that does not match its covered bytes.
var ErrCRC = errors.New("format: crc mismatch")

// ErrBadField reports a field value FORMAT.md forbids, such as an unknown
// critical extension, an out-of-range enum, or an out-of-order record.
var ErrBadField = errors.New("format: field has an invalid value")

// ErrObjectKind reports an Objects row whose kind is outside 1 to 4.
var ErrObjectKind = errors.New("format: object kind out of range")

// ErrFileRole reports an INDEX Files row whose role is a reserved id of
// the file role registry.
var ErrFileRole = errors.New("format: reserved file role")

// ErrHeaderLen reports a header_len other than the one this build knows
// for the structure and its version_major.
var ErrHeaderLen = errors.New("format: header_len is not the known value")

// ErrReserved reports a nonzero reserved field, reserved bit or padding
// byte.
var ErrReserved = errors.New("format: nonzero reserved field")
