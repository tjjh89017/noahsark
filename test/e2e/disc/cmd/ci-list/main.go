// Command ci-list is CI-only tooling, not a NoahsArk command surface. It
// reads a disc tree with the Go reader, walks the newest REFS record's
// snapshot, and prints one path per line, so the CI action can diff that
// listing against reference/decoder.py's list output.
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ci-list <disc-root>")
		os.Exit(2)
	}
	root := os.Args[1]

	rr, err := image.Read(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci-list: read:", err)
		os.Exit(1)
	}
	if len(rr.Refs.Records) == 0 {
		fmt.Fprintln(os.Stderr, "ci-list: REFS has no records")
		os.Exit(1)
	}
	snapID := object.ID(rr.Refs.Records[0].SnapshotID)

	entries, err := image.ListSnapshot(root, snapID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci-list: list:", err)
		os.Exit(1)
	}
	fmt.Printf("# snapshot %s\n", hex.EncodeToString(snapID[:]))
	for _, e := range entries {
		fmt.Println(e.Path)
	}
}
