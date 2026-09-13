package format

import "errors"

// ErrBadMagic reports a magic_project or magic_kind mismatch.
var ErrBadMagic = errors.New("format: bad magic")

// ErrShort reports a buffer too short for the structure it should hold.
var ErrShort = errors.New("format: buffer too short")

// ErrVersion reports an unknown version_major.
var ErrVersion = errors.New("format: unsupported version_major")

// ErrReserved reports a reserved field or padding byte that is not zero.
var ErrReserved = errors.New("format: reserved field not zero")
