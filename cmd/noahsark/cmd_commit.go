package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	register(&command{
		name:    "commit",
		usage:   "commit [--ref=NAME] [-m MESSAGE] [--exclude=PATTERN]... [--one-file-system] [SOURCE]",
		summary: "Commit a source directory tree as a new snapshot.",
		flags:   commitFlags,
	})
}

// commitOptions holds the command options of commit.
type commitOptions struct {
	ref           string
	message       string
	excludes      stringList
	oneFileSystem bool
}

func commitFlags(fs *flag.FlagSet) runFunc {
	o := &commitOptions{}
	fs.StringVar(&o.ref, "ref", "", "ref to move; the default is the local date of today, YYYY-MM-DD")
	fs.StringVar(&o.message, "m", "", "commit message, stored on the snapshot")
	fs.Var(&o.excludes, "exclude", "exclude pattern, gitignore-style; repeatable")
	fs.BoolVar(&o.oneFileSystem, "one-file-system", false, "do not cross a mount point; the mount point directory is recorded as empty")
	return o.run
}

// newWriter builds the Writer commit commits through. A test replaces
// it to reach the Writer's Stat seam before Commit runs.
var newWriter = object.NewWriter

// run implements "noahsark commit". See docs/decisions.md, "Commit".
func (o *commitOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	if len(args) > 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark commit [--ref=NAME] [-m MESSAGE] [--exclude=PATTERN]... [--one-file-system] [SOURCE]")
		return 2
	}
	// With no --ref, commit moves the ref named by the local date of
	// today, as YYYY-MM-DD. A second commit on the same day moves the
	// same name to the newer snapshot; log still reaches the older one.
	ref := o.ref
	if ref == "" {
		ref = e.now().Format("2006-01-02")
	}
	flagExcludes, err := parseExcludeFlags(o.excludes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}

	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return configExitCode(err)
	}

	lk, code, ok := lockRepo("commit", repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	// With no SOURCE on the command line, fall back to the source root
	// init --source stored; a SOURCE given here overrides it.
	source := cfg.SourceRoot
	if len(args) == 1 {
		source = args[0]
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

	layout := layoutOf(repoDir, cfg)
	// The staging store exists before the walk, thus the walk can leave
	// it out when it is inside the source.
	err = ensureRepoDirs(layout)
	if err == nil {
		err = mkdirDurable(layout.chunksDir())
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}
	logs, err := openLogs("commit", layout, true, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}
	if refuseWhileMissing("commit", layout, cfg, logs.Discs, stderr) {
		return 1
	}
	commitStageLog := logs.Items
	c, err := catalog.Open(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	// Chunk objects go to staging. Snapshot, tree and blob objects go
	// directly into the catalog.
	w := newWriter(layout.chunkPath, c.MetaPath)
	w.Now = e.now
	w.Progress = e.progress()
	w.Message = o.message
	w.OneFileSystem = o.oneFileSystem
	allExcludes := append(append([]object.Pattern(nil), ignoreExcludes...), flagExcludes...)
	if len(allExcludes) > 0 {
		w.Exclude = object.NewMatcher(allExcludes)
	}
	// commit treats a Lost item as not known: it writes the chunk again,
	// and records the item as Staged. A Staged item gets its chunk file
	// again when the file is missing or has the wrong size.
	w.Known = func(id object.ID) bool {
		rec, ok := commitStageLog.Get(id)
		return ok && rec.State != stage.Lost
	}
	w.OnDisc = func(id object.ID) bool {
		rec, ok := commitStageLog.Get(id)
		return ok && (rec.State == stage.Packed || rec.State == stage.OnDisc)
	}
	w.HasRecord = func(id object.ID) bool {
		_, ok := commitStageLog.Get(id)
		return ok
	}
	w.OwnDirs = ownDirs(layout)
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
	if err := markStaged(commitStageLog, snapID, sum.Reachable); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}
	if err := c.MarkComplete(snapID); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	if err := updateRef(layout.refsFile(), ref, snapID); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "snapshot %s\n", snapID.TextForm())
	_, _ = fmt.Fprintf(stdout, "ref %s -> %s\n", ref, snapID.TextForm())
	_, _ = fmt.Fprintf(stdout, "new items: %d, existing items: %d\n", sum.NewObjects, sum.ExistingObjects)
	_, _ = fmt.Fprintf(stdout, "unstable: %d, skipped: %d\n", len(sum.Unstable), len(sum.Skipped))
	for _, u := range sum.Unstable {
		_, _ = fmt.Fprintf(stdout, "unstable %s branch=%s\n", u.Path, u.Branch)
	}
	for _, p := range sum.Skipped {
		_, _ = fmt.Fprintf(stdout, "skipped %s: %s\n", p.Path, p.Reason)
	}
	for _, p := range sum.MountPoints {
		_, _ = fmt.Fprintf(stdout, "mount point %s: not crossed, recorded as an empty directory\n", p)
	}
	for _, d := range sum.OwnDirs {
		_, _ = fmt.Fprintf(stdout, "excluded %s: %s\n", d.Path, d.What)
	}
	printSpecialWarnings(stdout, sum.Special)
	if sum.Excluded > 0 {
		_, _ = fmt.Fprintf(stdout, "excluded: %d path(s)\n", sum.Excluded)
	}

	stagedItems, stagedBytes, err := image.StagedTotals(layout.objectPath(c), commitStageLog)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: commit:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "staged: %d items, %d bytes\n", stagedItems, stagedBytes)
	// The snapshot is committed also when a file was skipped or unstable,
	// so the next line comes before the exit code is chosen.
	_, _ = fmt.Fprintln(stdout, nextStatusLine)

	if len(sum.Unstable) > 0 || len(sum.Skipped) > 0 {
		return 1
	}
	return 0
}

// ownDirs names the directories of the repository that commit never
// walks: the repository, the staging store, and the pack --out directory
// that each plan symlink names. A plan tree that is not a symlink is
// inside the staging store.
func ownDirs(l repoLayout) []object.OwnDir {
	dirs := []object.OwnDir{
		{Path: l.repo, What: "the repository"},
		{Path: l.stagingDir(), What: "the staging store"},
	}
	trees, _ := filepath.Glob(filepath.Join(l.plansDir(), "*", planTreeName))
	for _, tree := range trees {
		if fi, err := os.Lstat(tree); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			dirs = append(dirs, object.OwnDir{Path: tree, What: "the disc root of a pack --out"})
		}
	}
	return dirs
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

// markStaged records the snapshot and every item that reachable names
// as Staged, when the state log l does not know the item or knows it as
// Lost, in one batch with one sync. reachable comes from the Writer's
// own Summary, not a walk of the files: a chunk the Writer found already
// on a disc gets no chunk file to walk into.
func markStaged(l *stage.Log, snapID object.ID, reachable []object.ID) error {
	return l.EnsureStaged(append([]object.ID{snapID}, reachable...)...)
}
