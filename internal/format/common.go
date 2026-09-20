package format

import "hash/crc32"

// crc32cTable is the Castagnoli table: polynomial 0x1EDC6F41, reflected,
// initial value 0xFFFFFFFF, final XOR 0xFFFFFFFF.
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// crc32c returns the CRC-32C checksum of data.
func crc32c(data []byte) uint32 {
	return crc32.Checksum(data, crc32cTable)
}

// objectHeaderCRCOffset is the byte offset of header_crc32c inside an
// object file: the common header, then the object header up to that field.
const objectHeaderCRCOffset = CommonHeaderLen + 24

// align8 rounds n up to the next multiple of 8.
func align8(n int) int {
	return (n + 7) &^ 7
}
