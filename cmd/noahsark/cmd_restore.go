package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// restoreStdin is where the disc-swap restore mode reads the operator's
// Enter key from. Tests replace it with a pipe.
var restoreStdin io.Reader = os.Stdin

// cmdRestore implements "noahsark restore". Two modes share this
// entry point.
//
// With a disc root, --disc or --discs-dir, restore reads every disc at
// once, unchanged from before: see resolveDiscRoots.
//
// With neither, and exactly SNAPSHOT and OUT-DIR left over, restore
// resolves SNAPSHOT through the local cache and walks the disc-swap
// loop one disc at a time, by disc number, prompting the operator to
// insert the next one. This is the single-drive path; see
// docs/decisions.md.
func cmdRestore(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	fs := newFlagSet("noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR\n"+
		"       noahsark restore [--include=PATH]... [--overwrite] --mount=DIR [--dry-run] SNAPSHOT OUT-DIR",
		"Restore a snapshot to a directory. Accepts --disc (repeatable) or --discs-dir in place of DISC-ROOT for the all-discs-at-once mode.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	var discFlags stringList
	fs.Var(&discFlags, "disc", "a disc root to restore from; repeatable")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	var includeFlags stringList
	fs.Var(&includeFlags, "include", "restore only this snapshot-relative path and, if it names a directory, everything under it; repeatable")
	overwrite := fs.Bool("overwrite", false, "unlink an existing path first and then create it; without this, an existing path is left alone")
	mountFlag := fs.String("mount", "", "the directory where the drive is mounted; required for the disc-swap mode")
	dryRun := fs.Bool("dry-run", false, "print the disc list the restore needs and stop; reads no disc and writes nothing; needs --mount")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("restore", fs, stderr) {
		return 2
	}

	multi := len(discFlags) > 0 || *discsDir != ""
	if *mountFlag != "" && multi {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount cannot be combined with --disc or --discs-dir")
		return 2
	}
	if *dryRun && *mountFlag == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --dry-run needs --mount; the disc list comes from the local cache, and the all-discs-at-once modes read every disc together with no such list to preview")
		return 2
	}
	// Two positional arguments, with --mount given, are the disc-swap
	// mode's SNAPSHOT OUT-DIR. Without --mount, the same two arguments
	// are ambiguous: they are a DISC-ROOT SNAPSHOT with OUT-DIR left
	// off when the first one names an existing directory, and a
	// SNAPSHOT OUT-DIR with no disc given otherwise. Both mistakes get
	// their own line, naming what the operator typed.
	if !multi && fs.NArg() == 2 {
		if *mountFlag == "" {
			if looksLikeDiscRoot(fs.Arg(0)) {
				_, _ = fmt.Fprintf(stderr, "noahsark: restore: %s is a disc root, so OUT-DIR is missing\n", fs.Arg(0))
			} else {
				_, _ = fmt.Fprintf(stderr, "noahsark: restore: no disc given for snapshot %q; pass a DISC-ROOT, --disc, --discs-dir or --mount\n", fs.Arg(0))
			}
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] SNAPSHOT OUT-DIR")
			return 2
		}
		return cmdRestoreDiscSwap(*repoFlag, includeFlags, *overwrite, *mountFlag, *dryRun, fs.Arg(0), fs.Arg(1), stdout, stderr, prog)
	}
	// --mount is disc-swap mode, which never takes a DISC-ROOT: three
	// positional arguments with --mount given is a leftover DISC-ROOT
	// from the all-discs-at-once form, not that mode's own SNAPSHOT
	// OUT-DIR pair.
	if !multi && *mountFlag != "" && fs.NArg() == 3 {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount takes no DISC-ROOT")
		_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] SNAPSHOT OUT-DIR")
		return 2
	}

	var discRoots, positional []string
	switch {
	case multi:
		if fs.NArg() != 2 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] --disc=ROOT [--disc=ROOT]... SNAPSHOT OUT-DIR")
			return 2
		}
		positional = fs.Args()
	default:
		if fs.NArg() != 3 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR")
			return 2
		}
		if !looksLikeDiscRoot(fs.Arg(0)) {
			// The first argument of this form is always a mounted disc
			// or an unpacked NOAHSARK tree. Name it here, so a
			// mistyped path is not reported later as a missing object.
			_, _ = fmt.Fprintf(stderr, "noahsark: restore: no such disc root: %s\n", fs.Arg(0))
			return 2
		}
		positional = fs.Args()[1:]
	}
	var err error
	discRoots, err = resolveDiscRoots(discFlags, *discsDir, fs.Args())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	snapshotArg, outDir := positional[0], positional[1]

	src, err := restore.OpenSource(discRoots)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	snapID, err := src.ParseSnapshotArg(snapshotArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}

	opts := []restore.Option{
		restore.WithInclude(includeFlags),
		restore.WithOverwrite(*overwrite),
	}
	if known := knownDiscsForRepo(*repoFlag); len(known) > 0 {
		opts = append(opts, restore.WithKnownDiscs(known))
	}
	rep, err := restore.RestoreMultiWithProgress(discRoots, snapID, outDir, prog, opts...)
	printProblems(stderr, rep)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	return printRestoreResult(stdout, rep, snapID, outDir)
}

// printProblems writes one warning line for every problem the report
// holds, then one line for the problems it counted but dropped. A
// problem that lost data is not called a warning; every other kind is.
func printProblems(stderr io.Writer, rep restore.Report) {
	for _, p := range rep.Problems {
		prefix := "noahsark: restore: warning:"
		if p.Kind == restore.KindFile {
			prefix = "noahsark: restore:"
		}
		_, _ = fmt.Fprintf(stderr, "%s %s: %s\n", prefix, p.Path, p.Err)
	}
	if dropped := rep.Dropped(); dropped > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: restore: warning: %d more problem(s) not shown\n", dropped)
	}
}

// printRestoreResult writes the result lines every restore mode ends
// with, and returns the exit code: 1 when the restore lost data or
// metadata, 0 otherwise.
func printRestoreResult(stdout io.Writer, rep restore.Report, snapID object.ID, outDir string) int {
	_, _ = fmt.Fprintf(stdout, "restored snapshot %s into %s\n", snapID.TextForm(), outDir)
	if rep.Resumed > 0 {
		_, _ = fmt.Fprintf(stdout, "resumed: %d file(s) already restored\n", rep.Resumed)
	}
	if summary := rep.Summary(); summary != "" {
		_, _ = fmt.Fprintln(stdout, summary)
	}
	if rep.Failed() {
		return 1
	}
	return 0
}

// resolveDiscRoots builds the disc root list restore reads from: the
// single positional DISC-ROOT when neither --disc nor --discs-dir is
// given, else every --disc value followed by every immediate
// subdirectory of --discs-dir.
func resolveDiscRoots(discFlags stringList, discsDir string, positional []string) ([]string, error) {
	if len(discFlags) == 0 && discsDir == "" {
		if len(positional) == 0 {
			return nil, fmt.Errorf("no disc root given")
		}
		return []string{positional[0]}, nil
	}

	roots := append([]string(nil), discFlags...)
	if discsDir != "" {
		entries, err := os.ReadDir(discsDir)
		if err != nil {
			return nil, fmt.Errorf("--discs-dir %s: %w", discsDir, err)
		}
		for _, e := range entries {
			path := filepath.Join(discsDir, e.Name())
			isDir := e.IsDir()
			if !isDir && e.Type()&os.ModeSymlink != 0 {
				// A symlinked disc root (a mounted image linked in
				// under --discs-dir) reports as a symlink, not a
				// directory, from ReadDir's own lstat. Stat through
				// it so a symlinked mount is not skipped.
				if info, err := os.Stat(path); err == nil {
					isDir = info.IsDir()
				}
			}
			if isDir {
				roots = append(roots, path)
			}
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no disc root found: pass --disc or a --discs-dir with mounted subdirectories")
	}
	return roots, nil
}

// errStdinClosed is returned when the disc-swap loop's prompt cannot
// read another line, the same signal a killed session leaves behind:
// the caller reports it as a failed restore, with every part file left
// in place for a later run.
var errStdinClosed = fmt.Errorf("stdin closed while waiting for the next disc")

// cmdRestoreDiscSwap runs the single-drive restore: build the plan,
// print the disc list, then read one disc at a time, prompting the
// operator between discs.
// dryRun prints the disc list and stops there, before any disc is read.
// The mode writes only below OUT-DIR, so it takes no repository lock.
func cmdRestoreDiscSwap(repoFlag string, includes stringList, overwrite bool, mountDir string, dryRun bool, snapshotArg, outDir string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if mountDir == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount is required; OPERATIONS.md's configuration reference names no restore.mount key")
		return 2
	}

	src, c, err := openCacheSource(repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	snapID, err := src.ParseSnapshotArg(snapshotArg)
	if err != nil {
		// --mount names a disc to swap discs through, so a ref this
		// build's cache does not know is reported as possibly on a
		// disc not yet inserted, not as an unknown name outright,
		// unless it looks like a truncated snapshot id, which
		// *refNotFoundError already reports as that.
		if _, ok := err.(*refNotFoundError); ok && !restore.LooksLikeSnapshotIDPrefix(snapshotArg) {
			err = fmt.Errorf("ref %q is not on the provided disc(s); a later disc in the chain may carry it", snapshotArg)
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	return cmdRestoreDiscSwapRun(c, snapID, includes, overwrite, mountDir, dryRun, outDir, stdout, stderr, prog)
}

// cmdRestoreDiscSwapRun is cmdRestoreDiscSwap's body once snapID and
// includes are known.
func cmdRestoreDiscSwapRun(c *cache.Cache, snapID object.ID, includes []string, overwrite bool, mountDir string, dryRun bool, outDir string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if err := c.CheckComplete(snapID); err != nil {
		if ie, ok := err.(*cache.IncompleteError); ok {
			_, _ = fmt.Fprintln(stderr, formatIncompleteError("restore", ie))
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		return reportSourceError("restore", stderr, err, c, snapID)
	}

	// The plan holds only the chunks the destination does not hold yet,
	// so a rerun and a dry run both list the discs that are still
	// needed and nothing more.
	needed, err := restore.NeededChunks(c, snap, outDir, includes, overwrite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	result, err := plan.BuildForChunks(c, snap, snapID, includes, needed)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	discs := discsBySeq(result)
	printPlanText(stdout, discs, result)
	if len(result.Missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: restore: %d object(s) have no run known to the cache; run recover with more discs\n", result.MissingObjectCount())
		return 1
	}
	if dryRun {
		return 0
	}

	a, err := restore.NewAssembler(c, snap, outDir, includes, overwrite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	scanner := bufio.NewScanner(restoreStdin)
	done := make(map[[16]byte]bool, len(discs))
	for len(discs) > 0 {
		// The disc already in the drive is read first when the restore
		// still needs it, whatever its place in the list: the operator
		// is never asked to take out a disc and put it back later.
		next := 0
		if id, err := restore.ReadDiscIdentity(mountDir); err == nil {
			for i, d := range discs {
				if d.DiscUUID == id.UUID {
					next = i
					break
				}
			}
		}
		d := discs[next]
		discs = append(discs[:next], discs[next+1:]...)

		md := newMountedDisc(mountDir, d, func() error {
			return detectDisc(mountDir, c, d, scanner, stdout, stderr, done)
		})
		if err := a.Disc(md, prog); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 1
		}
		if md.read {
			// A disc whose chunks are all already in place is never
			// read, and so never becomes a disc the operator swapped.
			done[d.DiscUUID] = true
		}
	}

	a.Finish()
	rep := a.Report()
	printProblems(stderr, rep)
	return printRestoreResult(stdout, rep, snapID, outDir)
}

// mountedDisc is one plan disc, read through the operator's drive. It
// detects the disc at the first chunk the walk must read from it, so a
// disc the restore no longer needs is never asked for.
type mountedDisc struct {
	mountDir string
	ids      map[object.ID]bool
	detect   func() error
	read     bool
}

func newMountedDisc(mountDir string, d plan.DiscEntry, detect func() error) *mountedDisc {
	ids := make(map[object.ID]bool, len(d.Objects))
	for _, o := range d.Objects {
		ids[o.ID] = true
	}
	return &mountedDisc{mountDir: mountDir, ids: ids, detect: detect}
}

func (m *mountedDisc) Has(id object.ID) bool { return m.ids[id] }

// Read returns one verified chunk payload, after the operator has put
// this disc in the drive. A prompt that cannot be answered stops the
// whole restore; a chunk that does not read or does not verify fails
// its own file only.
func (m *mountedDisc) Read(id object.ID) ([]byte, error) {
	if !m.read {
		if err := m.detect(); err != nil {
			return nil, &restore.FatalDiscError{Err: err}
		}
		m.read = true
	}
	return restore.ReadChunkFromRoot(m.mountDir, id)
}

// discsBySeq returns the plan's discs by disc number, then by uuid. The
// operator looks a disc up by the number on its sleeve, so the printed
// list and the order the restore asks for the discs both follow that
// number.
func discsBySeq(r *plan.Result) []plan.DiscEntry {
	discs := append([]plan.DiscEntry(nil), r.Discs...)
	sort.Slice(discs, func(i, j int) bool {
		if discs[i].DiscSeq != discs[j].DiscSeq {
			return discs[i].DiscSeq < discs[j].DiscSeq
		}
		return plan.UUIDText(discs[i].DiscUUID) < plan.UUIDText(discs[j].DiscUUID)
	})
	return discs
}

// printPlanText prints one line per disc, in the order the restore asks
// for them, then the plan's totals.
func printPlanText(stdout io.Writer, discs []plan.DiscEntry, r *plan.Result) {
	for _, d := range discs {
		_, _ = fmt.Fprintf(stdout, "disc %d %q (%s): %d objects, %d bytes\n",
			d.DiscSeq, d.Label, plan.UUIDText(d.DiscUUID), len(d.Objects), d.Bytes)
	}
	for _, m := range r.Missing {
		if !m.HasDisc {
			_, _ = fmt.Fprintf(stdout, "missing: %d object(s), disc unknown\n", m.Objects)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "missing: %d object(s) on disc %s, no cached DISCS row names it\n",
			m.Objects, plan.UUIDText(m.DiscUUID))
	}
	_, _ = fmt.Fprintf(stdout, "totals: %d discs, %d objects, %d bytes\n",
		len(r.Discs), r.TotalObjects, r.TotalBytes)
}

// discSwapRetries is how many times detectDisc retries an unreadable
// DISC.bin before it prompts the operator, and the pause between tries.
const discSwapRetries = 3

var discSwapRetryPause = 300 * time.Millisecond

// detectDisc waits until the disc d names is in the drive at mountDir:
// no prompt when that disc is already there, a prompt otherwise. The
// operator swaps the disc by hand; noahsark never unmounts and never
// ejects.
//
// done holds the discs this restore has already read. The first look of
// each call passes over such a disc with no mismatch line: it is only
// the disc the last step finished, still in the drive, and not a wrong
// disc the operator inserted. A later look reports it the normal way.
func detectDisc(mountDir string, c *cache.Cache, d plan.DiscEntry, scanner *bufio.Scanner, stdout, stderr io.Writer, done map[[16]byte]bool) error {
	unreadable := 0
	promptedOnce := false
	for {
		found, err := restore.ReadDiscIdentity(mountDir)
		switch {
		case err != nil:
			unreadable++
			if unreadable <= discSwapRetries {
				time.Sleep(discSwapRetryPause)
				continue
			}
		case found.UUID == d.DiscUUID:
			_, _ = fmt.Fprintf(stdout, "%s: found\n", discNameShort(d.DiscSeq, d.Label))
			return nil
		default:
			if promptedOnce || !done[found.UUID] {
				_, _ = fmt.Fprintf(stderr, "expected %s, found %s\n",
					discName(d.DiscSeq, d.Label, d.DiscUUID), discNameFound(c, found))
			}
		}
		_, _ = fmt.Fprintf(stderr, "insert %s into %s and press Enter\n",
			discName(d.DiscSeq, d.Label, d.DiscUUID), mountDir)
		if !scanner.Scan() {
			return errStdinClosed
		}
		unreadable = 0
		promptedOnce = true
	}
}

// discNameFound names the disc that is in the drive. Its own DISC.bin
// carries the number and the label, so the name is complete even for a
// disc of another repository; the cache fills in a label DISC.bin left
// empty.
func discNameFound(c *cache.Cache, found restore.DiscIdentity) string {
	label := found.Label
	if label == "" {
		label = labelForUUID(c, found.UUID)
	}
	return discName(found.Seq, label, found.UUID)
}

// labelForUUID looks up a disc's label in the cache's DISCS table. It
// returns "" when the cache does not know uuid.
func labelForUUID(c *cache.Cache, uuid [16]byte) string {
	discs, err := c.Discs()
	if err != nil {
		return ""
	}
	for _, row := range discs.Rows {
		if row.DiscUUID == uuid {
			n := min(int(row.LabelLen), len(row.Label))
			return string(row.Label[:n])
		}
	}
	return ""
}
