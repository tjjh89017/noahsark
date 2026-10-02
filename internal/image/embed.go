// Package image builds a full NOAHSARK disc tree, as ordinary files in an
// output directory, from a staging directory's objects and snapshots, and
// reads that tree back for verification.
package image

import _ "embed"

// FormatTxt is the on-disc FORMAT.txt: a byte copy of FORMAT.md. A
// writer writes these bytes and no others.
//
//go:embed format.txt
var FormatTxt []byte
