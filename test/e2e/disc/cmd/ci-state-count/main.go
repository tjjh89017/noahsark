// Command ci-state-count is CI-only tooling, not a NoahsArk command
// surface. It opens a repository's staging state log and prints the
// number of objects whose current state is Packed, so a shell scenario
// can compare it against a disc's own INDEX object count after
// rebuild-cache.
//
// Usage: ci-state-count STAGING-DIR
package main

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/stage"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-state-count STAGING-DIR")
		os.Exit(2)
	}
	stagingDir := os.Args[1]

	log, err := stage.Open(stagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-state-count:", err)
		os.Exit(1)
	}
	fmt.Println(log.CountState(stage.Packed))
}
