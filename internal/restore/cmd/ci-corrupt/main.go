// Command ci-corrupt is CI-only tooling, not a NoahsArk command surface.
// It flips one byte in each named FEC stream data block, by mapping a
// (column, stripe) pair to the real file and byte offset the same way
// internal/restore's Heal does, so CI can prove Heal repairs a real UDF
// image, not just an unpacked tree.
//
// Usage: ci-corrupt DISC-ROOT COLUMN:STRIPE [COLUMN:STRIPE ...]
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/image"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ci-corrupt DISC-ROOT COLUMN:STRIPE [COLUMN:STRIPE ...]")
		os.Exit(2)
	}
	root := os.Args[1]

	base, err := image.FindNoahsark(root)
	must(err)
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	must(err)
	paths, sizes, _, err := image.StreamFiles(base, runDir)
	must(err)
	layout, err := fec.NewStreamLayout(sizes, fec.K)
	must(err)
	L := layout.StripeCount()

	for _, arg := range os.Args[2:] {
		parts := strings.SplitN(arg, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "ci-corrupt: bad COLUMN:STRIPE argument:", arg)
			os.Exit(2)
		}
		col, err1 := strconv.ParseUint(parts[0], 10, 64)
		stripe, err2 := strconv.ParseUint(parts[1], 10, 64)
		if err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "ci-corrupt: bad COLUMN:STRIPE argument:", arg)
			os.Exit(2)
		}
		block := col*L + stripe
		idx, off, err := layout.Locate(block)
		must(err)
		if off >= sizes[idx] {
			fmt.Fprintf(os.Stderr, "ci-corrupt: column %d stripe %d falls in padding of %s, nothing to corrupt\n", col, stripe, paths[idx])
			os.Exit(1)
		}
		must(flipByte(paths[idx], int64(off)))
		fmt.Printf("corrupted column %d stripe %d: %s at offset %d\n", col, stripe, paths[idx], off)
	}
}

func flipByte(path string, off int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
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
		fmt.Fprintln(os.Stderr, "ci-corrupt:", err)
		os.Exit(1)
	}
}
