package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
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
// loop of OPERATIONS.md's "14.2 Disc-major order": one disc at a time,
// in plan order, prompting the operator to insert the next one. This is
// the single-drive path; see docs/decisions.md.
func cmdRestore(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	fs := newFlagSet("noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR\n"+
		"       noahsark restore [--include=PATH]... [--overwrite] --mount=DIR [--no-eject] [--dry-run] SNAPSHOT OUT-DIR",
		"Restore a snapshot to a directory. Accepts --disc (repeatable) or --discs-dir in place of DISC-ROOT for the all-discs-at-once mode.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	var discFlags stringList
	fs.Var(&discFlags, "disc", "a disc root to restore from; repeatable")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	var includeFlags stringList
	fs.Var(&includeFlags, "include", "restore only this snapshot-relative path and, if it names a directory, everything under it; repeatable")
	overwrite := fs.Bool("overwrite", false, "unlink an existing path first and then create it; without this, an existing path is left alone")
	mountFlag := fs.String("mount", "", "the directory where the drive is mounted; required for the disc-swap mode")
	noEject := fs.Bool("no-eject", false, "do not eject after each disc")
	dryRun := fs.Bool("dry-run", false, "print the disc list the restore needs and stop; writes nothing, takes no lock; needs --mount")
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
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] [--no-eject] SNAPSHOT OUT-DIR")
			return 2
		}
		return cmdRestoreDiscSwap(*repoFlag, includeFlags, *overwrite, *mountFlag, *noEject, *dryRun, fs.Arg(0), fs.Arg(1), stdout, stderr, prog)
	}
	// --mount is disc-swap mode, which never takes a DISC-ROOT: three
	// positional arguments with --mount given is a leftover DISC-ROOT
	// from the all-discs-at-once form, not that mode's own SNAPSHOT
	// OUT-DIR pair.
	if !multi && *mountFlag != "" && fs.NArg() == 3 {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount takes no DISC-ROOT")
		_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] [--no-eject] SNAPSHOT OUT-DIR")
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
// the caller reports it as a failed restore, with whatever was already
// spooled left in place for a later resume.
var errStdinClosed = fmt.Errorf("stdin closed while waiting for the next disc")

// cmdRestoreDiscSwap runs the single-drive restore of OPERATIONS.md's
// "14.2 Disc-major order": build the plan, print the disc list, then
// read one disc at a time, prompting the operator between discs.
// dryRun prints the disc list and stops there, before any disc is read
// and before the repository lock is taken.
func cmdRestoreDiscSwap(repoFlag string, includes stringList, overwrite bool, mountDir string, noEject, dryRun bool, snapshotArg, outDir string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if mountDir == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount is required; OPERATIONS.md's configuration reference names no restore.mount key")
		return 2
	}

	repoDir, err := discoverRepo(repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
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
	return cmdRestoreDiscSwapRun(c, repoDir, snapID, includes, overwrite, mountDir, noEject, dryRun, outDir, stdout, stderr, prog)
}

// cmdRestoreDiscSwapRun is cmdRestoreDiscSwap's body once snapID and
// includes are known.
func cmdRestoreDiscSwapRun(c *cache.Cache, repoDir string, snapID object.ID, includes []string, overwrite bool, mountDir string, noEject, dryRun bool, outDir string, stdout, stderr io.Writer, prog *progress.Reporter) int {
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

	result, err := plan.Build(c, snap, snapID, includes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	printPlanText(stdout, result)
	if len(result.Missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: restore: %d object(s) have no run known to the cache; run recover with more discs\n", result.MissingObjectCount())
		return 1
	}
	if dryRun {
		return 0
	}

	lk, code, ok := lockRepo("restore", repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	m, err := restore.BuildManifest(c, snap, outDir, includes, overwrite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}

	spoolRoot := filepath.Join(repoDir, "staging", "restore", snapID.TextForm())
	spoolDir := filepath.Join(spoolRoot, "objects")
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	resumed, err := resumeSpool(spoolDir, m, prog)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	if resumed > 0 {
		_, _ = fmt.Fprintf(stdout, "resuming: %d object(s) already spooled\n", resumed)
	}

	scanner := bufio.NewScanner(restoreStdin)
	var prevDiscUUID [16]byte
	havePrevDisc := false
	for _, d := range result.Discs {
		wanted := wantedChunkEntries(d, m)
		if len(wanted) == 0 {
			continue
		}
		// With --no-eject, the disc detectDisc reads first is still the
		// one this loop just finished: that is not a wrong disc, only
		// the operator not having swapped it out yet, so it does not
		// get the "expected ... found ..." mismatch line.
		skipUUID, haveSkipUUID := [16]byte{}, false
		if noEject && havePrevDisc {
			skipUUID, haveSkipUUID = prevDiscUUID, true
		}
		if err := detectDisc(mountDir, c, d, scanner, stdout, stderr, skipUUID, haveSkipUUID); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 2
		}
		prevDiscUUID, havePrevDisc = d.DiscUUID, true

		// One pass: every chunk this disc owes the manifest is spooled
		// before the disc is read again, matching OPERATIONS.md's
		// "read every needed object from it in one pass".
		for _, o := range wanted {
			payload, err := restore.ReadChunkFromRoot(mountDir, o.ID)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
				return 2
			}
			if err := os.WriteFile(restore.SpoolObjectPath(spoolDir, o.ID), payload, 0o644); err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
				return 2
			}
			m.MarkSpooled(o.ID)
		}
		if _, _, err := m.WriteReady(spoolDir, prog); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 2
		}
		if !noEject {
			ejectDrive(mountDir, stderr)
		}
	}

	if m.Pending() {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: not every file could be assembled; missing object(s):")
		for _, id := range m.MissingObjects() {
			_, _ = fmt.Fprintln(stderr, " ", id.TextForm())
		}
		return 2
	}
	m.Finish()
	rep := m.Report()
	printProblems(stderr, rep)
	if code := printRestoreResult(stdout, rep, snapID, outDir); code != 0 {
		return code
	}
	if err := removeEmptySpoolRoot(spoolRoot); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
	}
	return 0
}

// printPlanText prints one line per disc, by disc number and then by
// uuid, then the plan's totals. The operator looks a disc up by the
// number on its sleeve, so the list follows that number, not the read
// order the planner chose.
func printPlanText(stdout io.Writer, r *plan.Result) {
	discs := append([]plan.DiscEntry(nil), r.Discs...)
	sort.Slice(discs, func(i, j int) bool {
		if discs[i].DiscSeq != discs[j].DiscSeq {
			return discs[i].DiscSeq < discs[j].DiscSeq
		}
		return plan.UUIDText(discs[i].DiscUUID) < plan.UUIDText(discs[j].DiscUUID)
	})
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

// removeEmptySpoolRoot removes a disc-swap restore's staging/restore/
// <snapshot> directory once every file has been assembled and no spool
// object remains under it. A failed or still-pending restore leaves the
// directory in place, so a later resume still finds its spooled objects.
func removeEmptySpoolRoot(spoolRoot string) error {
	if err := os.Remove(filepath.Join(spoolRoot, "objects")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(spoolRoot); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// wantedChunkEntries returns the chunk objects d's plan entry assigns
// that the manifest still needs, in their existing plan order. Only
// chunk payloads are read from a mounted disc: BuildManifest already
// resolved every tree and blob from the cache.
func wantedChunkEntries(d plan.DiscEntry, m *restore.Manifest) []plan.ObjectEntry {
	var out []plan.ObjectEntry
	for _, o := range d.Objects {
		if o.Kind != format.ObjectKindChunk {
			continue
		}
		if m.NeedsChunk(o.ID) {
			out = append(out, o)
		}
	}
	return out
}

// resumeSpool marks every object already spooled from an earlier,
// interrupted run, and assembles any file that completes as a result.
// It returns how many spooled objects it found, so the caller can
// report "resuming: N object(s) already spooled".
func resumeSpool(spoolDir string, m *restore.Manifest, prog *progress.Reporter) (count int, err error) {
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	for _, e := range entries {
		id, err := object.ParseID(e.Name())
		if err != nil {
			continue
		}
		m.MarkSpooled(id)
	}
	if _, _, err := m.WriteReady(spoolDir, prog); err != nil {
		return len(entries), err
	}
	return len(entries), nil
}

// discSwapRetries is how many times detectDisc retries an unreadable
// DISC.bin before it prompts the operator, and the pause between tries.
const discSwapRetries = 3

var discSwapRetryPause = 300 * time.Millisecond

// detectDisc waits until the disc d names is in the drive at mountDir,
// OPERATIONS.md's "14.6 Disc detection": no prompt when the expected
// disc is already there; a prompt, and a mismatch report, otherwise.
// skipUUID, when haveSkipUUID is true, is a disc uuid the very first
// read must not report as a mismatch even though it is not d's own
// disc: with --no-eject, the disc still in the drive right after a
// read is the one the restore loop just finished, not a wrong disc the
// operator inserted, so it does not get the "expected ... found ..."
// line before the first prompt for d. A later, still-wrong read, once
// the operator has already been prompted once, is reported normally.
func detectDisc(mountDir string, c *cache.Cache, d plan.DiscEntry, scanner *bufio.Scanner, stdout, stderr io.Writer, skipUUID [16]byte, haveSkipUUID bool) error {
	unreadable := 0
	// promptedOnce tracks whether this call has already shown a
	// prompt, so a later, still-wrong read is reported the normal way
	// even when it happens to match the disc --no-eject asked this
	// call to skip once.
	promptedOnce := false
	for {
		uuid, err := restore.ReadDiscUUID(mountDir)
		switch {
		case err != nil:
			unreadable++
			if unreadable <= discSwapRetries {
				time.Sleep(discSwapRetryPause)
				continue
			}
		case uuid == d.DiscUUID:
			_, _ = fmt.Fprintf(stdout, "disc %d %s: found\n", d.DiscSeq, d.Label)
			return nil
		default:
			if promptedOnce || !haveSkipUUID || uuid != skipUUID {
				_, _ = fmt.Fprintf(stderr, "expected disc %s (%s), found %s (%s)\n",
					plan.UUIDText(d.DiscUUID), d.Label, plan.UUIDText(uuid), labelForUUID(c, uuid))
			}
		}
		_, _ = fmt.Fprintf(stderr, "insert disc %d %q (uuid %s) into %s and press Enter\n",
			d.DiscSeq, d.Label, plan.UUIDText(d.DiscUUID), mountDir)
		if !scanner.Scan() {
			return errStdinClosed
		}
		unreadable = 0
		promptedOnce = true
	}
}

// labelForUUID looks up a disc's label in the cache's DISCS table, for
// the mismatch report. It returns "" when the cache does not know uuid.
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

// ejectWarnedNoBinary and ejectWarnedPermission latch the two eject
// warnings this loop can print, so a multi-disc restore prints each at
// most once instead of once per disc.
var ejectWarnedNoBinary, ejectWarnedPermission bool

// geteuid is os.Geteuid, a seam so a test can stand in for a non-root
// process without actually running as one.
var geteuid = os.Geteuid

// ejectDrive unmounts and ejects mountDir. A missing eject binary is
// skipped with one line, not a per-disc warning: it is expected on a
// minimal host and the operator can still remove the disc by hand. An
// unmount failure while not running as root is almost always a
// permission problem, so it prints one line pointing at sudo or
// --no-eject, at most once; the operator can still remove the disc by
// hand either way.
func ejectDrive(mountDir string, stderr io.Writer) {
	if out, err := exec.Command("umount", mountDir).CombinedOutput(); err != nil {
		if geteuid() != 0 {
			if !ejectWarnedPermission {
				_, _ = fmt.Fprintf(stderr, "noahsark: restore: umount %s failed; run restore with sudo, or pass --no-eject\n", mountDir)
				ejectWarnedPermission = true
			}
		} else {
			_, _ = fmt.Fprintf(stderr, "warning: umount %s: %v: %s\n", mountDir, err, out)
		}
	}
	if _, err := exec.LookPath("eject"); err != nil {
		if !ejectWarnedNoBinary {
			_, _ = fmt.Fprintln(stderr, "eject: not found on PATH, skipping")
			ejectWarnedNoBinary = true
		}
		return
	}
	if err := exec.Command("eject", mountDir).Run(); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: eject %s: %v\n", mountDir, err)
	}
}
