// Command ci-index-count is CI-only tooling, not a NoahsArk command
// surface. It reads the INDEX of each disc with the Go reader and prints
// the number of distinct objects the discs list, so a shell scenario
// can compare a rebuilt state log's on-disc count against the discs'
// own count. Every disc carries every snapshot object, thus the discs
// list a snapshot more than once; the count holds it once.
//
// Usage: ci-index-count DISC-ROOT...
package main

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/image"
)

func main() {
	if len(os.Args) < 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-index-count DISC-ROOT...")
		os.Exit(2)
	}
	seen := make(map[[32]byte]bool)
	for _, root := range os.Args[1:] {
		rr, err := image.Read(root)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "ci-index-count: read:", err)
			os.Exit(1)
		}
		for _, o := range rr.Index.Objects {
			seen[o.ContentID] = true
		}
	}
	fmt.Println(len(seen))
}
