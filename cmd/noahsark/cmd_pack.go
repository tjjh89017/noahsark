package main

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	register(&command{
		name:    "pack",
		usage:   "pack --capacity=SIZE [--close] [--out=DIR] [--dry-run]\npack --undo DISC",
		summary: "Pack staged items onto the next disc, or undo the pack of the newest disc.",
		flags:   packFlags,
	})
}

// packOptions holds the command options of pack.
type packOptions struct {
	capacity  string
	outDir    string
	closeDisc bool
	dryRun    bool
	undo      bool
}

func packFlags(fs *flag.FlagSet) runFunc {
	o := &packOptions{}
	fs.StringVar(&o.capacity, "capacity", "", "target capacity ("+capacityHelpText()+"); required")
	fs.StringVar(&o.outDir, "out", "", "the directory that receives the disc root; it must be empty or absent")
	fs.BoolVar(&o.closeDisc, "close", false, "make status print the sealing burn line for this disc")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the discs that the staged data needs at this capacity, and stop")
	fs.BoolVar(&o.undo, "undo", false, "return the items of the newest disc to staged, while that disc is packed")
	return o.run
}

// packUsage is the usage line of a pack that takes staged items.
const packUsage = "usage: noahsark pack --capacity=SIZE [--close] [--out=DIR] [--dry-run]"

// packNothingStaged is the line of a pack that finds no staged item.
const packNothingStaged = "pack: nothing staged"

// run implements "noahsark pack". pack takes every pending ref; there
// is no way to name a snapshot explicitly. See docs/decisions.md, "Pack".
func (o *packOptions) run(e *env, args []string) int {
	if o.undo {
		return o.runUndo(e, args)
	}
	stdout, stderr := e.stdout, e.stderr
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, packUsage)
		return 2
	}

	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return configExitCode(err)
	}
	if o.capacity == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack needs --capacity; "+capacityHint())
		return 2
	}
	capacitySectors, err := parseCapacity(o.capacity)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}
	layout := layoutOf(repoDir, cfg)
	absOut := ""
	if o.outDir != "" && !o.dryRun {
		if absOut, err = e.abs(o.outDir); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 1
		}
		if code := checkPackOut(e, absOut, layout, stderr); code != 0 {
			return code
		}
	}

	// A dry run takes no lock, as a command that only reads.
	if !o.dryRun {
		lk, code, ok := lockRepo("pack", repoDir, stderr)
		if !ok {
			return code
		}
		defer releaseLock(lk)
	}

	now := e.now()
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	c, err := catalog.Open(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	snapshots, err := addPendingRefs(layout, repoUUID, nil, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	// When no ref moved since the last pack, the run still carries the
	// newest ref of the repository.
	if len(snapshots) == 0 {
		if newest := newestRef(c, allRepoRefs(layout)); newest != nil {
			newest.Time = now
			snapshots = append(snapshots, *newest)
		}
	}

	logs, err := openLogs("pack", layout, !o.dryRun, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	if refuseWhileMissing("pack", layout, cfg, logs.Discs, stderr) {
		return 1
	}
	if !o.dryRun {
		if err := finishUndonePacks(layout, c, repoUUID, logs, stderr); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 1
		}
	}
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	if !o.dryRun {
		if err := finishInterruptedPacks(layout, c, logs, ledger.Rows, stderr); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 1
		}
	}
	runSeq, discSeq := nextPackSeqNumbers(ledger.Rows, logs.Discs)

	labelName := newestRefName(c, allRepoRefs(layout))
	labelFor := func(seq uint64) string { return discLabel(labelName, seq) }

	opts := image.PackOptions{
		Snapshots:             snapshots,
		TargetCapacitySectors: capacitySectors,
		RepoUUID:              repoUUID,
		MinRunSeq:             runSeq,
		MinDiscSeq:            discSeq,
		Now:                   func() time.Time { return now },
	}
	// A warning about an item that pack cannot take makes the exit code
	// 1, also after a good pack.
	warned := false
	opts.Unreadable = func(item image.UnreadableItem) {
		_, _ = fmt.Fprintln(stderr, unreadableLine("pack", item, false))
		warned = true
	}
	if o.dryRun {
		code := runPackDryRun(stdout, stderr, layout, c, logs.Items, opts, o.capacity, labelFor)
		if code == 0 && warned {
			return 1
		}
		return code
	}
	stageLog := logs.Items

	var discUUID [16]byte
	if _, err := rand.Read(discUUID[:]); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	label := labelFor(discSeq)
	root, err := makePackRoot(layout, discUUID, absOut, o.closeDisc)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	opts.Store = packStore(layout, c, stageLog)
	opts.StageLog = stageLog
	opts.OutputDir = root
	opts.DiscUUID = discUUID
	opts.Label = label
	opts.Progress = e.progress()
	opts.WriteCatalog = func() error {
		_, err := catalog.WriteTablesFromRoot(c, root)
		return err
	}
	result, err := image.Pack(opts)
	if err != nil {
		// Pack removes the part-written disc root when it stops before
		// the ledger row. The plan directory and the catalog tables of
		// this disc go too; an --out directory stays. After the ledger
		// row, the disc root stays, and the next pack finishes the
		// records of the disc.
		if !ledgerNames(layout, repoUUID, discUUID) {
			_ = os.RemoveAll(layout.planDir(discUUID))
			_ = c.RemoveDisc(discUUID)
		}
		if errors.Is(err, image.ErrNothingToPack) {
			if warned {
				return 1
			}
			_, _ = fmt.Fprintln(stdout, packNothingStaged)
			_, _ = fmt.Fprintln(stdout, nextStatusLine)
			return 0
		}
		return packFailed(stderr, o.capacity, capacitySectors, err)
	}

	// The Packed event is the last durable write of a pack. A pack that
	// stops before it leaves a ledger row with no event, and the next
	// pack finishes the records of that disc.
	if err := logs.Discs.Append(packedEvent(now, discUUID, result, o.closeDisc)); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "packed disc %d %q: %d item(s), %d bytes\n",
		result.DiscSeq, label, result.ObjectCount, result.ObjectBytes)
	_, _ = fmt.Fprintf(stdout, "uuid: %s\n", uuidText(discUUID))
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	if warned {
		return 1
	}
	return 0
}

// packedEvent is the Packed event of the disc that result describes.
func packedEvent(now time.Time, discUUID [16]byte, result *image.PackResult, closeDisc bool) stage.DiscRecord {
	var flags stage.DiscFlags
	if closeDisc {
		flags |= stage.FlagClose
	}
	return stage.DiscRecord{
		TimeSec:  now.Unix(),
		DiscUUID: discUUID,
		Event:    stage.EventPacked,
		Flags:    flags,
		DiscSeq:  result.DiscSeq,
		RunSeq:   result.RunSeq,
	}
}

// nextPackSeqNumbers returns the run_seq and disc_seq of the next disc:
// one more than the highest number over the rows of the disc ledger and
// over all Packed events, undone packs included. A fresh repository
// starts at run_seq 1 and disc_seq 0.
func nextPackSeqNumbers(rows []format.DiscsRow, discLog *stage.DiscLog) (runSeq, discSeq uint64) {
	runSeq, discSeq = image.NextSeqNumbers(rows)
	// A Packed event always has a run_seq of 1 or more. Thus a highest
	// run_seq of 0 means that the log has no Packed event.
	if highDisc, highRun := discLog.HighestPacked(); highRun > 0 {
		runSeq = max(runSeq, highRun+1)
		discSeq = max(discSeq, highDisc+1)
	}
	return runSeq, discSeq
}

// discLabel is the label of disc seq: the name of the newest ref, then
// " disc SEQ". With no ref, it is "disc SEQ".
func discLabel(newestRef string, seq uint64) string {
	if newestRef == "" {
		return fmt.Sprintf("disc %d", seq)
	}
	return fmt.Sprintf("%s disc %d", newestRef, seq)
}

// checkPackOut refuses an --out path inside the repository or the
// staging store, a path that holds files, and a path that is not a
// directory, with exit code 2. It returns 0 for an empty or absent
// directory outside them. The check resolves the symlinks of the part of
// each path that exists.
func checkPackOut(e *env, absOut string, layout repoLayout, stderr io.Writer) int {
	out, err := resolveExisting(e, absOut)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	for _, dir := range []string{layout.repo, layout.stagingDir()} {
		inside, err := resolveExisting(e, dir)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 1
		}
		if isWithin(out, inside) {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: --out=%s is inside the repository or the staging store; give a directory outside them\n", absOut)
			return 2
		}
	}
	fi, err := os.Stat(absOut)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	if !fi.IsDir() {
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: --out=%s is not a directory\n", absOut)
		return 2
	}
	entries, err := os.ReadDir(absOut)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	if len(entries) > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: --out=%s holds files; give an empty or absent directory\n", absOut)
		return 2
	}
	return 0
}

// resolveExisting makes path absolute and resolves the symlinks of its
// longest part that exists. The rest of the path follows as it is.
func resolveExisting(e *env, path string) (string, error) {
	if !filepath.IsAbs(path) {
		wd, err := e.getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(wd, path)
	}
	path = filepath.Clean(path)
	rest := ""
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Join(path, rest), nil
		}
		rest = filepath.Join(filepath.Base(path), rest)
		path = parent
	}
}

// makePackRoot prepares the disc root of the disc discUUID and returns
// its path. With no absOut, the disc root is the tree directory of the
// plan directory. With absOut, the tree is a symlink to absOut, and the
// disc root is absOut. With closeDisc, the plan directory holds the file
// planCloseName, so that the records of a pack that stops after its
// ledger row still get the close flag.
func makePackRoot(layout repoLayout, discUUID [16]byte, absOut string, closeDisc bool) (string, error) {
	planDir := layout.planDir(discUUID)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return "", err
	}
	if closeDisc {
		if err := writeSyncedFile(filepath.Join(planDir, planCloseName)); err != nil {
			return "", err
		}
	}
	tree := layout.planTree(discUUID)
	root := tree
	if absOut == "" {
		if err := os.Mkdir(tree, 0o755); err != nil {
			return "", err
		}
	} else {
		if err := os.Symlink(absOut, tree); err != nil {
			return "", err
		}
		root = absOut
	}
	if err := syncDir(planDir); err != nil {
		return "", err
	}
	return root, syncDir(filepath.Dir(planDir))
}

// planCloseName is the file of a plan directory that marks a disc
// packed with --close.
const planCloseName = "close"

// writeSyncedFile creates the empty file path and syncs it.
func writeSyncedFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// syncDir flushes the entries of the directory dir to stable storage.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	return errors.Join(syncErr, closeErr)
}

// packFailed prints the refusal or the error of a failed pack and
// returns its exit code.
func packFailed(stderr io.Writer, capacity string, capacitySectors uint64, err error) int {
	if tooSmall, ok := errors.AsType[*image.ErrCapacityTooSmall](err); ok {
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: capacity %s (%d bytes) holds not one item; %s\n",
			capacity, capacitySectors*image.SectorSize, smallestItemText(tooSmall))
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: use a capacity of %d bytes or more\n", tooSmall.NeededSectors*image.SectorSize)
		return 2
	}
	_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
	return 1
}

// runPackDryRun implements "pack --dry-run". It prints the discs that
// the staged data needs, and writes nothing: no disc root, no record,
// no catalog entry and no ledger row. It uses no sequence number.
func runPackDryRun(stdout, stderr io.Writer, layout repoLayout, c *catalog.Catalog, stageLog *stage.Log, opts image.PackOptions, capacity string, labelFor func(uint64) string) int {
	opts.Store = packStore(layout, c, stageLog)
	opts.StageLog = stageLog

	discs, err := image.DryRun(opts, labelFor)
	if errors.Is(err, image.ErrNothingToPack) || (err == nil && len(discs) == 0) {
		_, _ = fmt.Fprintln(stdout, packNothingStaged)
		return 0
	}
	printDryRunDiscs(stdout, discs)
	if err != nil {
		return packFailed(stderr, capacity, opts.TargetCapacitySectors, err)
	}
	return 0
}

// printDryRunDiscs prints one line for each predicted disc, then the
// total. The total uses a fixed plural form, for a program to parse.
func printDryRunDiscs(stdout io.Writer, discs []image.DryRunDisc) {
	var totalItems int
	var totalBytes uint64
	for _, d := range discs {
		_, _ = fmt.Fprintf(stdout, "disc %d: %d items, %d bytes\n", d.DiscSeq, d.ObjectCount, d.ObjectBytes)
		totalItems += d.ObjectCount
		totalBytes += d.ObjectBytes
	}
	_, _ = fmt.Fprintf(stdout, "total: %d discs, %d items, %d bytes\n", len(discs), totalItems, totalBytes)
}

// smallestItemText names the smallest staged item of a refused
// capacity, the one item the capacity must first grow to hold.
func smallestItemText(e *image.ErrCapacityTooSmall) string {
	return fmt.Sprintf("the smallest staged item is %s %s, %d bytes",
		kindWord(e.SmallestKind), e.SmallestID.TextForm(), e.SmallestBytes)
}

// kindWord renders an object kind as the word an operator message uses.
func kindWord(kind format.ObjectKind) string {
	switch kind {
	case format.ObjectKindChunk:
		return "chunk"
	case format.ObjectKindBlob:
		return "blob"
	case format.ObjectKindTree:
		return "tree"
	case format.ObjectKindSnapshot:
		return "snapshot"
	default:
		return "item"
	}
}

// newestRef returns the ref whose snapshot was committed last, by the
// same rule newestRefName applies, or nil when snapshots is empty.
func newestRef(c *catalog.Catalog, snapshots []image.SnapshotRef) *image.SnapshotRef {
	name := newestRefName(c, snapshots)
	if name == "" {
		return nil
	}
	for i := range snapshots {
		if snapshots[i].Name == name {
			return &snapshots[i]
		}
	}
	return nil
}

// newestRefName returns the name of the ref whose snapshot was
// committed last. A tie goes to the name that sorts last, so a set of
// date names committed in the same second still gives the latest date.
// A snapshot that the catalog does not hold counts as the oldest, thus
// a name is still returned while any ref is given.
func newestRefName(c *catalog.Catalog, snapshots []image.SnapshotRef) string {
	name := ""
	var newest int64
	for _, s := range snapshots {
		var sec int64
		if snap, err := c.ReadSnapshot(s.ID); err == nil {
			sec = snap.TimeSec
		}
		if name == "" || sec > newest || (sec == newest && s.Name > name) {
			name, newest = s.Name, sec
		}
	}
	return name
}

// allRepoRefs reads every ref of the repository. An unreadable ref file
// gives no ref, and the label then is the disc number alone.
func allRepoRefs(layout repoLayout) []image.SnapshotRef {
	refs, err := readRefs(layout.refsFile())
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]image.SnapshotRef, 0, len(names))
	for _, n := range names {
		id, err := parseSnapshotID(refs[n])
		if err != nil {
			continue
		}
		out = append(out, image.SnapshotRef{Name: n, ID: id})
	}
	return out
}

// stringList implements flag.Value for a repeatable flag.
type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// addPendingRefs implements OPERATIONS.md's rule that pack carries
// forward every local ref record whose run_seq is still 0: a ref that
// commit moved since the last pack. named holds the refs already
// selected by --ref or --snapshot; addPendingRefs appends every other
// ref name whose current snapshot the refs ledger does not already
// carry under that name, so a pack picks up every pending ref, not
// only the one named on its command line.
func addPendingRefs(layout repoLayout, repoUUID [16]byte, named []image.SnapshotRef, now time.Time) ([]image.SnapshotRef, error) {
	allRefs, err := readRefs(layout.refsFile())
	if err != nil {
		return named, err
	}
	ledger, err := image.LoadRefsLedger(layout.refsLedgerFile(), repoUUID)
	if err != nil {
		return named, err
	}
	carried := make(map[string]object.ID, len(ledger.Records))
	for _, rec := range ledger.Records {
		carried[string(rec.Name[:rec.NameLen])] = object.ID(rec.SnapshotID)
	}
	haveName := make(map[string]bool, len(named))
	for _, s := range named {
		haveName[s.Name] = true
	}

	names := make([]string, 0, len(allRefs))
	for n := range allRefs {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		if haveName[n] {
			continue
		}
		id, err := parseSnapshotID(allRefs[n])
		if err != nil {
			return named, fmt.Errorf("ref %q: %w", n, err)
		}
		if cur, ok := carried[n]; ok && cur == id {
			continue // already carried forward with its own run_seq
		}
		named = append(named, image.SnapshotRef{Name: n, ID: id, Time: now})
		haveName[n] = true
	}
	return named, nil
}

// packStore gives pack the paths of the repository. Chunk objects come
// from staging, and snapshot, tree and blob objects from the catalog c.
// The snapshots are the snapshots of the catalog that the state log l
// knows: a staged snapshot is packed, and a snapshot that a disc holds
// is carried, as every run carries every snapshot of the repository.
func packStore(layout repoLayout, c *catalog.Catalog, l *stage.Log) image.Store {
	return image.Store{
		ObjectPath: layout.objectPath(c),
		SnapshotIDs: func() ([]object.ID, error) {
			all, err := c.ListSnapshots()
			if err != nil {
				return nil, err
			}
			known := make([]object.ID, 0, len(all))
			for _, id := range all {
				if _, ok := l.Get(id); ok {
					known = append(known, id)
				}
			}
			return known, nil
		},
		DiscsLedger: layout.discsLedgerFile(),
		RefsLedger:  layout.refsLedgerFile(),
	}
}

// refuseWhileMissing refuses the command cmd while a disc is missing. It
// prints `disc SEQ "LABEL" is missing` for each missing disc, in the
// order of the disc number, and reports true. The number and the label
// come from the disc ledger.
func refuseWhileMissing(cmd string, layout repoLayout, cfg repoConfig, discs *stage.DiscLog, stderr io.Writer) bool {
	missing := discs.InState(stage.DiscMissing)
	if len(missing) == 0 {
		return false
	}
	var rows []format.DiscsRow
	if repoUUID, err := decodeUUID(cfg.RepoUUID); err == nil {
		if ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID); err == nil {
			rows = ledger.Rows
		}
	}
	for _, row := range missingRows(missing, rows) {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s is missing\n", cmd, discNameShort(row.DiscSeq, labelText(row.Label[:row.LabelLen])))
	}
	return true
}

// ledgerNames reports whether the disc ledger holds a row of the disc
// discUUID.
func ledgerNames(layout repoLayout, repoUUID, discUUID [16]byte) bool {
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(ledger.Rows, func(r format.DiscsRow) bool { return r.DiscUUID == discUUID })
}

// finishInterruptedPacks repairs what a pack that stopped leaves. pack
// writes, in this order, the synced disc root, the catalog tables, the
// disc ledger row, the item records and the Packed event.
//
// A disc root and catalog tables with no ledger row and no event belong
// to a pack that stopped before its ledger row: no record names that
// disc, and the items stay Staged. finishInterruptedPacks removes the
// plan directory and the catalog tables of such a disc.
//
// Catalog tables with no record belong to a pack or a recover that
// stopped before its ledger row. finishInterruptedPacks removes them too.
//
// A ledger row with a disc root and no event belongs to a pack that
// stopped after its ledger row. finishInterruptedPacks records the
// items that the catalog INDEX of the disc lists and that are still
// Staged as Packed, then appends the Packed event. The event carries the
// close flag when the plan directory holds planCloseName.
func finishInterruptedPacks(layout repoLayout, c *catalog.Catalog, logs *stage.Logs, rows []format.DiscsRow, stderr io.Writer) error {
	inLedger := make(map[[16]byte]format.DiscsRow, len(rows))
	for _, r := range rows {
		inLedger[r.DiscUUID] = r
	}
	known := func(u [16]byte) bool {
		_, ok := logs.Discs.Disc(u)
		_, row := inLedger[u]
		return ok || row
	}
	for _, dir := range []string{filepath.Join(c.Dir(), "discs"), layout.plansDir()} {
		for _, u := range uuidDirs(dir) {
			if known(u) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(dir, uuidText(u))); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: removed %s: no record names disc %s; an earlier pack or recover stopped before it recorded the disc\n", filepath.Join(dir, uuidText(u)), uuidText(u))
		}
	}

	for _, row := range slices.SortedFunc(maps.Values(inLedger), func(a, b format.DiscsRow) int { return cmp.Compare(a.DiscSeq, b.DiscSeq) }) {
		if _, ok := logs.Discs.Disc(row.DiscUUID); ok {
			continue
		}
		if _, err := os.Lstat(layout.planTree(row.DiscUUID)); err != nil {
			continue
		}
		idx, err := c.IndexForDisc(row.DiscUUID)
		if err != nil {
			return fmt.Errorf("disc %d: an earlier pack stopped, and the catalog holds no INDEX of the disc: %w", row.DiscSeq, err)
		}
		var ids []object.ID
		for _, o := range idx.Objects {
			id := object.ID(o.ContentID)
			if rec, ok := logs.Items.Get(id); !ok || rec.State == stage.Staged {
				ids = append(ids, id)
			}
		}
		if err := logs.Items.EnsureStaged(ids...); err != nil {
			return err
		}
		if err := logs.Items.MarkPacked(row.RunSeq, row.DiscUUID, ids...); err != nil {
			return err
		}
		var flags stage.DiscFlags
		if _, err := os.Lstat(filepath.Join(layout.planDir(row.DiscUUID), planCloseName)); err == nil {
			flags |= stage.FlagClose
		}
		event := stage.DiscRecord{TimeSec: row.CreatedSec, DiscUUID: row.DiscUUID, Event: stage.EventPacked, Flags: flags, DiscSeq: row.DiscSeq, RunSeq: row.RunSeq}
		if err := logs.Discs.Append(event); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: %s: an earlier pack stopped before it recorded the disc; its records are now complete\n",
			discNameShort(row.DiscSeq, labelText(row.Label[:row.LabelLen])))
	}
	return nil
}

// uuidDirs returns the uuid of each entry of dir whose name is the text
// form of a uuid. A missing dir gives none.
func uuidDirs(dir string) [][16]byte {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out [][16]byte
	for _, ent := range entries {
		raw, err := hex.DecodeString(strings.ReplaceAll(ent.Name(), "-", ""))
		if err != nil || len(raw) != 16 || uuidText([16]byte(raw)) != ent.Name() {
			continue
		}
		out = append(out, [16]byte(raw))
	}
	return out
}

func decodeUUID(s string) ([16]byte, error) {
	var out [16]byte
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("repo.uuid: %w", err)
	}
	if len(raw) != 16 {
		return out, fmt.Errorf("repo.uuid: expected 16 bytes, got %d", len(raw))
	}
	copy(out[:], raw)
	return out, nil
}
