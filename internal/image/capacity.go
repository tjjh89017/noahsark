package image

import "fmt"

// SectorSize is the disc block size every structure and every file is
// aligned to.
const SectorSize = 2048

// objectFanoutDirs is the number of objects/<ab> fanout directories the
// tree can hold: one per possible two-hex-digit prefix (see
// object.ID.FanoutByte). The tree never has more directories than this
// plus a small fixed number of top-level directories, whatever the file
// count, so it bounds the directory count instead of tracking it exactly.
const objectFanoutDirs = 256

// fixedTreeDirs is the top-level directories every run tree has
// regardless of file count: NOAHSARK, NOAHSARK/objects,
// NOAHSARK/snapshots and NOAHSARK/runs/<seq>.
const fixedTreeDirs = 4

// filesystemMarginBytes is a fixed slack added on top of the itemised
// terms, for allocation descriptors and other small UDF structures this
// estimate does not name one by one.
const filesystemMarginBytes = 4 << 20

// filesystemPerFileOverheadBytes is the per-file cost: one File Entry
// block plus a share of the FID (File Identifier Descriptor) space its
// parent directory spends on it.
const filesystemPerFileOverheadBytes = 2 * SectorSize

// filesystemPerDirOverheadBytes is the per-directory cost: one File
// Entry block for the directory itself plus a share of the FID space
// its own parent spends on it.
const filesystemPerDirOverheadBytes = 2 * SectorSize

// filesystemMarginNumerator and filesystemMarginDenominator express a
// proportional margin of 0.1% of capacity, on top of the fixed and
// per-item terms, for costs that scale with the volume itself rather
// than with the file count.
const (
	filesystemMarginNumerator   = 1
	filesystemMarginDenominator = 1000
)

// spaceBitmapOverheadBytes returns the size, in bytes rounded up to a
// whole block, of the UDF space bitmap: one bit per sector of the
// partition.
func spaceBitmapOverheadBytes(capacitySectors uint64) uint64 {
	bitmapBytes := (capacitySectors + 7) / 8
	blocks := (bitmapBytes + SectorSize - 1) / SectorSize
	return blocks * SectorSize
}

// estimateDirCount bounds a run tree's directory count from its file
// count, without tracking the real count: at most one directory per
// object fanout prefix, plus the tree's own fixed top-level
// directories.
func estimateDirCount(fileCount int) int {
	return min(fileCount, objectFanoutDirs) + fixedTreeDirs
}

// EstimateFilesystemOverhead returns the estimated UDF metadata cost, in
// bytes, of a volume of capacitySectors sectors holding fileCount files.
//
// The estimate has four terms: a fixed base (the partition start and
// end reservations, the space bitmap, and a fixed margin for
// allocation descriptors and other small structures), a per-file cost
// (one File Entry block plus FID space), a per-directory cost (the
// same, for the run tree's own directories, bounded by fileCount and
// the fanout layout), and a proportional margin scaled to capacity for
// costs the itemised terms miss.
//
// Reading: measured on a real mkudffs UDF 2.01 image, loop-mounted and
// populated; see docs/decisions.md under "Profiles a reader must know:
// filesystem overhead estimate".
func EstimateFilesystemOverhead(fileCount int, capacitySectors uint64) uint64 {
	base := spaceBitmapOverheadBytes(capacitySectors) + filesystemMarginBytes
	perFile := uint64(fileCount) * filesystemPerFileOverheadBytes
	perDir := uint64(estimateDirCount(fileCount)) * filesystemPerDirOverheadBytes
	margin := capacitySectors * SectorSize * filesystemMarginNumerator / filesystemMarginDenominator
	return base + perFile + perDir + margin
}

// DataBudgetBlocks returns the number of blockSize stream blocks a run
// may fill, given a target capacity of targetSectors and a UDF tree of
// fileCount files. It reserves the estimated filesystem overhead for
// fileCount files, plus the two RUN.bin and RUN2.bin header copies,
// then gives the rest to whole FEC stripes: k data blocks out of every
// stripeWidth sectors, the checksum and parity sectors of a stripe
// taking the remainder. A partial stripe's sectors go unused, and a
// target too small for the reserved sectors alone yields a zero
// budget.
func DataBudgetBlocks(targetSectors uint64, fileCount int, k, stripeWidth int) uint64 {
	reservedBytes := EstimateFilesystemOverhead(fileCount, targetSectors) + 2*RunFileLen
	reservedSectors := (reservedBytes + SectorSize - 1) / SectorSize
	if reservedSectors >= targetSectors {
		return 0
	}
	usableSectors := targetSectors - reservedSectors
	stripes := usableSectors / uint64(stripeWidth)
	return stripes * uint64(k)
}

// CheckCapacity refuses a run whose total on-disc size, streamBytes plus
// checksumBytes plus parityBytes plus runHeaderCopyBytes, exceeds
// targetSectors once the filesystem overhead for fileCount files is
// added. targetSectors of zero is always refused.
func CheckCapacity(streamBytes, checksumBytes, parityBytes, runHeaderCopyBytes uint64, fileCount int, targetSectors uint64) error {
	if targetSectors == 0 {
		return fmt.Errorf("image: target capacity is required and must not be zero")
	}
	overhead := EstimateFilesystemOverhead(fileCount, targetSectors)
	total := streamBytes + checksumBytes + parityBytes + runHeaderCopyBytes + overhead
	limit := targetSectors * SectorSize
	if total > limit {
		return fmt.Errorf("image: run needs %d bytes (including %d bytes of estimated filesystem overhead), target capacity is %d bytes", total, overhead, limit)
	}
	return nil
}
