// Command ci-index-count is CI-only tooling, not a NoahsArk command
// surface. It reads one disc's INDEX with the Go reader and prints the
// number of objects it lists, so a shell scenario can compare a
// rebuilt state log's packed count against the disc's own count.
//
// Usage: ci-index-count DISC-ROOT
package main

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/image"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-index-count DISC-ROOT")
		os.Exit(2)
	}
	root := os.Args[1]

	rr, err := image.Read(root)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-index-count: read:", err)
		os.Exit(1)
	}
	fmt.Println(len(rr.Index.Objects))
}
