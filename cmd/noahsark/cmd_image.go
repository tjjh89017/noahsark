package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// imageBuildUsage is the one usage line "image build" prints.
const imageBuildUsage = "usage: noahsark image build [--force] DISC"

func init() {
	register(&command{
		name:    "build",
		group:   "image",
		usage:   "image build [--force] DISC",
		summary: "Build the UDF image of a disc from its disc root, as root. The image length comes from the DISC.bin of the disc root.",
		flags:   imageBuildFlags,
	})
}

// imageHost holds the host programs that image build runs. A test
// replaces them, so that it needs no mkudffs and no loop mount.
var imageHost = struct {
	// mkudffsVersion returns the udftools version, or an error when
	// mkudffs is missing or too old.
	mkudffsVersion func() (string, error)
	// makeImage builds and populates the image file.
	makeImage func(treeDir, imagePath string, sectors uint64, prog *progress.Reporter) error
}{
	mkudffsVersion: image.CheckTools,
	makeImage:      image.MakeImage,
}

// imageBuildOptions holds the command options of image build.
type imageBuildOptions struct {
	force bool
}

func imageBuildFlags(fs *flag.FlagSet) runFunc {
	o := &imageBuildOptions{}
	fs.BoolVar(&o.force, "force", false, "remove an existing image and build it again")
	return o.run
}

// run implements "noahsark image build [--force] DISC". It reads the
// repository and writes only staging/plans/UUID/tree.img. It takes no
// lock, because it runs as root and a lock file that root creates would
// block the operator. docs/states.md, rows 15 to 19, gives the messages.
func (o *imageBuildOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "image build"
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, imageBuildUsage)
		return 2
	}

	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	layout := layoutOf(repoDir, cfg)
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	discs, err := stage.OpenDiscLogReadOnly(layout.stateDir())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	if n := discs.TornBytes(); n > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: the disc state log's tail was truncated; %d byte(s) after the last valid record were ignored, matching a crash during an earlier append\n", cmd, n)
	}
	discUUID, err := resolveDisc(ledger.Rows, discs, args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	disc := discTargetOf(ledger.Rows, discs, discUUID)
	treeDir := layout.planTree(discUUID)
	imagePath := layout.planImage(discUUID)

	switch disc.info.State {
	case stage.DiscPacked, stage.DiscBurned, stage.DiscVerified:
	case stage.DiscOnDiscOnly, stage.DiscLost, stage.DiscMissing:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: no disc root at %s\n", cmd, treeDir)
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, discStateRefusal(disc))
		return 1
	}

	if _, err := imageHost.mkudffsVersion(); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}

	if e.euid() != 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: image build needs root for the loop mount; run: %s\n", o.sudoLine(repoDir, ledger.Rows, discs, disc))
		return 1
	}

	if _, err := os.Stat(treeDir); errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: no disc root at %s\n", cmd, treeDir)
		return 1
	} else if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}

	if _, err := os.Lstat(imagePath); err == nil {
		if !o.force {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s exists; add --force to build it again\n", cmd, imagePath)
			return 1
		}
		if err := os.Remove(imagePath); err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
			return 1
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}

	discBin, err := readTreeDisc(treeDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s: %v\n", cmd, treeDir, err)
		return 1
	}
	sectors := discBin.CapacitySectors
	if err := imageHost.makeImage(treeDir, imagePath, sectors, e.progress()); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "built image %s (%d bytes)\n", imagePath, sectors*image.SectorSize)
	return 0
}

// sudoLine is the command line that runs this image build as root. It
// names the disc by its number, or by its full uuid when another disc
// has the same number.
func (o *imageBuildOptions) sudoLine(repoDir string, rows []format.DiscsRow, discs *stage.DiscLog, disc discTarget) string {
	arg := strconv.FormatUint(disc.seq, 10)
	same := 0
	for _, c := range uniqueDiscCandidates(rows, func(u [16]byte) bool {
		d, ok := discs.Disc(u)
		return ok && d.State == stage.DiscUndone
	}) {
		if c.Seq == disc.seq {
			same++
		}
	}
	if same > 1 {
		arg = uuidText(disc.info.UUID)
	}
	force := ""
	if o.force {
		force = "--force "
	}
	return "sudo noahsark --repo=" + quoteShellWord(repoDir) + " image build " + force + arg
}

// readTreeDisc reads DISC.bin from a disc root. pack wrote it, so it
// carries the capacity, the label and the disc number of the disc.
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
