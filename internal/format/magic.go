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
	// MagicBundle is reserved for a later version. A Phase 1 writer never
	// emits it; a reader refuses it.
	MagicBundle   = magicFromString("BUNDLE")
	MagicDisc     = magicFromString("DISC")
	MagicRun      = magicFromString("RUN")
	MagicIndex    = magicFromString("INDEX")
	MagicChecksum = magicFromString("CHECKSUM")
	MagicRefs     = magicFromString("REFS")
	MagicDiscs    = magicFromString("DISCS")
)

// Host-only magics. Their structures never reach a disc; the operations
// document holds their layouts.
var (
	MagicNASL = magicFromString("NASL")
	MagicNABN = magicFromString("NABN")
	MagicNABP = magicFromString("NABP")
	MagicNABS = magicFromString("NABS")
	MagicNALR = magicFromString("NALR")
	MagicNANT = magicFromString("NANT")
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
type HashAlgo uint32

const (
	// HashAlgoSHA256 is the only algorithm a Phase 1 writer emits.
	HashAlgoSHA256 HashAlgo = 0x12
	// HashAlgoBLAKE3 is reserved for a later version.
	HashAlgoBLAKE3 HashAlgo = 0x1e
	// HashAlgoSHA512 is reserved. Not used in version 1.
	HashAlgoSHA512 HashAlgo = 0x13
	// HashAlgoSHA512_256 is reserved for a later version.
	HashAlgoSHA512_256 HashAlgo = 0x1020
	// HashAlgoBLAKE2b256 is reserved.
	HashAlgoBLAKE2b256 HashAlgo = 0xb220
)

// Compression is the compression registry.
type Compression uint8

const (
	CompressionNone Compression = 0
	CompressionZstd Compression = 1
	CompressionLZ4  Compression = 2
)

// ChunkerProfile is the chunker profile registry.
type ChunkerProfile uint8

const (
	ChunkerProfileP3 ChunkerProfile = 1
	ChunkerProfileP4 ChunkerProfile = 2
	ChunkerProfileP5 ChunkerProfile = 3
)

// DiscFSProfile is the disc filesystem profile registry.
type DiscFSProfile uint8

// DiscFSProfileOneshot is the default and only Phase 1 profile.
const DiscFSProfileOneshot DiscFSProfile = 0

// MediaType is the media type registry. It is informational; a reader
// never rejects a value it does not know.
type MediaType uint8

const (
	MediaTypeBDRSL25GB  MediaType = 1
	MediaTypeBDRDL50GB  MediaType = 2
	MediaTypeBDRXL100GB MediaType = 3
	MediaTypeBDRXL128GB MediaType = 4
)

// FECScheme is the FEC scheme registry.
type FECScheme uint8

// FECSchemeNone is the default Phase 1 scheme: no checksum column and no
// parity. Burning two identical discs is the primary redundancy; FEC is
// a reserve feature a run opts into.
const FECSchemeNone FECScheme = 0

// FECSchemeRS255GF8 is the Reed-Solomon scheme, opt-in.
const FECSchemeRS255GF8 FECScheme = 1
