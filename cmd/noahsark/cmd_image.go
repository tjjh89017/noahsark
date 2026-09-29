package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	// makeImage builds and populates the image file of the plan.
	makeImage func(plan *image.Plan, sectors uint64, prog *progress.Reporter) error
}{
	mkudffsVersion: image.CheckTools,
	makeImage:      image.BuildImage,
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
//
// Root runs it on a repository of a user with no privilege, and that
// user can change the repository while it runs. Thus it reads each file
// of the repository through a descriptor, never through a symlink, and
// only a regular file of the owner of the repository.
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
	repo, err := image.OpenDir(repoDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	defer func() { _ = repo.Close() }()
	cfg, err := readRepoConfig(repo)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return configExitCode(err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	layout := layoutOf(repoDir, cfg)
	ledger, discs, err := readRepoState(repo, layout, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	warnDiscLogTornTail(cmd, discs, stderr)
	discUUID, err := resolveDisc(ledger.Rows, discs, args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	disc := discTargetOf(ledger.Rows, discs, discUUID)
	treeDir := layout.planTree(discUUID)

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

	plan, err := image.OpenPlan(layout.planDir(discUUID), planTreeName, planImageName)
	if errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: no disc root at %s\n", cmd, treeDir)
		return 1
	} else if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	defer func() { _ = plan.Close() }()
	if uid, _ := plan.Owner(); uid != repo.UID {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s belongs to uid %d, not to uid %d, the owner of the repository %s; image build refuses it\n",
			cmd, layout.planDir(discUUID), uid, repo.UID, repoDir)
		return 1
	}

	if exists, err := plan.ImageExists(); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	} else if exists {
		if !o.force {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s exists; add --force to build it again\n", cmd, plan.ImagePath)
			return 1
		}
		if err := plan.RemoveImage(); err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
			return 1
		}
	}

	discBin, err := plan.ReadDisc()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	sectors := discBin.CapacitySectors
	if err := imageHost.makeImage(plan, sectors, e.progress()); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "built image %s (%d bytes)\n", plan.ImagePath, sectors*image.SectorSize)
	return 0
}

// readRepoConfig reads config.yaml through the descriptor of the
// repository directory repo, as readConfig reads it by path.
func readRepoConfig(repo *image.Dir) (repoConfig, error) {
	path := configPath(repo.Path)
	data, err := repo.ReadFile(configFileName)
	if err != nil {
		return repoConfig{}, err
	}
	f, err := decodeConfig(data)
	if err != nil {
		return repoConfig{}, &configError{path: path, err: err}
	}
	stagingDir := f.Staging.Dir
	if !filepath.IsAbs(stagingDir) {
		stagingDir = filepath.Join(repo.Path, stagingDir)
	}
	return repoConfig{
		RepoUUID:   f.Repo.UUID,
		StagingDir: stagingDir,
		SourceRoot: f.Sources.Root,
		PackDevice: f.Pack.Device,
	}, nil
}

// readRepoState reads the disc ledger and the disc state log through
// the descriptor of the repository directory repo. A missing state
// directory or file is empty, as for the readers by path.
func readRepoState(repo *image.Dir, layout repoLayout, repoUUID [16]byte) (format.DiscsTable, *stage.DiscLog, error) {
	ledger := format.DiscsTable{RepoUUID: repoUUID}
	var logData []byte
	state, err := repo.Subdir(stateDirName)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return ledger, nil, err
	default:
		defer func() { _ = state.Close() }()
		data, err := state.ReadFile(discsLedgerName)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return ledger, nil, fmt.Errorf("disc ledger: %w", err)
		default:
			if _, err := ledger.Decode(data); err != nil {
				return ledger, nil, fmt.Errorf("disc ledger %s: %w", layout.discsLedgerFile(), err)
			}
		}
		logData, err = state.ReadFile(discLogFileName)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ledger, nil, err
		}
	}
	discs, err := openDiscLogData(logData, layout.discLogFile())
	return ledger, discs, err
}

// openDiscLogData replays the disc state log data, which image build
// read from path. The stage reader takes a directory, thus the data goes
// to a new directory that only this process can change, and never
// through the state directory of the repository again. An error names
// path, not that directory.
func openDiscLogData(data []byte, path string) (*stage.DiscLog, error) {
	dir, err := image.PrivateTempDir("noahsark-state-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	copyPath := filepath.Join(dir, discLogFileName)
	if data != nil {
		if err := os.WriteFile(copyPath, data, 0o600); err != nil {
			return nil, err
		}
	}
	discs, err := stage.OpenDiscLogReadOnly(dir)
	if err != nil {
		return nil, errors.New(strings.ReplaceAll(err.Error(), copyPath, path))
	}
	return discs, nil
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
