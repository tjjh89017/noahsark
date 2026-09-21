package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	if refuseBadConfig("gc", cfg, stderr, configKeysForGC...) {
		return 2
	}

	// gc --dry-run still reads the state log to report what it would
	// delete, so it takes the same lock as a real gc.
	lk, code, ok := lockRepo("gc", repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	warnIfTruncated("gc", stageLog, stderr)
	cacheDir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	c, err := cache.Open(cacheDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}

	retainAfterClean := cfg.RetainAfterClean
	if retainAfterCleanOverride >= 0 {
		retainAfterClean = retainAfterCleanOverride
	}

	discNames := discNamesFromLedger(cfg.StagingDir, repoUUID)
	candidates, uncached := gcPlanStagingObjects(stageLog, c, cfg.StagingDir, retainAfterClean, cfg.MinVerifiedCopies, gcClock())
	candidates = append(candidates, gcOrphans(stageLog, cfg.StagingDir)...)
	if retainAfterCleanOverride >= 0 && !*dryRun && len(candidates) > 0 {
		if code, ok := confirmForceAfter(candidates, stdout, stderr); !ok {
			return code
		}
	}
	planDirs := gcPlanDirs(stageLog, cfg.StagingDir, candidates)
	objDeleted, objBytes, failures := gcApplyStagingObjects(stageLog, candidates, *dryRun)
	dirDeleted, dirBytes, dirFailures := gcApplyPlanDirs(planDirs, *dryRun)
	failures = append(failures, dirFailures...)

	verb := "deleted"
	if *dryRun {
		verb = "would delete"
	}
	_, _ = fmt.Fprintf(stdout, "gc: staging: %s %d staged object(s), %d bytes\n", verb, objDeleted, objBytes)
	_, _ = fmt.Fprintf(stdout, "gc: plans: %s %d disc plan directory(ies), %d bytes\n", verb, dirDeleted, dirBytes)
	if *dryRun {
		printDryRunGroupSummary(candidates, discNames, stdout)
		for _, d := range planDirs {
			_, _ = fmt.Fprintf(stdout, "would delete: %s: plan directory %s, %d bytes\n", discNameOf(discNames, d.discUUID), d.path, d.bytes)
		}
	}
	printHeldForCopies(stdout, stageLog, discNames, cfg.MinVerifiedCopies)
	if uncached > 0 {
		_, _ = fmt.Fprintf(stdout, "gc: %d object(s) skipped: their disc's INDEX is not cached\n", uncached)
	}

	// A staged file gc could not unlink is a failure at run time, named
	// by its path and the underlying error, never silently folded into
	// "nothing eligible".
	for _, f := range failures {
		_, _ = fmt.Fprintf(stderr, "noahsark: gc: %s: %v\n", f.path, f.err)
	}
	if len(failures) > 0 {
		return 1
	}

	if objDeleted == 0 && dirDeleted == 0 && *dryRun && uncached == 0 {
		printNothingEligibleYet(stdout, stageLog, cfg.RetainAfterClean, cfg.MinVerifiedCopies)
	}
	// Nothing eligible, whether reported by --dry-run or found true by a
	// real run, is success: gc did everything the repository's state
	// allows, and there was nothing to free.
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
func printHeldForCopies(stdout io.Writer, l *stage.Log, names map[[16]byte]string, minCopies int) {
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
		_, _ = fmt.Fprintf(stdout, "gc: %s: %d of %d copies verified; %d object(s) held; verify the second copy\n",
			discNameOf(names, order[text]), h.lowest, minCopies, h.objects)
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
		row, byteLen, found := findObjectRow(idx, id)
		if !found {
			uncached++
			continue
		}

		path := image.StagedPath(stagingDir, id, row.Kind)
		size := byteLen
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

// gcFailure names one staging object gc could not free, and why, so the
// operator sees a reason instead of a bare zero count.
type gcFailure struct {
	path string
	err  error
}

// gcApplyStagingObjects deletes (or, under dryRun, reports) every object
// gcPlanStagingObjects listed. Under dryRun, gcApplyStagingObjects
// prints nothing per object, leaving the report to
// printDryRunGroupSummary's per-run summary. It reports every object it
// could not record or unlink, instead of counting it as freed nothing
// with no reason given.
func gcApplyStagingObjects(l *stage.Log, objs []gcObj, dryRun bool) (deleted int, bytesFreed uint64, failures []gcFailure) {
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
				failures = append(failures, gcFailure{path: o.path, err: fmt.Errorf("recording it ON-DISC: %w", err)})
				continue
			}
		}
		removeErr := gcRemove(o.path)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			failures = append(failures, gcFailure{path: o.path, err: removeErr})
			continue
		}
		// A file already gone (IsNotExist) frees nothing this run: count
		// and report only the bytes and objects an actual removal freed.
		if removeErr == nil {
			deleted++
			bytesFreed += o.size
		}
	}
	return deleted, bytesFreed, failures
}

// printDryRunGroupSummary prints one line per disc objs groups by, each
// with that disc's own eligible object count and bytes, in uuid text
// order. It is gc --dry-run's whole report.
func printDryRunGroupSummary(objs []gcObj, names map[[16]byte]string, stdout io.Writer) {
	type group struct {
		objects int
		bytes   uint64
	}
	byDisc := make(map[string]*group)
	order := make(map[string][16]byte)
	var uuids []string
	for _, o := range objs {
		text := uuidText(o.discUUID)
		g, ok := byDisc[text]
		if !ok {
			g = &group{}
			byDisc[text] = g
			order[text] = o.discUUID
			uuids = append(uuids, text)
		}
		g.objects++
		g.bytes += o.size
	}
	slices.Sort(uuids)
	for _, text := range uuids {
		g := byDisc[text]
		_, _ = fmt.Fprintf(stdout, "would delete: %s: %d object(s), %d bytes\n", discNameOf(names, order[text]), g.objects, g.bytes)
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
		return 1, false
	}
	return 0, true
}

// findObjectRow returns idx's Objects row for id, and the length of the
// object's file from the role 13 Files row that pairs with it. It
// confirms the object is actually present in the run gc is about to
// delete its staging copy of.
func findObjectRow(idx *format.Index, id object.ID) (format.IndexObjectRecord, uint64, bool) {
	var fileRows []format.IndexFileRecord
	for _, row := range idx.Files {
		if row.Role == format.FileRoleObject {
			fileRows = append(fileRows, row)
		}
	}
	for i, row := range idx.Objects {
		if object.ID(row.ContentID) != id {
			continue
		}
		if i >= len(fileRows) {
			return row, 0, true
		}
		return row, fileRows[i].ByteLen, true
	}
	return format.IndexObjectRecord{}, 0, false
}

// discNamesFromLedger maps each disc uuid the local ledger knows to the
// name an operator reads: the number, the label and the uuid.
func discNamesFromLedger(stagingDir string, repoUUID [16]byte) map[[16]byte]string {
	names := make(map[[16]byte]string)
	ledger, err := image.LoadDiscsLedger(stagingDir, repoUUID)
	if err != nil {
		return names
	}
	for _, row := range ledger.Rows {
		n := min(int(row.LabelLen), len(row.Label))
		names[row.DiscUUID] = discName(row.DiscSeq, string(row.Label[:n]), row.DiscUUID)
	}
	return names
}

// discNameOf returns the name of uuid, or the uuid alone when the
// ledger has no row for it.
func discNameOf(names map[[16]byte]string, uuid [16]byte) string {
	if name, ok := names[uuid]; ok {
		return name
	}
	return "disc " + uuidText(uuid)
}

// gcPlanDir is one disc's plan directory: the disc tree that pack wrote
// by default, and the image that image build put beside it.
type gcPlanDir struct {
	discUUID [16]byte
	path     string
	bytes    uint64
}

// gcPlanDirs lists the plan directory of every disc whose objects are
// all ON-DISC, pending included, since pending is what this run is
// about to record. The directory holds a second copy of bytes the disc
// itself now holds, thus gc frees it under the same rules as a staged
// file.
//
// A disc that is packed or burned, or that has fewer verifies than
// gc.min_verified_copies, keeps its directory: the operator may still
// have to build the image again, or burn the second copy. A pack with
// --out outside staging writes no plan directory, so gc never touches
// the operator's own output.
func gcPlanDirs(l *stage.Log, stagingDir string, pending []gcObj) []gcPlanDir {
	total := l.OnDiscCountByDisc()
	done := l.CountByDiscInState(stage.OnDiscOnly)
	for _, o := range pending {
		if o.needsRecord {
			done[o.discUUID]++
		}
	}
	var dirs []gcPlanDir
	for uuid, n := range total {
		if n == 0 || done[uuid] != n {
			continue
		}
		path := planDirPath(stagingDir, uuid)
		bytes, ok := dirBytes(path)
		if !ok {
			continue
		}
		dirs = append(dirs, gcPlanDir{discUUID: uuid, path: path, bytes: bytes})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].path < dirs[j].path })
	return dirs
}

// planDirPath is the directory pack writes a disc's tree under, by
// default.
func planDirPath(stagingDir string, discUUID [16]byte) string {
	return filepath.Join(stagingDir, "plans", hex.EncodeToString(discUUID[:]))
}

// dirBytes sums the size of every regular file below path. It reports
// ok false when path does not exist.
func dirBytes(path string) (uint64, bool) {
	if _, err := os.Stat(path); err != nil {
		return 0, false
	}
	var total uint64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += uint64(info.Size())
		}
		return nil
	})
	return total, true
}

// gcApplyPlanDirs removes (or, under dryRun, only counts) every plan
// directory gcPlanDirs listed.
func gcApplyPlanDirs(dirs []gcPlanDir, dryRun bool) (deleted int, bytesFreed uint64, failures []gcFailure) {
	for _, d := range dirs {
		if !dryRun {
			if err := os.RemoveAll(d.path); err != nil {
				failures = append(failures, gcFailure{path: d.path, err: err})
				continue
			}
		}
		deleted++
		bytesFreed += d.bytes
	}
	return deleted, bytesFreed, failures
}
