package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// resolves SNAPSHOT through the local cache, the same way "plan" does,
// and walks the disc-swap loop of OPERATIONS.md's "14.2 Disc-major
// order": one disc at a time, in plan order, prompting the operator to
// insert the next one. This is the single-drive path; see
// docs/decisions.md.
func cmdRestore(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if refuseLaterPhaseFlags("restore", args, stderr) {
		return 2
	}

	fs := newFlagSet("noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR|DISC-ROOT] SNAPSHOT OUT-DIR\n       noahsark restore --plan=FILE --mount=DIR [--overwrite] OUT-DIR",
		"Restore a snapshot to a directory. Accepts --disc (repeatable) or --discs-dir in place of DISC-ROOT for the all-discs-at-once mode. --plan resumes a plan file written by \"plan --out\" instead of naming SNAPSHOT.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	var discFlags stringList
	fs.Var(&discFlags, "disc", "a disc root to restore from; repeatable")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	var includeFlags stringList
	fs.Var(&includeFlags, "include", "restore only this snapshot-relative path and, if it names a directory, everything under it; repeatable")
	overwrite := fs.Bool("overwrite", false, "unlink an existing path first and then create it; without this, an existing path is left alone")
	mountFlag := fs.String("mount", "", "the directory where the drive is mounted; required for the disc-swap mode")
	noEject := fs.Bool("no-eject", false, "do not eject after each disc")
	interactive := fs.Bool("interactive", false, "prompt on every disc, not only on a mismatch")
	planFlag := fs.String("plan", "", "restore from a plan file written by plan --out")
	stagingBudgetFlag := fs.String("staging-budget", "", "peak staging bytes allowed; overrides restore.staging_budget")
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
	if *planFlag != "" && multi {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --plan cannot be combined with --disc or --discs-dir")
		return 2
	}
	if *planFlag != "" {
		if *mountFlag == "" {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore: --plan requires --mount")
			return 2
		}
		if fs.NArg() == 2 {
			// --plan already fixes the snapshot; an extra leading
			// argument here is a SNAPSHOT left over from the plain
			// SNAPSHOT OUT-DIR form, not a second OUT-DIR.
			_, _ = fmt.Fprintln(stderr, "noahsark: restore: --plan takes no SNAPSHOT")
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore --plan=FILE --mount=DIR [--no-eject] [--interactive] [--staging-budget=SIZE] OUT-DIR")
			return 2
		}
		if fs.NArg() != 1 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore --plan=FILE --mount=DIR [--no-eject] [--interactive] [--staging-budget=SIZE] OUT-DIR")
			return 2
		}
		return cmdRestoreDiscSwap(*repoFlag, includeFlags, *overwrite, *mountFlag, *noEject, *interactive, "", fs.Arg(0), *planFlag, *stagingBudgetFlag, stdout, stderr, prog)
	}
	// A bare two positional arguments, with --mount given, is the
	// disc-swap mode's SNAPSHOT OUT-DIR. Without --mount, the same two
	// arguments are ambiguous: they could equally be a DISC-ROOT
	// SNAPSHOT for the all-discs-at-once mode with OUT-DIR left off. This
	// build resolves that only by requiring --mount for disc-swap, so a
	// missing OUT-DIR reports the usage line instead of silently running
	// the wrong mode.
	if !multi && fs.NArg() == 2 {
		if *mountFlag == "" {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] [--no-eject] [--interactive] SNAPSHOT OUT-DIR")
			return 2
		}
		return cmdRestoreDiscSwap(*repoFlag, includeFlags, *overwrite, *mountFlag, *noEject, *interactive, fs.Arg(0), fs.Arg(1), "", *stagingBudgetFlag, stdout, stderr, prog)
	}
	// --mount is disc-swap mode, which never takes a DISC-ROOT: three
	// positional arguments with --mount given is a leftover DISC-ROOT
	// from the all-discs-at-once form, not that mode's own SNAPSHOT
	// OUT-DIR pair.
	if !multi && *mountFlag != "" && fs.NArg() == 3 {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount takes no DISC-ROOT")
		_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] [--no-eject] [--interactive] SNAPSHOT OUT-DIR")
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

	opts := []restore.Option{restore.WithInclude(includeFlags), restore.WithOverwrite(*overwrite)}
	if known := knownDiscsForRepo(*repoFlag); len(known) > 0 {
		opts = append(opts, restore.WithKnownDiscs(known))
	}
	skipped, err := restore.RestoreMultiWithProgress(discRoots, snapID, outDir, prog, opts...)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "restored snapshot %s into %s\n", snapID.TextForm(), outDir)
	if skipped > 0 {
		_, _ = fmt.Fprintf(stdout, "skipped %d existing path(s); pass --overwrite to replace them\n", skipped)
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

// restoreSpoolBytesObserved, when set, is called after every change to
// the disc-swap loop's running spool total, so a test can record the
// peak without polling the filesystem. It is nil (a no-op) outside
// tests.
var restoreSpoolBytesObserved func(current uint64)

// cmdRestoreDiscSwap runs the single-drive restore of OPERATIONS.md's
// "14.2 Disc-major order": build the same plan "plan" would print, then
// read one disc at a time, prompting the operator between discs.
//
// planFile, when set, resumes a persisted plan instead of building one:
// snapshotArg is then ignored (empty), and the plan's own snapshot,
// include list and disc order apply. stagingBudgetOverride, when set,
// overrides restore.staging_budget for this run, in the same units
// --capacity accepts.
func cmdRestoreDiscSwap(repoFlag string, includes stringList, overwrite bool, mountDir string, noEject, interactive bool, snapshotArg, outDir, planFile, stagingBudgetOverride string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if mountDir == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount is required; OPERATIONS.md's configuration reference names no restore.mount key")
		return 2
	}

	repoDir, err := discoverRepo(repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	stagingBudget := cfg.RestoreStagingBudget
	if stagingBudgetOverride != "" {
		stagingBudget, err = parseByteSize(stagingBudgetOverride)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore: --staging-budget:", err)
			return 2
		}
	}

	src, c, err := openCacheSource(repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	var planDiscOrder []string
	if planFile != "" {
		if len(includes) > 0 {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore: --include cannot be combined with --plan; the plan file already fixes the include list")
			return 2
		}
		doc, err := readRestorePlanFile(planFile)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 2
		}
		repoUUID, err := decodeUUID(cfg.RepoUUID)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 1
		}
		wantUUID := plan.UUIDText(repoUUID)
		if !strings.EqualFold(doc.RepoUUID, wantUUID) {
			_, _ = fmt.Fprintf(stderr, "noahsark: restore: --plan=%s is for repository %s, this repository is %s\n", planFile, doc.RepoUUID, wantUUID)
			return 2
		}
		snapID, err := object.ParseID(doc.Snapshot)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: restore: --plan=%s names snapshot %q, which is not a valid snapshot id: %v\n", planFile, doc.Snapshot, err)
			return 2
		}
		includes = doc.Include
		for _, d := range doc.Discs {
			planDiscOrder = append(planDiscOrder, strings.ToLower(d.DiscUUID))
		}
		return cmdRestoreDiscSwapRun(c, repoDir, snapID, includes, planDiscOrder, planFile, stagingBudget, overwrite, mountDir, noEject, interactive, outDir, stdout, stderr, prog)
	}

	snapID, err := src.ParseSnapshotArg(snapshotArg)
	if err != nil {
		// --mount names a disc to swap discs through, so a ref this
		// build's cache does not know is reported as possibly on a
		// disc not yet inserted, not as an unknown name outright.
		if _, ok := err.(*refNotFoundError); ok {
			err = fmt.Errorf("ref %q is not on the provided disc(s); a later disc in the chain may carry it", snapshotArg)
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	return cmdRestoreDiscSwapRun(c, repoDir, snapID, includes, nil, "", stagingBudget, overwrite, mountDir, noEject, interactive, outDir, stdout, stderr, prog)
}

// readRestorePlanFile reads and parses a plan file "plan --out" wrote.
func readRestorePlanFile(path string) (*planDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--plan=%s: %w", path, err)
	}
	var doc planDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("--plan=%s: %w", path, err)
	}
	return &doc, nil
}

// reorderDiscsForPlan reorders discs to match wantOrder, a list of
// lower-case hex disc uuids in the order a persisted plan named them.
// It fails when a named disc is not among discs: the cache no longer
// agrees with the persisted plan, most often because it was rebuilt
// from a different set of discs since the plan was written.
func reorderDiscsForPlan(discs []plan.DiscEntry, wantOrder []string) ([]plan.DiscEntry, error) {
	byUUID := make(map[string]plan.DiscEntry, len(discs))
	for _, d := range discs {
		byUUID[strings.ToLower(plan.UUIDText(d.DiscUUID))] = d
	}
	ordered := make([]plan.DiscEntry, 0, len(wantOrder))
	for _, uuid := range wantOrder {
		d, ok := byUUID[uuid]
		if !ok {
			return nil, fmt.Errorf("plan names disc %s, which the current cache's plan for this snapshot does not need; rebuild-cache and re-plan", uuid)
		}
		ordered = append(ordered, d)
	}
	return ordered, nil
}

// cmdRestoreDiscSwapRun is cmdRestoreDiscSwap's body once snapID,
// includes and, when resuming a persisted plan, planDiscOrder are known.
func cmdRestoreDiscSwapRun(c *cache.Cache, repoDir string, snapID object.ID, includes []string, planDiscOrder []string, planFile string, stagingBudget uint64, overwrite bool, mountDir string, noEject, interactive bool, outDir string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if err := c.CheckComplete(snapID); err != nil {
		if ie, ok := err.(*cache.IncompleteError); ok {
			msg := formatIncompleteError("restore", ie)
			if planFile != "" {
				msg = fmt.Sprintf("noahsark: restore: --plan=%s names a snapshot not known to this repository's cache: %s", planFile, incompleteErrorBody(ie))
			}
			_, _ = fmt.Fprintln(stderr, msg)
			return 3
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
	if planDiscOrder != nil {
		result.Discs, err = reorderDiscsForPlan(result.Discs, planDiscOrder)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 2
		}
	}
	passSplit, err := plan.ComputePasses(result.Discs, stagingBudget)
	if err != nil {
		// A single object above the budget is refused for real, with the
		// exact file it belongs to, once the manifest is built below;
		// this conservative, per-object check is not fatal by itself.
		passSplit = plan.PassSplit{DiscPasses: make([]int, len(result.Discs)), Total: 1}
		for i := range passSplit.DiscPasses {
			passSplit.DiscPasses[i] = 1
		}
	}
	printPlanText(stdout, result, passSplit)
	if len(result.Missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: restore: %d object(s) have no run known to the cache; rebuild-cache from more discs\n", result.MissingObjectCount())
		return 3
	}

	m, err := restore.BuildManifest(c, snap, outDir, includes, overwrite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}

	if path, need, ok := m.FileExceedingBudget(stagingBudget); ok {
		_, _ = fmt.Fprintf(stderr, "noahsark: restore: %s alone needs %d bytes of staging, above the staging budget of %d bytes; no split of one file's own chunks can honour it\n",
			path, need, stagingBudget)
		return 2
	}

	spoolRoot := filepath.Join(repoDir, "staging", "restore", snapID.TextForm())
	spoolDir := filepath.Join(spoolRoot, "objects")
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	resumed, spoolBytes, err := resumeSpool(spoolDir, m, prog)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	if resumed > 0 {
		_, _ = fmt.Fprintf(stdout, "resuming: %d object(s) already spooled\n", resumed)
	}
	reportSpoolBytes(spoolBytes)

	scanner := bufio.NewScanner(restoreStdin)
	var prevDiscUUID [16]byte
	havePrevDisc := false
	for i, d := range result.Discs {
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
		if err := detectDisc(mountDir, c, d, interactive, scanner, stdout, stderr, skipUUID, haveSkipUUID); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 2
		}
		prevDiscUUID, havePrevDisc = d.DiscUUID, true

		totalPasses := passSplit.DiscPasses[i]
		passNum := 0
		idx := 0
		for idx < len(wanted) {
			passNum++
			if totalPasses > 1 {
				_, _ = fmt.Fprintf(stdout, "disc %d %s: pass %d/%d\n", d.DiscSeq, d.Label, passNum, totalPasses)
			}
			batch := nextBudgetBatch(wanted, &idx, stagingBudget, spoolBytes)
			for _, o := range batch {
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
				spoolBytes += o.Bytes
				reportSpoolBytes(spoolBytes)
			}
			_, freed, err := m.WriteReady(spoolDir, prog)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
				return 2
			}
			spoolBytes -= freed
			reportSpoolBytes(spoolBytes)
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

	_, _ = fmt.Fprintf(stdout, "restored snapshot %s into %s\n", snapID.TextForm(), outDir)
	if m.Resumed() > 0 {
		_, _ = fmt.Fprintf(stdout, "resumed: %d file(s) already restored\n", m.Resumed())
	}
	if m.Skipped() > 0 {
		_, _ = fmt.Fprintf(stdout, "skipped %d existing path(s); pass --overwrite to replace them\n", m.Skipped())
		return 1
	}
	if err := removeEmptySpoolRoot(spoolRoot); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
	}
	return 0
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
// resolved every tree and blob from the cache. Each entry's Bytes is
// how many spool bytes reading it will cost, the figure the staging
// budget's pass split is measured against.
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

// nextBudgetBatch consumes wanted[*idx:] up to the point where adding
// one more object would push spoolBytes over budget, and returns that
// batch. A budget of 0 means unlimited: the whole remainder is one
// batch. It always takes at least one object, so a single object
// larger than what is left of the budget still makes progress rather
// than looping forever; FileExceedingBudget has already refused the
// one case that guarantees this can never fit, a file whose own total
// is above the whole budget.
func nextBudgetBatch(wanted []plan.ObjectEntry, idx *int, budget, spoolBytes uint64) []plan.ObjectEntry {
	start := *idx
	if budget == 0 {
		*idx = len(wanted)
		return wanted[start:]
	}
	cur := spoolBytes
	for *idx < len(wanted) {
		o := wanted[*idx]
		if *idx > start && cur+o.Bytes > budget {
			break
		}
		cur += o.Bytes
		*idx++
	}
	return wanted[start:*idx]
}

// reportSpoolBytes tells restoreSpoolBytesObserved, when a test has set
// it, the disc-swap loop's current running spool total.
func reportSpoolBytes(current uint64) {
	if restoreSpoolBytesObserved != nil {
		restoreSpoolBytesObserved(current)
	}
}

// resumeSpool marks every object already spooled from an earlier,
// interrupted run, and assembles any file that completes as a result.
// It returns how many spooled objects it found and their total bytes,
// so the disc-swap loop's running spool total starts accurate.
func resumeSpool(spoolDir string, m *restore.Manifest, prog *progress.Reporter) (count int, bytes uint64, err error) {
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		return 0, 0, err
	}
	if len(entries) == 0 {
		return 0, 0, nil
	}
	for _, e := range entries {
		id, err := object.ParseID(e.Name())
		if err != nil {
			continue
		}
		if fi, err := e.Info(); err == nil {
			bytes += uint64(fi.Size())
		}
		m.MarkSpooled(id)
	}
	_, freed, err := m.WriteReady(spoolDir, prog)
	if err != nil {
		return len(entries), bytes, err
	}
	bytes -= freed
	return len(entries), bytes, nil
}

// discSwapRetries is how many times detectDisc retries an unreadable
// DISC.bin before it prompts the operator, and the pause between tries.
const discSwapRetries = 3

var discSwapRetryPause = 300 * time.Millisecond

// detectDisc waits until the disc d names is in the drive at mountDir,
// OPERATIONS.md's "14.6 Disc detection": no prompt when the expected
// disc is already there, unless interactive is set; a prompt, and a
// mismatch report, otherwise.
// skipUUID, when haveSkipUUID is true, is a disc uuid the very first
// read must not report as a mismatch even though it is not d's own
// disc: with --no-eject, the disc still in the drive right after a
// read is the one the restore loop just finished, not a wrong disc the
// operator inserted, so it does not get the "expected ... found ..."
// line before the first prompt for d. A later, still-wrong read, once
// the operator has already been prompted once, is reported normally.
func detectDisc(mountDir string, c *cache.Cache, d plan.DiscEntry, interactive bool, scanner *bufio.Scanner, stdout, stderr io.Writer, skipUUID [16]byte, haveSkipUUID bool) error {
	unreadable := 0
	// promptedOnce becomes true after the first prompt this call has
	// shown, so --interactive prompts exactly once per disc rather than
	// forever: a mismatch or an unreadable disc always prompts anyway,
	// and once it has, a later matching read is accepted straight away.
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
			if !interactive || promptedOnce {
				_, _ = fmt.Fprintf(stdout, "disc %d %s: found\n", d.DiscSeq, d.Label)
				return nil
			}
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
