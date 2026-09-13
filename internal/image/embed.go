// Package image builds a full NOAHSARK disc tree, as ordinary files in an
// output directory, from a staging directory's objects and snapshots, and
// reads that tree back for verification.
package image

import _ "embed"

// DecoderPy is the reference decoder, copied byte for byte from
// reference/decoder.py at build time. Every disc carries these exact
// bytes as REFERENCE/decoder.py.
//
//go:embed decoder.py
var DecoderPy []byte
