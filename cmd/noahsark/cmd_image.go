package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/tjjh89017/noahsark/internal/image"
)

// cmdImage implements "noahsark image build". OPERATIONS.md's
// "image build --run=SEQ --out=FILE" selects the run from repository
// state this build does not keep; instead it takes the packed tree
// directory directly, the one pack's --out already printed. See
// docs/decisions.md, "16. CLI reference".
func cmdImage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark image build --out=FILE [--capacity=N] TREE-DIR")
		return 2
	}
	if args[0] != "build" {
		_, _ = fmt.Fprintf(stderr, "noahsark: image %s is not available in Phase 1; only \"image build\" is\n", args[0])
		return 2
	}

	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "output image path")
	capacityStr := fs.String("capacity", "", "image length (sectors, or e.g. 25GB)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *out == "" {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark image build --out=FILE [--capacity=N] TREE-DIR")
		return 2
	}
	treeDir := fs.Arg(0)

	if *capacityStr == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: image build: --capacity is required")
		return 2
	}
	sectors, err := parseCapacity(*capacityStr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
		return 2
	}

	if _, err := image.CheckTools(); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
		return 1
	}

	if err := image.MakeImage(treeDir, *out, sectors); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "built image %s (%d sectors)\n", *out, sectors)
	return 0
}
