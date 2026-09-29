package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// gcRemove unlinks one staged file. Tests replace it to fail the unlink
// after the durable record, and so to check that the next gc run frees
// the orphan the crash left behind.
var gcRemove = os.Remove

func init() {
	register(&command{
		name:    "gc",
		usage:   "gc [--dry-run] [--force-after=DURATION]",
		summary: "Free the staged files of verified discs after 7 days.",
		flags:   gcFlags,
	})
}

// gcOptions holds the command options of gc.
type gcOptions struct {
	dryRun     bool
	forceAfter string
}

func gcFlags(fs *flag.FlagSet) runFunc {
	o := &gcOptions{}
	fs.BoolVar(&o.dryRun, "dry-run", false, "print what would be freed, and free nothing")
	fs.StringVar(&o.forceAfter, "force-after", "", "shorten the 7-day wait to this duration for this run only")
	return o.run
}

// run implements "noahsark gc". It frees the chunk files and the plan
// directory of a verified disc after the wait since its verified time,
// and the chunk file of an item that is already OnDisc. It never
// removes a file of the catalog or of the state directory. See
// OPERATIONS.md, "GC rules".
func (o *gcOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "gc"
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark gc [--dry-run] [--force-after=DURATION]")
		return 2
	}
	wait := retainAfterClean
	if o.forceAfter != "" {
		d, err := parseRetentionDuration(o.forceAfter)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: gc: --force-after:", err)
			return 2
		}
		wait = d
	}

	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 2
	}

	lk, code, ok := lockRepo(cmd, repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	layout := layoutOf(repoDir, cfg)
	logs, err := openLogs(cmd, layout, true, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	c, err := catalog.Open(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}

	now := e.now()
	plan := planGC(logs, c, layout, wait, now)
	var failures []gcFailure
	if !o.dryRun {
		if err := recordGC(logs, plan, now); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
			return 1
		}
	}
	objDeleted, objBytes, objFailures := gcApplyStagingObjects(plan.files(), o.dryRun)
	dirDeleted, dirBytes, dirFailures := gcApplyPlanDirs(plan.planDirs, o.dryRun)
	failures = append(append(failures, objFailures...), dirFailures...)

	verb := "deleted"
	if o.dryRun {
		verb = "would delete"
	}
	_, _ = fmt.Fprintf(stdout, "gc: staging: %s %d staged object(s), %d bytes\n", verb, objDeleted, objBytes)
	_, _ = fmt.Fprintf(stdout, "gc: plans: %s %d disc plan directory(ies), %d bytes\n", verb, dirDeleted, dirBytes)
	if o.dryRun {
		names := discNamesFromLedger(layout.discsLedgerFile(), repoUUID)
		printDryRunGroupSummary(plan.files(), names, stdout)
		for _, d := range plan.planDirs {
			_, _ = fmt.Fprintf(stdout, "would delete: %s: plan directory %s, %d bytes\n", discNameOf(names, d.discUUID), d.path, d.bytes)
		}
	}
	if plan.noIndex > 0 {
		_, _ = fmt.Fprintf(stdout, "gc: %d object(s) skipped: their disc's INDEX is not in the catalog\n", plan.noIndex)
	}
	if plan.unlisted > 0 {
		_, _ = fmt.Fprintf(stdout, "gc: %d object(s) skipped: their disc's INDEX does not list them\n", plan.unlisted)
	}

	// A staged file gc could not unlink is a failure at run time, named
	// by its path and the underlying error.
	for _, f := range failures {
		_, _ = fmt.Fprintf(stderr, "noahsark: gc: %s: %v\n", f.path, f.err)
	}
	if len(failures) > 0 {
		return 1
	}
	if objDeleted == 0 && dirDeleted == 0 && plan.noIndex == 0 && plan.unlisted == 0 {
		printNothingEligibleYet(stdout, plan)
	}
	return 0
}

// gcDiscPlan is one verified disc that gc frees: the Packed items of the
// disc, and the chunk files of those items.
type gcDiscPlan struct {
	uuid  [16]byte
	items []object.ID
	objs  []gcObj
}

// gcPlan is what one gc run frees.
type gcPlan struct {
	// discs are the verified discs whose wait is over and whose items
	// the catalog INDEX of the disc lists.
	discs []gcDiscPlan
	// orphans are the chunk files of items that are already OnDisc.
	orphans []gcObj
	// planDirs are the plan directories of the discs in discs and of
	// the on disc only discs.
	planDirs []gcPlanDir
	// noIndex counts the items of a ready disc whose INDEX is not in
	// the catalog. unlisted counts the items of a ready disc that its
	// INDEX does not list.
	noIndex, unlisted int
	// nextFree is the earliest time at which the wait of a verified
	// disc is over, when hasNext is true.
	nextFree time.Time
	hasNext  bool
}

// files returns every chunk file that the plan frees.
func (p *gcPlan) files() []gcObj {
	var out []gcObj
	for _, d := range p.discs {
		out = append(out, d.objs...)
	}
	return append(out, p.orphans...)
}

// planGC lists what gc frees at the time now, and changes nothing. A
// disc is ready when it is verified and wait has passed since its
// verified time. gc frees no item of a ready disc when the catalog does
// not hold the INDEX of the disc, or when that INDEX does not list every
// Packed item of the disc: Freed moves the whole disc.
func planGC(logs *stage.Logs, c *catalog.Catalog, layout repoLayout, wait time.Duration, now time.Time) *gcPlan {
	p := &gcPlan{}
	for _, d := range logs.Discs.InState(stage.DiscVerified) {
		freeAt := d.VerifiedTime.Add(wait)
		if now.Before(freeAt) {
			if !p.hasNext || freeAt.Before(p.nextFree) {
				p.nextFree, p.hasNext = freeAt, true
			}
			continue
		}
		items := logs.Items.ItemsOfDiscInState(d.UUID, stage.Packed)
		var objs []gcObj
		if len(items) > 0 {
			idx, err := c.IndexForDisc(d.UUID)
			if err != nil {
				p.noIndex += len(items)
				continue
			}
			var unlisted int
			objs, unlisted = gcPlanDisc(d.UUID, items, idx, layout)
			if unlisted > 0 {
				p.unlisted += unlisted
				continue
			}
		}
		p.discs = append(p.discs, gcDiscPlan{uuid: d.UUID, items: items, objs: objs})
		p.planDirs = appendPlanDir(p.planDirs, layout, d.UUID)
	}
	for _, d := range logs.Discs.InState(stage.DiscOnDiscOnly) {
		p.planDirs = appendPlanDir(p.planDirs, layout, d.UUID)
	}
	p.orphans = gcOrphans(logs.Items, layout)
	return p
}

// appendPlanDir adds the plan directory of the disc discUUID to dirs,
// when it exists.
func appendPlanDir(dirs []gcPlanDir, layout repoLayout, discUUID [16]byte) []gcPlanDir {
	path := layout.planDir(discUUID)
	bytes, ok := dirBytes(path)
	if !ok {
		return dirs
	}
	return append(dirs, gcPlanDir{discUUID: discUUID, path: path, bytes: bytes})
}

// gcPlanDisc confirms each item of the disc discUUID against idx, the
// catalog INDEX of that disc, and returns the chunk file of each chunk
// item. unlisted counts the items that idx does not list. The INDEX of
// the disc that the item record names is the only INDEX that counts: an
// INDEX of another disc with the same run_seq never stands in for it.
func gcPlanDisc(discUUID [16]byte, items []object.ID, idx *format.Index, layout repoLayout) (objs []gcObj, unlisted int) {
	for _, id := range items {
		row, byteLen, found := findObjectRow(idx, id)
		if !found {
			unlisted++
			continue
		}
		if row.Kind != format.ObjectKindChunk {
			continue
		}
		path := layout.chunkFile(id)
		size := byteLen
		if fi, err := os.Stat(path); err == nil {
			size = uint64(fi.Size())
		}
		objs = append(objs, gcObj{id: id, path: path, size: size, discUUID: discUUID})
	}
	return objs, unlisted
}

// recordGC writes the records of the discs of p: the OnDisc records of
// the items of each disc as one durable batch, then the Freed event of
// the disc. Both are synced before gc unlinks a chunk file. A crash
// after them leaves the chunk files as orphans, which the next gc frees.
func recordGC(logs *stage.Logs, p *gcPlan, now time.Time) error {
	for _, d := range p.discs {
		if err := logs.Items.MarkOnDisc(d.items...); err != nil {
			return err
		}
		if err := logs.Discs.Append(discEvent(now, d.uuid, stage.EventFreed)); err != nil {
			return err
		}
	}
	return nil
}

// printNothingEligibleYet prints the line of a gc that frees nothing. It
// names the earliest time at which the wait of a verified disc is over.
func printNothingEligibleYet(stdout io.Writer, p *gcPlan) {
	if !p.hasNext {
		_, _ = fmt.Fprintln(stdout, "gc: nothing is eligible yet")
		return
	}
	_, _ = fmt.Fprintf(stdout, "gc: nothing is eligible yet; earliest eligible date: %s\n", p.nextFree.Format(time.RFC3339))
}

// gcObj is one chunk file that gc frees: the item, the path of its file,
// the size to report, and the disc that holds the item.
type gcObj struct {
	id       object.ID
	path     string
	size     uint64
	discUUID [16]byte
}

// gcOrphans lists every OnDisc chunk whose chunk file is still on the
// disk. gc writes the OnDisc record, syncs it, and only then unlinks the
// file, so a crash between the two leaves exactly this. The durable
// record already proves that a disc holds the item, thus the next gc
// run frees the file with no further check.
func gcOrphans(l *stage.Log, layout repoLayout) []gcObj {
	var objs []gcObj
	for _, id := range l.IDsInState(stage.OnDisc) {
		rec, _ := l.Get(id)
		path := layout.chunkFile(id)
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		objs = append(objs, gcObj{id: id, path: path, size: uint64(fi.Size()), discUUID: rec.DiscUUID})
	}
	slices.SortFunc(objs, func(a, b gcObj) int { return strings.Compare(a.path, b.path) })
	return objs
}

// gcFailure names one staging object gc could not free, and why, so the
// operator sees a reason instead of a bare zero count.
type gcFailure struct {
	path string
	err  error
}

// gcApplyStagingObjects unlinks (or, under dryRun, only counts) every
// file of objs. It reports every file it could not unlink. A file that
// is already gone frees nothing and is not a failure.
func gcApplyStagingObjects(objs []gcObj, dryRun bool) (deleted int, bytesFreed uint64, failures []gcFailure) {
	for _, o := range objs {
		if dryRun {
			deleted++
			bytesFreed += o.size
			continue
		}
		removeErr := gcRemove(o.path)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			failures = append(failures, gcFailure{path: o.path, err: removeErr})
			continue
		}
		if removeErr == nil {
			deleted++
			bytesFreed += o.size
		}
	}
	return deleted, bytesFreed, failures
}

// printDryRunGroupSummary prints one line for each disc that objs
// groups by, each with the file count and the bytes of that disc, in
// uuid text order. It is the report of gc --dry-run.
func printDryRunGroupSummary(objs []gcObj, names map[[16]byte]string, stdout io.Writer) {
	type group struct {
		objects int
		bytes   uint64
	}
	byDisc := make(map[[16]byte]*group)
	for _, o := range objs {
		g, ok := byDisc[o.discUUID]
		if !ok {
			g = &group{}
			byDisc[o.discUUID] = g
		}
		g.objects++
		g.bytes += o.size
	}
	discs := slices.SortedFunc(maps.Keys(byDisc), func(a, b [16]byte) int { return strings.Compare(uuidText(a), uuidText(b)) })
	for _, u := range discs {
		g := byDisc[u]
		_, _ = fmt.Fprintf(stdout, "would delete: %s: %d object(s), %d bytes\n", discNameOf(names, u), g.objects, g.bytes)
	}
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
func discNamesFromLedger(ledgerPath string, repoUUID [16]byte) map[[16]byte]string {
	names := make(map[[16]byte]string)
	ledger, err := image.LoadDiscsLedger(ledgerPath, repoUUID)
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

// gcPlanDir is one disc's plan directory: the disc root that pack wrote
// by default, and the image that image build put beside it. For a pack
// --out disc, it holds the symlink to the disc root, and gc removes the
// symlink only.
type gcPlanDir struct {
	discUUID [16]byte
	path     string
	bytes    uint64
}

// dirBytes sums the size of every regular file below path. It does not
// follow a symlink. It reports ok false when path does not exist.
func dirBytes(path string) (uint64, bool) {
	if _, err := os.Lstat(path); err != nil {
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
// directory of dirs.
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
