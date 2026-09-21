package format

// Magic is an 8-byte ASCII magic value, zero-padded when the name is
// shorter than 8 bytes. A reader compares all 8 bytes, padding included.
type Magic [8]byte

// magicFromString computes an 8-byte magic from its ASCII name. It never
// copies a hexadecimal column.
func magicFromString(name string) Magic {
	var m Magic
	copy(m[:], name)
	return m
}

// ProjectMagic is magic_project, the 8 ASCII bytes every common header
// carries.
var ProjectMagic = magicFromString("NOAHSARK")

// Structure magic_kind values.
var (
	MagicChunk    = magicFromString("CHUNK")
	MagicBlob     = magicFromString("BLOB")
	MagicTree     = magicFromString("TREE")
	MagicSnapshot = magicFromString("SNAPSHOT")
	MagicDisc     = magicFromString("DISC")
	MagicRun      = magicFromString("RUN")
	MagicIndex    = magicFromString("INDEX")
	MagicChecksum = magicFromString("CHECKSUM")
	MagicRefs     = magicFromString("REFS")
	MagicDiscs    = magicFromString("DISCS")
)

// ObjectKind is the object kind registry.
type ObjectKind uint8

const (
	ObjectKindChunk    ObjectKind = 1
	ObjectKindBlob     ObjectKind = 2
	ObjectKindTree     ObjectKind = 3
	ObjectKindSnapshot ObjectKind = 4
)

// HashAlgo is the hash algorithm registry. Values are multicodec codes.
type HashAlgo uint8

// HashAlgoSHA256 is the only algorithm of format major 1.
const HashAlgoSHA256 HashAlgo = 0x12

// Compression is the compression registry.
type Compression uint8

const (
	CompressionNone Compression = 0
	CompressionZstd Compression = 1
)

// FECScheme is the FEC scheme registry.
type FECScheme uint8

// FECSchemeNone is the default scheme: no checksum column and no
// parity. Burning two identical discs is the primary redundancy; FEC is
// a reserve feature a run opts into.
const FECSchemeNone FECScheme = 0

// FECSchemeRS255GF8 is the Reed-Solomon scheme, opt-in.
const FECSchemeRS255GF8 FECScheme = 1
