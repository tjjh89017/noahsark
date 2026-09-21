package main

import (
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
// commit flags to an optional source path and --ref: the quick check,
// excludes, source-type override, mirror mode and commit bundles all
// need a config or state layer this build does not have. See
// docs/decisions.md, "16. CLI reference".
func cmdCommit(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	fs := newFlagSet("noahsark commit [--repo=PATH] [--ref=NAME] [-m MESSAGE] [--exclude=PATTERN]... [--one-file-system] [SOURCE]",
		"Commit a source directory tree as a new snapshot.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	ref := fs.String("ref", "LATEST", "ref to move")
	message := fs.String("m", "", "commit message, stored on the snapshot")
	var excludeFlags stringList
	fs.Var(&excludeFlags, "exclude", "exclude pattern, gitignore-style; repeatable")
	oneFileSystem := fs.Bool("one-file-system", false, "do not cross a mount point; the mount point directory is recorded as empty")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("commit", fs, stderr) {
		return 2
	}
	if fs.NArg() > 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark commit [--repo=PATH] [--ref=NAME] [-m MESSAGE] [--exclude=PATTERN]... [--one-file-system] [SOURCE]")
		return 2
	}
	flagExcludes, err := parseExcludeFlags(excludeFlags)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}

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
	if refuseBadConfig("commit", cfg, stderr, configKeysForCommit...) {
		return 2
	}

	lk, code, ok := lockRepo("commit", repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	// With no SOURCE on the command line, fall back to the source root
	// init --source stored; a SOURCE given here overrides it.
	source := cfg.SourceRoot
	if fs.NArg() == 1 {
		source = fs.Arg(0)
	}
	if source == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit: no SOURCE given and no source root in the config; pass a path on the command line, or set sources.root in the config")
		return 2
	}

	ignoreExcludes, err := parseIgnoreFile(source)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}

	commitStageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}
	warnIfTruncated("commit", commitStageLog, stderr)

	w := newWriter(cfg.StagingDir)
	w.RestatAfterRead = cfg.RestatAfterRead
	w.RetryUnstable = cfg.RetryUnstable
	w.Progress = prog
	w.Message = *message
	w.OneFileSystem = *oneFileSystem
	allExcludes := append(append(append([]object.Pattern(nil), cfg.ExcludePatterns...), ignoreExcludes...), flagExcludes...)
	if len(allExcludes) > 0 {
		w.Exclude = object.NewMatcher(allExcludes)
	}
	w.Known = func(id object.ID) bool {
		rec, ok := commitStageLog.Get(id)
		return ok && (rec.State == stage.Staged || rec.State.OnDisc())
	}
	w.OnDisc = func(id object.ID) bool {
		rec, ok := commitStageLog.Get(id)
		return ok && rec.State.OnDisc()
	}
	snapID, sum, err := w.Commit(source)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	// Every new object is recorded STAGED before the ref moves to point
	// at this snapshot. A crash between the two steps must never leave
	// a ref that names a snapshot whose objects have no state log
	// record: pack only places an id it finds STAGED, so an object with
	// no record is never packed.
	if err := markStaged(cfg.StagingDir, snapID, sum.Reachable); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	if err := updateRef(repoDir, *ref, snapID); err != nil {
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
		_, _ = fmt.Fprintf(stdout, "skipped %s: %s\n", p.Path, p.Reason)
	}
	for _, p := range sum.MountPoints {
		_, _ = fmt.Fprintf(stdout, "mount point %s: not crossed, recorded as an empty directory\n", p)
	}
	printSpecialWarnings(stdout, sum.Special)
	_, _ = fmt.Fprintf(stdout, "unstable: %d, skipped: %d\n", len(sum.Unstable), len(sum.Skipped))
	if sum.Excluded > 0 {
		_, _ = fmt.Fprintf(stdout, "excluded: %d path(s)\n", sum.Excluded)
	}

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

// maxSpecialWarnings bounds how many special-file warning lines one
// commit prints, the same bound restore puts on its own warnings. A
// source tree with a large /dev copied into it must not bury the rest
// of the commit report.
const maxSpecialWarnings = 20

// printSpecialWarnings names each FIFO, socket and device node the
// commit recorded without content, up to maxSpecialWarnings lines, then
// one line with the total. The operator learns at commit time that
// these paths hold no data on the disc, while the source is still
// there to look at. It never changes the exit code: such a path is
// normal in many source trees, and the commit is complete without it.
func printSpecialWarnings(stdout io.Writer, special []object.SpecialPath) {
	if len(special) == 0 {
		return
	}
	shown := min(len(special), maxSpecialWarnings)
	for _, p := range special[:shown] {
		_, _ = fmt.Fprintf(stdout, "warning: %s: %s, no content is backed up\n", p.Path, p.Kind)
	}
	if rest := len(special) - shown; rest > 0 {
		_, _ = fmt.Fprintf(stdout, "warning: %d more special file(s) not shown\n", rest)
	}
	_, _ = fmt.Fprintf(stdout, "special files: %d; a FIFO, a socket and a device node carry no content, and restore does not create them\n", len(special))
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

// markStaged appends a Staged state.db record for the snapshot and every
// object reachable names that has no record yet: the whole staging
// state machine's entry point. reachable comes from the Writer's own
// Summary, not a walk of the staging directory: an object the Writer
// found already on a disc gets no staging file to walk into.
func markStaged(stagingDir string, snapID object.ID, reachable []object.ID) error {
	l, err := stage.Open(stagingDir)
	if err != nil {
		return err
	}
	if err := l.EnsureStaged(snapID); err != nil {
		return err
	}
	for _, id := range reachable {
		if err := l.EnsureStaged(id); err != nil {
			return err
		}
	}
	return nil
}
