package image

import "fmt"

// SectorSize is the disc block size every structure and every file is
// aligned to.
const SectorSize = 2048

// filesystemFixedOverheadBytes covers the UDF volume and partition
// descriptors, the anchors and the space bitmap: fixed cost that does not
// grow with the file count.
//
// Reading: FORMAT.md's "8.1" gives no numeric overhead budget, only the
// mkudffs options and the anchor placement rule. The
// probe-udf-small-files action measured close-to-one-sector-per-file
// overhead for small files and a small, roughly constant base cost for
// the volume structures themselves. This constant takes the simplest
// deterministic reading of that evidence: a fixed 1 MiB base cost.
const filesystemFixedOverheadBytes = 1 << 20

// filesystemPerFileOverheadBytes is the per-file cost: one File Entry
// block. A UDF File Entry occupies a whole logical block, so this is one
// sector regardless of the file's own size.
//
// Reading: same evidence as filesystemFixedOverheadBytes. Recorded in
// docs/decisions.md under "8.1 Profiles a reader must know".
const filesystemPerFileOverheadBytes = SectorSize

// EstimateFilesystemOverhead returns the estimated UDF metadata cost, in
// bytes, of a volume holding fileCount files.
func EstimateFilesystemOverhead(fileCount int) uint64 {
	return filesystemFixedOverheadBytes + uint64(fileCount)*filesystemPerFileOverheadBytes
}

// CheckCapacity refuses a run whose total on-disc size, streamBytes plus
// checksumBytes plus parityBytes plus runHeaderCopyBytes, exceeds
// targetSectors once the filesystem overhead for fileCount files is
// added. targetSectors of zero is always refused.
func CheckCapacity(streamBytes, checksumBytes, parityBytes, runHeaderCopyBytes uint64, fileCount int, targetSectors uint64) error {
	if targetSectors == 0 {
		return fmt.Errorf("image: target capacity is required and must not be zero")
	}
	overhead := EstimateFilesystemOverhead(fileCount)
	total := streamBytes + checksumBytes + parityBytes + runHeaderCopyBytes + overhead
	limit := targetSectors * SectorSize
	if total > limit {
		return fmt.Errorf("image: run needs %d bytes (including %d bytes of estimated filesystem overhead), target capacity is %d bytes", total, overhead, limit)
	}
	return nil
}
