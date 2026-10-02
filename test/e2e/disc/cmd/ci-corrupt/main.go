// Command ci-corrupt is CI-only tooling, not a NoahsArk command surface.
// It flips one byte of a file at each given offset, so a CI scenario can
// write damage into a loop-mounted disc image and prove that verify finds
// it.
//
// Usage: ci-corrupt FILE OFFSET [OFFSET ...]
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) < 3 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-corrupt FILE OFFSET [OFFSET ...]")
		os.Exit(2)
	}
	path := os.Args[1]
	for _, arg := range os.Args[2:] {
		off, err := strconv.ParseInt(arg, 10, 64)
		if err != nil || off < 0 {
			_, _ = fmt.Fprintln(os.Stderr, "ci-corrupt: bad offset:", arg)
			os.Exit(2)
		}
		must(flipByte(path, off))
		fmt.Printf("corrupted %s at offset %d\n", path, off)
	}
}

// flipByte inverts every bit of the byte at off in the file at path.
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
	if _, err := f.WriteAt(b[:], off); err != nil {
		return err
	}
	return f.Close()
}

func must(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-corrupt:", err)
		os.Exit(1)
	}
}
