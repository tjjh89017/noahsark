package main

import (
	"fmt"
	"io"
	"os"
	"sort"
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

// cmdGC implements "noahsark gc". OPERATIONS.md's own CLI reference
// (16.20) gives gc only --dry-run and --force-after; this build adds
// --keep-snapshots, an explicit override of cache.snapshot_depth
// (17.12), since 2.4's local cache layout and 4.5's GC rules both
// describe trimming the cache as part of gc's job, and a config key
// alone would leave no way to try a different depth without editing the
// repository. See docs/decisions.md, "4. Staging state machine".
func cmdGC(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark gc [--dry-run] [--keep-snapshots=N]",
		"Delete GC-ELIGIBLE staging objects and trim the local cache.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	dryRun := fs.Bool("dry-run", false, "print what would be deleted, and free nothing")
	keepSnapshots := fs.Int("keep-snapshots", -1, "keep cache trees and blobs reachable from only the newest N snapshots; default cache.snapshot_depth")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("gc", fs, stderr) {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark gc [--dry-run] [--keep-snapshots=N]")
		return 2
	}
	if *keepSnapshots < -1 {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc: --keep-snapshots must not be negative")
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

	objDeleted, objBytes, uncached := gcStagingObjects(stageLog, c, cfg.StagingDir, cfg.RetainAfterClean, *dryRun, stdout)

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
	_, _ = fmt.Fprintf(stdout, "gc: cache: %s %d tree(s)/blob(s), %d bytes\n", verb, treesDeleted, treesBytes)
	if uncached > 0 {
		_, _ = fmt.Fprintf(stdout, "gc: %d object(s) skipped: their run's INDEX is not cached\n", uncached)
	}

	if objDeleted == 0 && treesDeleted == 0 {
		if *dryRun {
			if uncached == 0 {
				printNothingEligibleYet(stdout, stageLog, cfg.RetainAfterClean)
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
func printNothingEligibleYet(stdout io.Writer, l *stage.Log, retainAfterClean time.Duration) {
	when, ok := earliestEligibleAt(l, retainAfterClean, gcClock())
	if !ok {
		_, _ = fmt.Fprintln(stdout, "gc: nothing is eligible yet")
		return
	}
	_, _ = fmt.Fprintf(stdout, "gc: nothing is eligible yet; earliest eligible date: %s\n", when.Format(time.RFC3339))
}

// earliestEligibleAt returns the earliest time some CLEAN object reaches
// retainAfterClean and becomes GC-ELIGIBLE, and whether the staging log
// holds any CLEAN object to measure that from.
func earliestEligibleAt(l *stage.Log, retainAfterClean time.Duration, now time.Time) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, id := range l.IDsInState(stage.Clean) {
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
// GC-ELIGIBLE, or CLEAN for at least retainAfterClean as of now. It does
// not itself change any state; the caller promotes CLEAN to GC-ELIGIBLE
// only when it is not a dry run.
func gcCandidates(l *stage.Log, retainAfterClean time.Duration, now time.Time) []object.ID {
	ids := l.IDsInState(stage.GCEligible)
	for _, id := range l.IDsInState(stage.Clean) {
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

// gcStagingObjects deletes every eligible staging object gc's rules
// allow. An object whose run's INDEX is not cached is left alone and
// counted separately: OPERATIONS.md's GC rules require confirming
// presence through the cached manifest before every delete.
func gcStagingObjects(l *stage.Log, c *cache.Cache, stagingDir string, retainAfterClean time.Duration, dryRun bool, stdout io.Writer) (deleted int, bytesFreed uint64, uncached int) {
	now := gcClock()
	for _, id := range gcCandidates(l, retainAfterClean, now) {
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

		if !dryRun && rec.State == stage.Clean {
			if err := l.MarkGCEligible(id); err != nil {
				continue
			}
		}

		path := image.StagedPath(stagingDir, id, row.Kind)
		size := row.StoredLen
		if fi, err := os.Stat(path); err == nil {
			size = uint64(fi.Size())
		}

		if dryRun {
			_, _ = fmt.Fprintf(stdout, "would delete %s (%d bytes, run %d)\n", id.TextForm(), size, rec.RunSeq)
			deleted++
			bytesFreed += size
			continue
		}

		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			continue
		}
		if err := l.MarkDeleted(id); err != nil {
			continue
		}
		deleted++
		bytesFreed += size
	}
	return deleted, bytesFreed, uncached
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
