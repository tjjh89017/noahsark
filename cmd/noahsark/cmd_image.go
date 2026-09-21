package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// imageBuildUsage is the one usage line "image build" prints.
const imageBuildUsage = "usage: noahsark image build --out=FILE [--force] TREE-DIR"

// cmdImage implements "noahsark image build". It takes the packed tree
// directory, the one that pack printed. See docs/decisions.md, "Image
// build".
//
// The image length is the capacity pack already wrote into the tree's
// own DISC.bin. An operator who had to repeat that capacity by hand
// could type a different one, and an image of the wrong length is not
// the disc pack planned.
func cmdImage(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, imageBuildUsage)
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, imageBuildUsage)
		return 0
	}
	if args[0] != "build" {
		_, _ = fmt.Fprintf(stderr, "noahsark: image %s: unknown subcommand; \"image build\" is the only one\n", args[0])
		return 2
	}
	fs := newFlagSet("noahsark image build --out=FILE [--force] TREE-DIR",
		"Build a disc image from a packed tree directory. The image length comes from the tree's own DISC.bin.", stderr)
	out := fs.String("out", "", "output image path")
	force := fs.Bool("force", false, "overwrite --out if it already exists")
	if err := fs.Parse(args[1:]); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("image build", fs, stderr) {
		return 2
	}
	if fs.NArg() != 1 || *out == "" {
		_, _ = fmt.Fprintln(stderr, imageBuildUsage)
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

	disc, err := readTreeDisc(treeDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: image build: %s: %v\n", treeDir, err)
		return 1
	}
	sectors := disc.CapacitySectors

	if err := image.MakeImage(treeDir, *out, sectors, prog); err != nil {
		if errors.Is(err, image.ErrPopulateNeedsRoot) {
			_, _ = fmt.Fprintf(stderr, "noahsark: image build: %s; run: sudo noahsark image build %s\n", err, strings.Join(args[1:], " "))
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "built image %s (%d bytes)\n", *out, sectors*image.SectorSize)
	return 0
}

// readTreeDisc reads DISC.bin from a packed tree directory. pack wrote
// it, so it carries the capacity, the label and the disc number of the
// disc the tree is for.
func readTreeDisc(treeDir string) (format.Disc, error) {
	names := image.NewNameCache()
	base, err := image.FindNoahsark(treeDir, names)
	if err != nil {
		return format.Disc{}, err
	}
	buf, err := os.ReadFile(filepath.Join(base, names.Resolve(base, "DISC.bin")))
	if err != nil {
		return format.Disc{}, err
	}
	var disc format.Disc
	if err := disc.Decode(buf); err != nil {
		return format.Disc{}, err
	}
	return disc, nil
}
