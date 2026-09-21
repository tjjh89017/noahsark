// Command ci-disc-field is CI-only tooling, not a NoahsArk command
// surface. It reads one disc's DISC.bin, REFS.bin and DISCS.bin with
// the same Go reader verify uses, and prints the fields verify's
// operator-facing output no longer carries (the capacity fields and
// the refs and discs row counts), so a shell scenario can check them
// without asking normal operator output to carry internals it dropped.
//
// Usage: ci-disc-field DISC-ROOT
package main

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/image"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-disc-field DISC-ROOT")
		os.Exit(2)
	}
	root := os.Args[1]

	rr, err := image.Read(root)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-disc-field: read:", err)
		os.Exit(1)
	}
	fmt.Printf("capacity %d sectors\n", rr.Disc.CapacitySectors)
	fmt.Printf("refs: %d, discs: %d\n", len(rr.Refs.Records), len(rr.Discs.Rows))
}
