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
// answer from. Tests replace it with a pipe, and a script answers it
// with a pipe too.
var gcStdin io.Reader = os.Stdin

// gcRemove unlinks one staged file. Tests replace it to fail the unlink
// after the durable record, and so to check that the next gc run frees
// the orphan the crash left behind.
var gcRemove = os.Remove

// cmdGC implements "noahsark gc". It frees the staging bytes of an
// object that two verified copies already hold, and nothing else: the
// local cache is never trimmed. See docs/decisions.md, "4. Staging
// state machine".
func cmdGC(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark gc [--dry-run] [--force-after=DURATION]",
		"Delete the staged files of objects that verified discs hold.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	dryRun := fs.Bool("dry-run", false, "print what would be deleted, and free nothing")
	forceAfter := fs.String("force-after", "", "shorten retention to this duration for this run only, ignoring staging.retain_after_clean; it does not pass by gc.min_verified_copies; requires confirmation")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("gc", fs, stderr) {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark gc [--dry-run] [--force-after=DURATION]")
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
	candidates = append(candidates, gcOrphans(stageLog, cfg.StagingDir)...)
	if retainAfterCleanOverride >= 0 && !*dryRun && len(candidates) > 0 {
		if code, ok := confirmForceAfter(candidates, stdout, stderr); !ok {
			return code
		}
	}
	objDeleted, objBytes := gcApplyStagingObjects(stageLog, candidates, *dryRun)

	verb := "deleted"
	if *dryRun {
		verb = "would delete"
	}
	_, _ = fmt.Fprintf(stdout, "gc: staging: %s %d object(s), %d bytes\n", verb, objDeleted, objBytes)
	if *dryRun {
		printDryRunGroupSummary(candidates, stdout)
	}
	printHeldForCopies(stdout, stageLog, cfg.MinVerifiedCopies)
	if uncached > 0 {
		_, _ = fmt.Fprintf(stdout, "gc: %d object(s) skipped: their disc's INDEX is not cached\n", uncached)
	}

	if objDeleted == 0 {
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
// retainAfterClean and gc may free it, and whether the staging log
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

// gcCandidates returns every object id eligible for deletion: CLEAN for
// at least retainAfterClean as of now. An object with fewer than
// minCopies successful verifies is never a candidate: two identical
// discs are the redundancy, so the staged bytes stay until the second
// copy has been read back. It changes no state itself.
func gcCandidates(l *stage.Log, retainAfterClean time.Duration, minCopies int, now time.Time) []object.ID {
	var ids []object.ID
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
// path to its staged file, the size to report and free, the disc it
// belongs to, and whether gc must still record it ON-DISC before it
// unlinks the file. An orphan left by a crash already has that record.
type gcObj struct {
	id          object.ID
	path        string
	size        uint64
	discUUID    [16]byte
	needsRecord bool
}

// gcPlanStagingObjects lists every staging object gc's rules allow
// deleting as of now, without changing any state. An object whose own
// disc has no cached INDEX is left off the list and counted separately:
// OPERATIONS.md's GC rules require confirming presence through the
// cached index before every delete. The disc uuid of the object's own
// state record is the key, so an index of another disc that repeats the
// same run_seq can never stand in for it.
func gcPlanStagingObjects(l *stage.Log, c *cache.Cache, stagingDir string, retainAfterClean time.Duration, minCopies int, now time.Time) (objs []gcObj, uncached int) {
	for _, id := range gcCandidates(l, retainAfterClean, minCopies, now) {
		rec, ok := l.Get(id)
		if !ok {
			continue
		}
		idx, err := c.IndexForDisc(rec.DiscUUID)
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
		objs = append(objs, gcObj{id: id, path: path, size: size, discUUID: rec.DiscUUID, needsRecord: true})
	}
	return objs, uncached
}

// gcOrphans lists every ON-DISC object whose staged file is still on the
// disk. gc writes the ON-DISC record, flushes it, and only then unlinks
// the file, so a crash between the two leaves exactly this: a file with
// no owner. The durable record already proves a disc holds the object,
// thus the next gc run frees the file with no further check.
func gcOrphans(l *stage.Log, stagingDir string) []gcObj {
	var objs []gcObj
	for _, id := range l.IDsInState(stage.OnDiscOnly) {
		rec, ok := l.Get(id)
		if !ok {
			continue
		}
		for _, kind := range []format.ObjectKind{format.ObjectKindChunk, format.ObjectKindSnapshot} {
			path := image.StagedPath(stagingDir, id, kind)
			fi, err := os.Stat(path)
			if err != nil {
				continue
			}
			objs = append(objs, gcObj{id: id, path: path, size: uint64(fi.Size()), discUUID: rec.DiscUUID})
		}
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].path < objs[j].path })
	return objs
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

		// The record goes to the disk first. A crash after it and before
		// the unlink leaves an orphan file the next run frees; a crash
		// the other way round would leave an object the log calls CLEAN
		// with no bytes behind it.
		if o.needsRecord {
			if err := l.MarkOnDisc(o.id); err != nil {
				continue
			}
		}
		removeErr := gcRemove(o.path)
		if removeErr != nil && !os.IsNotExist(removeErr) {
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

// printDryRunGroupSummary prints one line per disc objs groups by, each
// with that disc's own eligible object count and bytes, in uuid text
// order. It is gc --dry-run's whole report.
func printDryRunGroupSummary(objs []gcObj, stdout io.Writer) {
	type group struct {
		objects int
		bytes   uint64
	}
	byDisc := make(map[string]*group)
	var uuids []string
	for _, o := range objs {
		text := uuidText(o.discUUID)
		g, ok := byDisc[text]
		if !ok {
			g = &group{}
			byDisc[text] = g
			uuids = append(uuids, text)
		}
		g.objects++
		g.bytes += o.size
	}
	slices.Sort(uuids)
	for _, text := range uuids {
		g := byDisc[text]
		_, _ = fmt.Fprintf(stdout, "would delete: disc %s: %d object(s), %d bytes\n", text, g.objects, g.bytes)
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
// on stderr, and reads the answer from gcStdin. A closed or empty stdin
// answers no, thus a killed session never deletes under a shortened
// retention. A script answers with a pipe: "echo y | noahsark gc
// --force-after=1h". It reports ok=false, with the exit code to return,
// when the run must stop instead of deleting.
func confirmForceAfter(objs []gcObj, stdout, stderr io.Writer) (exitCode int, ok bool) {
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
