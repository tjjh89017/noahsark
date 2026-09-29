// Command ci-state-count is CI-only tooling, not a NoahsArk command
// surface. It opens a repository's state log and prints the
// number of objects the log places on some disc, so a shell scenario
// can compare it against a disc's own INDEX object count after
// recover.
//
// Usage: ci-state-count STATE-DIR
//
// STATE-DIR is the state directory of the repository, REPO/state.
package main

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/stage"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-state-count STATE-DIR")
		os.Exit(2)
	}
	stateDir := os.Args[1]

	log, err := stage.Open(stateDir)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-state-count:", err)
		os.Exit(1)
	}
	fmt.Println(log.CountOnDisc())
}
