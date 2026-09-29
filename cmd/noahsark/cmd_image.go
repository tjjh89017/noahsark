package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// imageBuildUsage is the one usage line "image build" prints.
const imageBuildUsage = "usage: noahsark image build --out=FILE [--force] TREE-DIR"

func init() {
	register(&command{
		name:    "build",
		group:   "image",
		usage:   "image build --out=FILE [--force] TREE-DIR",
		summary: "Build a disc image from a packed tree directory. The image length comes from the tree's own DISC.bin.",
		flags:   imageBuildFlags,
	})
}

// imageBuildOptions holds the command options of image build.
type imageBuildOptions struct {
	out   string
	force bool
}

func imageBuildFlags(fs *flag.FlagSet) runFunc {
	o := &imageBuildOptions{}
	fs.StringVar(&o.out, "out", "", "output image path")
	fs.BoolVar(&o.force, "force", false, "overwrite --out if it already exists")
	return o.run
}

// run implements "noahsark image build". It takes the packed tree
// directory, the one that pack printed. See docs/decisions.md, "Image
// build".
//
// The image length is the capacity pack already wrote into the tree's
// own DISC.bin. An operator who had to repeat that capacity by hand
// could type a different one, and an image of the wrong length is not
// the disc pack planned.
func (o *imageBuildOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	if len(args) != 1 || o.out == "" {
		_, _ = fmt.Fprintln(stderr, imageBuildUsage)
		return 2
	}
	treeDir := args[0]

	if !o.force {
		if _, err := os.Stat(o.out); err == nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: image build: --out=%s already exists; pass --force to overwrite it\n", o.out)
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

	if err := image.MakeImage(treeDir, o.out, sectors, e.progress()); err != nil {
		if errors.Is(err, image.ErrPopulateNeedsRoot) {
			forceArg := ""
			if o.force {
				forceArg = "--force "
			}
			_, _ = fmt.Fprintf(stderr, "noahsark: image build: %s; run: sudo noahsark image build --out=%s %s%s\n", err, o.out, forceArg, treeDir)
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: image build:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "built image %s (%d bytes)\n", o.out, sectors*image.SectorSize)
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
