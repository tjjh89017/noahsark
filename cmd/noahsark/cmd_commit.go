package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/tjjh89017/noahsark/internal/object"
)

// cmdCommit implements "noahsark commit". It reduces OPERATIONS.md's
// commit flags to the source path and --ref: the quick check, excludes,
// source-type override, mirror mode and commit bundles all need a config
// or state layer this build does not have. See docs/decisions.md,
// "16. CLI reference".
func cmdCommit(args []string, stdout, stderr io.Writer) int {
	if refuseLaterPhaseFlags("commit", args, stderr) {
		return 2
	}

	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repoFlag := fs.String("repo", "", "repository root")
	ref := fs.String("ref", "LATEST", "ref to move")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: noahsark commit SOURCE [--repo=PATH] [--ref=NAME]")
		return 2
	}
	source := fs.Arg(0)

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}

	w := object.NewWriter(cfg.StagingDir)
	snapID, sum, err := w.Commit(source)
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	if err := updateRef(repoDir, *ref, snapID); err != nil {
		fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	fmt.Fprintf(stdout, "snapshot %s\n", snapID.TextForm())
	fmt.Fprintf(stdout, "ref %s -> %s\n", *ref, snapID.TextForm())
	fmt.Fprintf(stdout, "new objects: %d, existing objects: %d\n", sum.NewObjects, sum.ExistingObjects)
	return 0
}
