// Command ci-heal is CI-only tooling, not a NoahsArk command surface.
// It runs internal/restore's Heal over a disc tree and prints one line
// per stripe it repaired.
//
// Usage: ci-heal DISC-ROOT [OUT-DIR]
//
// With no OUT-DIR, Heal repairs DISC-ROOT in place; DISC-ROOT must be
// writable. With OUT-DIR, Heal copies the tree there first and repairs
// the copy, for a read-only mount.
package main

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/restore"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: ci-heal DISC-ROOT [OUT-DIR]")
		os.Exit(2)
	}
	root := os.Args[1]
	outDir := ""
	if len(os.Args) == 3 {
		outDir = os.Args[2]
	}

	reports, err := restore.Heal(root, outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci-heal:", err)
		os.Exit(1)
	}
	for _, r := range reports {
		fmt.Printf("stripe %d: repaired data columns %v, parity columns %v\n", r.Stripe, r.DataColumns, r.ParityColumns)
	}
	fmt.Printf("ci-heal: %d stripe(s) repaired\n", len(reports))
}
