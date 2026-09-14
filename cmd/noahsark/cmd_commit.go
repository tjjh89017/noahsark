package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// newWriter builds the Writer cmdCommit commits through. A test replaces
// it to reach the Writer's Stat seam before Commit runs.
var newWriter = object.NewWriter

// cmdCommit implements "noahsark commit". It reduces OPERATIONS.md's
// commit flags to the source path and --ref: the quick check, excludes,
// source-type override, mirror mode and commit bundles all need a config
// or state layer this build does not have. See docs/decisions.md,
// "16. CLI reference".
func cmdCommit(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
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
		_, _ = fmt.Fprintln(stderr, "usage: noahsark commit SOURCE [--repo=PATH] [--ref=NAME]")
		return 2
	}
	source := fs.Arg(0)

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}

	w := newWriter(cfg.StagingDir)
	w.RestatAfterRead = cfg.RestatAfterRead
	w.RetryUnstable = cfg.RetryUnstable
	w.Progress = prog
	snapID, sum, err := w.Commit(source)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	if err := updateRef(repoDir, *ref, snapID); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	if err := markStaged(cfg.StagingDir, snapID); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "snapshot %s\n", snapID.TextForm())
	_, _ = fmt.Fprintf(stdout, "ref %s -> %s\n", *ref, snapID.TextForm())
	_, _ = fmt.Fprintf(stdout, "new objects: %d, existing objects: %d\n", sum.NewObjects, sum.ExistingObjects)
	for _, u := range sum.Unstable {
		_, _ = fmt.Fprintf(stdout, "unstable %s branch=%s\n", u.Path, u.Branch)
	}
	for _, p := range sum.Skipped {
		_, _ = fmt.Fprintf(stdout, "skipped %s\n", p)
	}
	_, _ = fmt.Fprintf(stdout, "unstable: %d, skipped: %d\n", len(sum.Unstable), len(sum.Skipped))

	stagedObjects, stagedBytes, err := stagedTotals(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "staged: %d objects, %d bytes\n", stagedObjects, stagedBytes)

	if len(sum.Unstable) > 0 || len(sum.Skipped) > 0 {
		return 1
	}
	return 0
}

// stagedTotals reports the repository-wide STAGED total: how many
// objects still wait for a pack, and their combined byte size.
func stagedTotals(stagingDir string) (objects int, bytes uint64, err error) {
	l, err := stage.Open(stagingDir)
	if err != nil {
		return 0, 0, err
	}
	return image.StagedTotals(stagingDir, l)
}

// markStaged appends a Staged state.db record for every object snapID
// reaches that has no record yet: the whole staging state machine's
// entry point.
func markStaged(stagingDir string, snapID object.ID) error {
	l, err := stage.Open(stagingDir)
	if err != nil {
		return err
	}
	objs, err := image.CollectReachable(stagingDir, []object.ID{snapID})
	if err != nil {
		return err
	}
	for _, o := range objs {
		if err := l.EnsureStaged(o.ID); err != nil {
			return err
		}
	}
	return nil
}
