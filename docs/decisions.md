# Implementation decisions

Each entry records a reading of FORMAT.md chosen by the implementation. Each
entry is named by the FORMAT.md heading it reads.

## 11.1 INDEX

`container_len`, `body_crc32c` and `header_crc32c` are all derivable from
bytes the same call already lays out: `Index.Encode` computes and writes all
three, overwriting any value the caller set. `Index.Decode` verifies both
CRCs and refuses a mismatch. This does not change the bytes a correct writer
puts on disc; it only fixes which Go call computes them.

## 11.2 REFS and 11.3 DISCS

`RefsTable.Encode` and `DiscsTable.Encode` compute and write `body_crc32c`
and `header_crc32c` the same way, for the same reason. `Decode` for both
verifies both CRCs and refuses a mismatch.

## 10.3 Checksum column

`ChecksumRecord.Encode` computes and writes `header_crc32c` over bytes 0 to
15. `Decode` verifies it and refuses a mismatch.
