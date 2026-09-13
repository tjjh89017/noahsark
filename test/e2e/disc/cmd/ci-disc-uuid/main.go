// Command ci-disc-uuid is CI-only tooling, not a NoahsArk command
// surface. It reads one disc's DISC.bin with the Go reader and prints
// that disc's uuid, hyphenated the same way restore's missing-disc
// errors do, so a shell scenario can grep a restore error for the uuid
// of a disc it deliberately omitted.
//
// Usage: ci-disc-uuid DISC-ROOT
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/image"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-disc-uuid DISC-ROOT")
		os.Exit(2)
	}
	root := os.Args[1]

	rr, err := image.Read(root)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-disc-uuid: read:", err)
		os.Exit(1)
	}
	fmt.Println(uuidText(rr.Disc.DiscUUID))
}

// uuidText formats a 16-byte uuid as hyphenated lowercase text, matching
// internal/restore's own uuidText.
func uuidText(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
