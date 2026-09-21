// Command ci-corrupt is CI-only tooling, not a NoahsArk command surface.
// It flips one byte in a named FEC block, so CI can prove Heal repairs a
// real UDF image, not just an unpacked tree.
//
// Each argument names one block:
//
//	COLUMN:STRIPE      a data stream column, mapped to its file and byte
//	                   offset the same way internal/restore's Heal does
//	p:COLUMN:STRIPE    a parity column (COLUMN counts from 0, the first
//	                   parity column), in runs/*/parity/p%04d.bin
//	c:STRIPE           the checksum record for one stripe, in
//	                   runs/*/checksum.bin
//	peek:COLUMN:STRIPE prints the data column's current block digest
//	                   without changing it, so a caller can prove a
//	                   block is unchanged across two peeks
//
// Usage: ci-corrupt DISC-ROOT BLOCK [BLOCK ...]
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

func main() {
	if len(os.Args) < 3 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-corrupt DISC-ROOT BLOCK [BLOCK ...]")
		os.Exit(2)
	}
	root := os.Args[1]

	cache := image.NewNameCache()
	base, err := image.FindNoahsark(root, cache)
	must(err)
	runDir, err := image.NewestRunDir(cache.Join(base, "runs"))
	must(err)
	paths, sizes, _, err := image.StreamFilesWithCache(base, runDir, cache)
	must(err)
	layout, err := fec.NewStreamLayout(sizes, fec.K)
	must(err)
	L := layout.StripeCount()

	for _, arg := range os.Args[2:] {
		switch {
		case strings.HasPrefix(arg, "p:"):
			must(corruptParity(runDir, arg))
		case strings.HasPrefix(arg, "c:"):
			must(corruptChecksum(runDir, arg))
		case strings.HasPrefix(arg, "peek:"):
			must(peekData(paths, sizes, layout, L, arg))
		default:
			must(corruptData(paths, sizes, layout, L, arg))
		}
	}
}

// peekData prints the SHA-256 digest of a data stream column's block,
// without modifying it, so a caller can compare two peeks of the same
// block and prove nothing wrote to it in between.
func peekData(paths []string, sizes []uint64, layout *fec.StreamLayout, L uint64, arg string) error {
	rest := strings.TrimPrefix(arg, "peek:")
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("bad peek:COLUMN:STRIPE argument: %s", arg)
	}
	col, err1 := strconv.ParseUint(parts[0], 10, 64)
	stripe, err2 := strconv.ParseUint(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("bad peek:COLUMN:STRIPE argument: %s", arg)
	}
	block := col*L + stripe
	idx, off, err := layout.Locate(block)
	if err != nil {
		return err
	}
	buf := make([]byte, fec.BlockSize)
	f, err := os.Open(paths[idx])
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := f.ReadAt(buf, int64(off))
	if err != nil && n == 0 {
		return err
	}
	sum := sha256.Sum256(buf[:n])
	fmt.Printf("peek column %d stripe %d: %s\n", col, stripe, hex.EncodeToString(sum[:]))
	return nil
}

func corruptData(paths []string, sizes []uint64, layout *fec.StreamLayout, L uint64, arg string) error {
	parts := strings.SplitN(arg, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("bad COLUMN:STRIPE argument: %s", arg)
	}
	col, err1 := strconv.ParseUint(parts[0], 10, 64)
	stripe, err2 := strconv.ParseUint(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("bad COLUMN:STRIPE argument: %s", arg)
	}
	block := col*L + stripe
	idx, off, err := layout.Locate(block)
	if err != nil {
		return err
	}
	if off >= sizes[idx] {
		return fmt.Errorf("column %d stripe %d falls in padding of %s, nothing to corrupt", col, stripe, paths[idx])
	}
	if err := flipByte(paths[idx], int64(off)); err != nil {
		return err
	}
	fmt.Printf("corrupted data column %d stripe %d: %s at offset %d\n", col, stripe, paths[idx], off)
	return nil
}

// corruptParity flips one byte in a parity column's stripe block. A
// parity file holds its column and nothing else, the same layout Heal
// reads.
func corruptParity(runDir, arg string) error {
	parts := strings.SplitN(arg, ":", 3)
	if len(parts) != 3 {
		return fmt.Errorf("bad p:COLUMN:STRIPE argument: %s", arg)
	}
	col, err1 := strconv.ParseUint(parts[1], 10, 64)
	stripe, err2 := strconv.ParseUint(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("bad p:COLUMN:STRIPE argument: %s", arg)
	}
	if col >= uint64(fec.M) {
		return fmt.Errorf("parity column %d is out of range, this run has %d parity columns", col, fec.M)
	}
	path := filepath.Join(runDir, "parity", fmt.Sprintf("p%04d.bin", uint64(fec.K)+1+col))
	off := int64(stripe) * fec.BlockSize
	if err := flipByte(path, off); err != nil {
		return err
	}
	fmt.Printf("corrupted parity column %d stripe %d: %s at offset %d\n", col, stripe, path, off)
	return nil
}

// corruptChecksum flips one byte in one stripe's checksum record.
func corruptChecksum(runDir, arg string) error {
	parts := strings.SplitN(arg, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("bad c:STRIPE argument: %s", arg)
	}
	stripe, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return fmt.Errorf("bad c:STRIPE argument: %s", arg)
	}
	path := filepath.Join(runDir, "checksum.bin")
	off := int64(stripe) * format.ChecksumRecordLen
	if err := flipByte(path, off); err != nil {
		return err
	}
	fmt.Printf("corrupted checksum record for stripe %d: %s at offset %d\n", stripe, path, off)
	return nil
}

func flipByte(path string, off int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var b [1]byte
	if _, err := f.ReadAt(b[:], off); err != nil {
		return err
	}
	b[0] ^= 0xFF
	_, err = f.WriteAt(b[:], off)
	return err
}

func must(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-corrupt:", err)
		os.Exit(1)
	}
}
