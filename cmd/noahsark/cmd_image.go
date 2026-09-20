package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// cmdImage implements "noahsark image build". OPERATIONS.md's
// "image build --run=SEQ --out=FILE" selects the run from repository
// state this build does not keep; instead it takes the packed tree
// directory directly, the one pack's --out already printed. See
// docs/decisions.md, "16. CLI reference".
func cmdImage(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark image build --out=FILE --capacity=N [--force] TREE-DIR")
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, "usage: noahsark image build --out=FILE --capacity=N [--force] TREE-DIR")
		return 0
	}
	if args[0] != "build" {
		_, _ = fmt.Fprintf(stderr, "noahsark: image %s is not available in Phase 1; only \"image build\" is\n", args[0])
		return 2
	}
	fs := newFlagSet("noahsark image build --out=FILE --capacity=N [--force] TREE-DIR",
		"Build a disc image from a packed tree directory.", stderr)
	out := fs.String("out", "", "output image path")
	capacityStr := fs.String("capacity", "", "image length (sectors, or e.g. 25GB)")
	force := fs.Bool("force", false, "overwrite --out if it already exists")
	if err := fs.Parse(args[1:]); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("image build", fs, stderr) {
		return 2
	}
	if fs.NArg() != 1 || *out == "" {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark image build --out=FILE --capacity=N [--force] TREE-DIR")
		return 2
	}
	treeDir := fs.Arg(0)

	if !*force {
		if _, err := os.Stat(*out); err == nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: image build: --out=%s already exists; pass --force to overwrite it\n", *out)
			return 2
		} else if !os.IsNotExist(err) {
			_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
			return 1
		}
	}

	if *capacityStr == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: image build: --capacity is required")
		return 2
	}
	sectors, err := parseCapacity(*capacityStr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
		return 2
	}

	if err := image.MakeImage(treeDir, *out, sectors, prog); err != nil {
		if errors.Is(err, image.ErrPopulateNeedsRoot) {
			_, _ = fmt.Fprintf(stderr, "noahsark: image build: %s; run: sudo noahsark image build %s\n", err, strings.Join(args[1:], " "))
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "built image %s (%d sectors)\n", *out, sectors)
	return 0
}
