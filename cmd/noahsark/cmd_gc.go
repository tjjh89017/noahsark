package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// gcClock is the source of the current time gc measures retention
// against. Tests replace it with a fake clock to check the retention
// rules without waiting.
var gcClock = time.Now

// gcStdin is where gc's --force-after confirmation reads the operator's
// answer from. Tests replace it with a pipe.
var gcStdin io.Reader = os.Stdin

// gcStdinIsTerminal reports whether gc's real stdin is a terminal. Tests
// replace this to exercise the confirmation prompt without a real
// terminal attached.
var gcStdinIsTerminal = func() bool {
	return isTerminal(os.Stdin)
}

// cmdGC implements "noahsark gc". OPERATIONS.md's own CLI reference
// (16.20) gives gc only --dry-run and --force-after; this build adds
// --keep-snapshots, an explicit override of cache.snapshot_depth
// (17.12), since 2.4's local cache layout and 4.5's GC rules both
// describe trimming the cache as part of gc's job, and a config key
// alone would leave no way to try a different depth without editing the
// repository. See docs/decisions.md, "4. Staging state machine".
func cmdGC(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark gc [--dry-run] [--keep-snapshots=N] [--force-after=DURATION] [--yes]",
		"Delete GC-ELIGIBLE staging objects and trim the local cache.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	dryRun := fs.Bool("dry-run", false, "print what would be deleted, and free nothing")
	keepSnapshots := fs.Int("keep-snapshots", -1, "keep cache trees and blobs reachable from only the newest N snapshots; default cache.snapshot_depth")
	forceAfter := fs.String("force-after", "", "shorten retention to this duration for this run only, ignoring staging.retain_after_clean; it does not pass by gc.min_verified_copies; requires confirmation")
	yes := fs.Bool("yes", false, "skip --force-after's interactive confirmation")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("gc", fs, stderr) {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark gc [--dry-run] [--keep-snapshots=N] [--force-after=DURATION] [--yes]")
		return 2
	}
	if *keepSnapshots < -1 {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc: --keep-snapshots must not be negative")
		return 2
	}
	retainAfterCleanOverride := time.Duration(-1)
	if *forceAfter != "" {
		d, err := parseRetentionDuration(*forceAfter)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: gc: --force-after:", err)
			return 2
		}
		retainAfterCleanOverride = d
	}
	// Refuse a non-interactive --force-after before anything else runs,
	// including the eligibility scan: whether any object turns out to
	// be eligible must never change whether this confirmation is
	// required.
	if retainAfterCleanOverride >= 0 && !*dryRun && !*yes && !gcStdinIsTerminal() {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc: --force-after needs an interactive confirmation; stdin is not a terminal, pass --yes")
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}

	// gc --dry-run still reads the state log to report what it would
	// delete, so it takes the same lock as a real gc.
	lk, code, ok := lockExclusive("gc", repoDir, cfg.LockTimeout, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}
	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}
	warnIfTruncated("gc", stageLog, stderr)
	cacheDir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}
	c, err := cache.Open(cacheDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}

	retainAfterClean := cfg.RetainAfterClean
	if retainAfterCleanOverride >= 0 {
		retainAfterClean = retainAfterCleanOverride
	}

	candidates, uncached := gcPlanStagingObjects(stageLog, c, cfg.StagingDir, retainAfterClean, cfg.MinVerifiedCopies, gcClock())
	if retainAfterCleanOverride >= 0 && !*dryRun && len(candidates) > 0 {
		if code, ok := confirmForceAfter(candidates, *yes, stdout, stderr); !ok {
			return code
		}
	}
	objDeleted, objBytes := gcApplyStagingObjects(stageLog, candidates, *dryRun)

	depth := cfg.CacheSnapshotDepth
	if *keepSnapshots >= 0 {
		depth = *keepSnapshots
	}
	var treesDeleted int
	var treesBytes uint64
	if depth > 0 {
		treesDeleted, treesBytes, err = gcTrimCache(c, depth, *dryRun)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
			return 2
		}
	}

	verb := "deleted"
	if *dryRun {
		verb = "would delete"
	}
	_, _ = fmt.Fprintf(stdout, "gc: staging: %s %d object(s), %d bytes\n", verb, objDeleted, objBytes)
	if *dryRun {
		printDryRunGroupSummary(candidates, stdout)
	}
	printHeldForCopies(stdout, stageLog, cfg.MinVerifiedCopies)
	_, _ = fmt.Fprintf(stdout, "gc: cache: %s %d tree(s)/blob(s), %d bytes\n", verb, treesDeleted, treesBytes)
	if uncached > 0 {
		_, _ = fmt.Fprintf(stdout, "gc: %d object(s) skipped: their run's INDEX is not cached\n", uncached)
	}

	if objDeleted == 0 && treesDeleted == 0 {
		if *dryRun {
			if uncached == 0 {
				printNothingEligibleYet(stdout, stageLog, cfg.RetainAfterClean, cfg.MinVerifiedCopies)
			}
			return 0
		}
		return 1
	}
	return 0
}

// printNothingEligibleYet prints gc's dry-run message for a repository
// where nothing is eligible for deletion yet, naming the earliest date
// a CLEAN object reaches retainAfterClean and becomes eligible, when the
// staging log holds a CLEAN object to measure that from.
func printNothingEligibleYet(stdout io.Writer, l *stage.Log, retainAfterClean time.Duration, minCopies int) {
	when, ok := earliestEligibleAt(l, retainAfterClean, minCopies, gcClock())
	if !ok {
		_, _ = fmt.Fprintln(stdout, "gc: nothing is eligible yet")
		return
	}
	_, _ = fmt.Fprintf(stdout, "gc: nothing is eligible yet; earliest eligible date: %s\n", when.Format(time.RFC3339))
}

// earliestEligibleAt returns the earliest time some CLEAN object reaches
// retainAfterClean and becomes GC-ELIGIBLE, and whether the staging log
// holds any CLEAN object to measure that from. An object with fewer than
// minCopies verifies has no such date yet: only another verify, not the
// passing of time, can free it.
func earliestEligibleAt(l *stage.Log, retainAfterClean time.Duration, minCopies int, now time.Time) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, id := range l.IDsInState(stage.Clean) {
		if !hasVerifiedCopies(l, id, minCopies) {
			continue
		}
		cleanAt, ok := l.CleanTime(id)
		if !ok {
			continue
		}
		eligibleAt := cleanAt.Add(retainAfterClean)
		if !found || eligibleAt.Before(earliest) {
			earliest = eligibleAt
			found = true
		}
	}
	return earliest, found
}

// gcCandidates returns every object id eligible for deletion: already
// GC-ELIGIBLE, or CLEAN for at least retainAfterClean as of now. An
// object with fewer than minCopies successful verifies is never a
// candidate: two identical discs are the redundancy, so the staged bytes
// stay until the second copy has been read back. It does not itself
// change any state; the caller promotes CLEAN to GC-ELIGIBLE only when
// it is not a dry run.
func gcCandidates(l *stage.Log, retainAfterClean time.Duration, minCopies int, now time.Time) []object.ID {
	var ids []object.ID
	for _, id := range l.IDsInState(stage.GCEligible) {
		if hasVerifiedCopies(l, id, minCopies) {
			ids = append(ids, id)
		}
	}
	for _, id := range l.IDsInState(stage.Clean) {
		if !hasVerifiedCopies(l, id, minCopies) {
			continue
		}
		cleanAt, ok := l.CleanTime(id)
		if !ok {
			continue
		}
		if now.Sub(cleanAt) >= retainAfterClean {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].TextForm() < ids[j].TextForm() })
	return ids
}

// hasVerifiedCopies reports whether id has at least minCopies successful
// verifies.
func hasVerifiedCopies(l *stage.Log, id object.ID, minCopies int) bool {
	rec, ok := l.Get(id)
	return ok && int(rec.VerifyCount) >= minCopies
}

// printHeldForCopies prints one line for each disc that holds CLEAN
// objects back because the disc has fewer than minCopies successful
// verifies. It names the lowest count of the disc, the number of objects
// held, and the action that frees them.
func printHeldForCopies(stdout io.Writer, l *stage.Log, minCopies int) {
	type held struct {
		objects int
		lowest  uint8
	}
	byDisc := make(map[[16]byte]*held)
	for _, id := range l.IDsInState(stage.Clean) {
		rec, ok := l.Get(id)
		if !ok || int(rec.VerifyCount) >= minCopies {
			continue
		}
		h, ok := byDisc[rec.DiscUUID]
		if !ok {
			h = &held{lowest: rec.VerifyCount}
			byDisc[rec.DiscUUID] = h
		}
		h.objects++
		if rec.VerifyCount < h.lowest {
			h.lowest = rec.VerifyCount
		}
	}
	uuids := make([]string, 0, len(byDisc))
	order := make(map[string][16]byte, len(byDisc))
	for u := range byDisc {
		text := uuidText(u)
		uuids = append(uuids, text)
		order[text] = u
	}
	slices.Sort(uuids)
	for _, text := range uuids {
		h := byDisc[order[text]]
		_, _ = fmt.Fprintf(stdout, "gc: disc %s: %d of %d copies verified; %d object(s) held; verify the second copy\n",
			text, h.lowest, minCopies, h.objects)
	}
}

// gcObj is one staging object gc's rules allow deleting: its id, the
// path to its staged file, the size to report and free, the run it
// belongs to, and whether it must still be promoted from CLEAN to
// GC-ELIGIBLE before a real (non-dry-run) delete.
type gcObj struct {
	id       object.ID
	path     string
	size     uint64
	runSeq   uint64
	wasClean bool
}

// gcPlanStagingObjects lists every staging object gc's rules allow
// deleting as of now, without changing any state. An object whose run's
// INDEX is not cached is left off the list and counted separately:
// OPERATIONS.md's GC rules require confirming presence through the
// cached manifest before every delete.
func gcPlanStagingObjects(l *stage.Log, c *cache.Cache, stagingDir string, retainAfterClean time.Duration, minCopies int, now time.Time) (objs []gcObj, uncached int) {
	for _, id := range gcCandidates(l, retainAfterClean, minCopies, now) {
		rec, ok := l.Get(id)
		if !ok {
			continue
		}
		idx, err := c.IndexForRun(rec.RunSeq)
		if err != nil {
			uncached++
			continue
		}
		row, found := findObjectRow(idx, id)
		if !found {
			uncached++
			continue
		}

		path := image.StagedPath(stagingDir, id, row.Kind)
		size := row.StoredLen
		if fi, err := os.Stat(path); err == nil {
			size = uint64(fi.Size())
		}
		objs = append(objs, gcObj{id: id, path: path, size: size, runSeq: rec.RunSeq, wasClean: rec.State == stage.Clean})
	}
	return objs, uncached
}

// gcApplyStagingObjects deletes (or, under dryRun, reports) every object
// gcPlanStagingObjects listed. Under dryRun, gcApplyStagingObjects
// prints nothing per object, leaving the report to
// printDryRunGroupSummary's per-run summary.
func gcApplyStagingObjects(l *stage.Log, objs []gcObj, dryRun bool) (deleted int, bytesFreed uint64) {
	for _, o := range objs {
		if dryRun {
			deleted++
			bytesFreed += o.size
			continue
		}

		if o.wasClean {
			if err := l.MarkGCEligible(o.id); err != nil {
				continue
			}
		}
		removeErr := os.Remove(o.path)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			continue
		}
		if err := l.MarkDeleted(o.id); err != nil {
			continue
		}
		// A file already gone (IsNotExist) frees nothing this run: count
		// and report only the bytes and objects an actual removal freed.
		if removeErr == nil {
			deleted++
			bytesFreed += o.size
		}
	}
	return deleted, bytesFreed
}

// printDryRunGroupSummary prints one line per run_seq objs groups by,
// each with that run's own eligible object count and bytes, in
// ascending run_seq order. It is gc --dry-run's whole report.
func printDryRunGroupSummary(objs []gcObj, stdout io.Writer) {
	type group struct {
		objects int
		bytes   uint64
	}
	byRun := make(map[uint64]*group)
	var runSeqs []uint64
	for _, o := range objs {
		g, ok := byRun[o.runSeq]
		if !ok {
			g = &group{}
			byRun[o.runSeq] = g
			runSeqs = append(runSeqs, o.runSeq)
		}
		g.objects++
		g.bytes += o.size
	}
	slices.Sort(runSeqs)
	for _, seq := range runSeqs {
		g := byRun[seq]
		_, _ = fmt.Fprintf(stdout, "would delete: run %d: %d object(s), %d bytes\n", seq, g.objects, g.bytes)
	}
}

// gcTotalBytes sums every object's size in objs.
func gcTotalBytes(objs []gcObj) uint64 {
	var total uint64
	for _, o := range objs {
		total += o.size
	}
	return total
}

// confirmForceAfter asks the operator to confirm a --force-after delete
// on stderr, reading the answer from gcStdin, unless yes is already
// given. It refuses outright when gc's stdin is not a terminal and yes
// was not given: a killed or scripted session must not silently delete
// under a shortened retention. It reports ok=false, with the exit code
// to return, when the run should stop instead of deleting.
func confirmForceAfter(objs []gcObj, yes bool, stdout, stderr io.Writer) (exitCode int, ok bool) {
	if yes {
		return 0, true
	}
	if !gcStdinIsTerminal() {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc: --force-after needs an interactive confirmation; stdin is not a terminal, pass --yes")
		return 2, false
	}
	_, _ = fmt.Fprintf(stderr, "delete %d object(s), %d bytes? [y/N] ", len(objs), gcTotalBytes(objs))
	scanner := bufio.NewScanner(gcStdin)
	answer := ""
	if scanner.Scan() {
		answer = strings.TrimSpace(strings.ToLower(scanner.Text()))
	}
	if answer != "y" && answer != "yes" {
		_, _ = fmt.Fprintln(stdout, "gc: --force-after not confirmed; nothing deleted")
		return 2, false
	}
	return 0, true
}

// findObjectRow returns idx's Objects row for id, confirming the object
// is actually present in the run gc is about to delete its staging copy
// of.
func findObjectRow(idx *format.Index, id object.ID) (format.IndexObjectRecord, bool) {
	for _, row := range idx.Objects {
		if object.ID(row.ContentID) == id {
			return row, true
		}
	}
	return format.IndexObjectRecord{}, false
}

// gcTrimCache keeps only the trees and blobs reachable from the newest
// keep cached snapshots, and deletes the rest, reporting how many
// objects and bytes it removed. Every runs/<seq>/ catalog copy and
// every snapshot object are always kept.
func gcTrimCache(c *cache.Cache, keep int, dryRun bool) (int, uint64, error) {
	newest, err := c.NewestSnapshotsByTime(keep)
	if err != nil {
		return 0, 0, err
	}
	return c.TrimToSnapshots(newest, dryRun)
}
