package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

func labelText(b []byte) string {
	return string(b)
}

// discIdentity is the uuid, the number and the label of a disc from its
// DISC.bin. It names the disc of a failed check too.
type discIdentity struct {
	DiscUUID [16]byte
	DiscSeq  uint64
	Label    string
}

// readDiscIdentity reads DISC.bin under root, without the object checks.
func readDiscIdentity(root string) (discIdentity, error) {
	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		return discIdentity{}, err
	}
	discBuf, err := os.ReadFile(filepath.Join(base, names.Resolve(base, "DISC.bin")))
	if err != nil {
		return discIdentity{}, fmt.Errorf("DISC.bin: %w", err)
	}
	var disc format.Disc
	if err := disc.Decode(discBuf); err != nil {
		return discIdentity{}, fmt.Errorf("DISC.bin: %w", err)
	}
	ident := discIdentity{
		DiscUUID: disc.DiscUUID,
		DiscSeq:  disc.DiscSeq,
		Label:    labelText(disc.Label[:min(int(disc.LabelLen), len(disc.Label))]),
	}
	return ident, nil
}

// printNotices prints each notice of the read rr to stderr. A nil rr
// prints nothing.
func printNotices(stderr io.Writer, cmd string, rr *image.ReadResult) {
	if rr == nil {
		return
	}
	for _, n := range rr.Notices {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, n)
	}
}
