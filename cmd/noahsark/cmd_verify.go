package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// cmdVerify implements "noahsark verify --image=PATH". A drive mount
// needs root, which this build never assumes, so --image here names a
// mounted disc path or an unpacked NOAHSARK tree, the same root
// image.Read and restore.Heal already accept, instead of OPERATIONS.md's
// raw image file plus --mapfile. See docs/decisions.md,
// "16. CLI reference".
func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	imagePath := fs.String("image", "", "mounted disc path or unpacked NOAHSARK tree")
	heal := fs.Bool("heal", false, "repair the disc with Reed-Solomon parity before reporting")
	healOut := fs.String("out", "", "heal into this directory instead of in place")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *imagePath == "" {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify --image=PATH [--heal] [--out=DIR]")
		return 2
	}

	target := *imagePath
	if *heal {
		reports, err := restore.Heal(*imagePath, *healOut)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify: heal:", err)
			return 1
		}
		for _, r := range reports {
			_, _ = fmt.Fprintf(stdout, "stripe %d: repaired data columns %v, parity columns %v\n", r.Stripe, r.DataColumns, r.ParityColumns)
		}
		_, _ = fmt.Fprintf(stdout, "heal: %d stripe(s) repaired\n", len(reports))
		if *healOut != "" {
			target = *healOut
		}
	}

	rr, err := image.Read(target)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "disc label: %q\n", labelText(rr.Disc.Label[:rr.Disc.LabelLen]))
	_, _ = fmt.Fprintf(stdout, "disc capacity: %d sectors, forced %d sectors, capacity_is_forced=%d\n",
		rr.Disc.CapacitySectors, rr.Disc.CapacityForcedSectors, rr.Disc.CapacityIsForced)
	_, _ = fmt.Fprintf(stdout, "run: %d objects verified, %d run header copies\n", rr.ObjectsVerified, rr.RunCopies)
	_, _ = fmt.Fprintf(stdout, "refs: %d, discs: %d\n", len(rr.Refs.Records), len(rr.Discs.Rows))
	_, _ = fmt.Fprintln(stdout, "verify: ok")
	return 0
}

func labelText(b []byte) string {
	return string(b)
}
