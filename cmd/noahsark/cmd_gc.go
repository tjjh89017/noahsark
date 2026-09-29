package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
// removes a file of the catalog or of the state directory. A dry run
// takes no lock and writes no file. docs/states.md, rows 52 to 56, gives
// the lines.
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
		return configExitCode(err)
	}

	if !o.dryRun {
		lk, code, ok := lockRepo(cmd, repoDir, stderr)
		if !ok {
			return code
		}
		defer releaseLock(lk)
	}

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	layout := layoutOf(repoDir, cfg)
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	logs, err := openLogs(cmd, layout, !o.dryRun, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	indexOf, err := gcIndexReader(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}

	now := e.now()
	plan := planGC(logs, indexOf, layout, gcDiscSeqs(ledger.Rows), wait, now)
	if o.dryRun {
		printGCPlan(stdout, plan, "would free", plan.itemCount(), plan.byteCount())
		if len(plan.skipped) > 0 {
			return 1
		}
		return 0
	}

	if err := recordGC(logs, plan, now); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: gc:", err)
		return 1
	}
	items, bytes, failures := applyGC(plan)
	printGCPlan(stdout, plan, "freed", items, bytes)
	for _, f := range failures {
		_, _ = fmt.Fprintf(stderr, "noahsark: gc: %s: %v\n", f.path, f.err)
	}
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	if len(failures) > 0 || len(plan.skipped) > 0 {
		return 1
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

// gcHeld is one disc whose Packed items gc keeps: a verified disc whose
// wait is not over, or a packed or burned disc.
type gcHeld struct {
	seq   uint64
	items int
	// verified is true for a verified disc. until is then the end of its
	// wait.
	verified bool
	until    time.Time
}

// gcSkip is one verified disc whose wait is over and whose items gc
// cannot confirm against the catalog INDEX of the disc.
type gcSkip struct {
	seq   uint64
	items int
	// noIndex is true when the catalog holds no INDEX of the disc. It is
	// false when the INDEX does not list items items of the disc.
	noIndex bool
}

// gcPlan is what one gc run frees, holds and skips.
type gcPlan struct {
	// discs are the verified discs whose wait is over and whose items
	// the catalog INDEX of the disc lists.
	discs []gcDiscPlan
	// orphans are the chunk files of items that are already OnDisc.
	orphans []gcObj
	// planDirs are the plan directories of the discs in discs and of
	// the on disc only discs.
	planDirs []gcPlanDir
	held     []gcHeld
	skipped  []gcSkip
}

// itemCount is the number of items that the plan frees: the items that
// it records OnDisc, and the orphans.
func (p *gcPlan) itemCount() int {
	n := len(p.orphans)
	for _, d := range p.discs {
		n += len(d.items)
	}
	return n
}

// byteCount is the number of bytes that the plan frees: the disk space
// of the chunk files and of the plan directories.
func (p *gcPlan) byteCount() uint64 {
	var n uint64
	for _, o := range p.files() {
		n += o.size
	}
	for _, d := range p.planDirs {
		n += d.bytes
	}
	return n
}

// files returns every chunk file that the plan frees.
func (p *gcPlan) files() []gcObj {
	var out []gcObj
	for _, d := range p.discs {
		out = append(out, d.objs...)
	}
	return append(out, p.orphans...)
}

// gcIndexFunc returns the catalog INDEX of one disc.
type gcIndexFunc func(discUUID [16]byte) (*format.Index, error)

// errNoCatalog is the error of gcIndexFunc when the repository has no
// catalog directory.
var errNoCatalog = errors.New("the repository has no catalog directory")

// gcIndexReader returns the INDEX reader of the catalog of repoDir. It
// does not create the catalog directory: gc writes no file of the
// catalog.
func gcIndexReader(repoDir string) (gcIndexFunc, error) {
	if _, err := os.Stat(catalog.Dir(repoDir)); errors.Is(err, fs.ErrNotExist) {
		return func([16]byte) (*format.Index, error) { return nil, errNoCatalog }, nil
	}
	c, err := catalog.Open(repoDir)
	if err != nil {
		return nil, err
	}
	return c.IndexForDisc, nil
}

// gcDiscSeqs maps each disc of the disc ledger to its disc number.
func gcDiscSeqs(rows []format.DiscsRow) map[[16]byte]uint64 {
	seqs := make(map[[16]byte]uint64, len(rows))
	for _, r := range rows {
		seqs[r.DiscUUID] = r.DiscSeq
	}
	return seqs
}

// planGC lists what gc frees, holds and skips at the time now, and
// changes nothing. A verified disc is ready when wait has passed since
// its verified time. gc frees no item of a ready disc when the catalog
// does not hold the INDEX of the disc, or when that INDEX does not list
// every Packed item of the disc: Freed moves the whole disc.
func planGC(logs *stage.Logs, indexOf gcIndexFunc, layout repoLayout, seqs map[[16]byte]uint64, wait time.Duration, now time.Time) *gcPlan {
	p := &gcPlan{}
	for _, d := range logs.Discs.Discs() {
		seq, ok := seqs[d.UUID]
		if !ok {
			seq = d.DiscSeq
		}
		switch d.State {
		case stage.DiscPacked, stage.DiscBurned:
			if items := logs.Items.ItemsOfDiscInState(d.UUID, stage.Packed); len(items) > 0 {
				p.held = append(p.held, gcHeld{seq: seq, items: len(items)})
			}
		case stage.DiscVerified:
			p.planVerified(logs.Items, indexOf, layout, d, seq, wait, now)
		case stage.DiscOnDiscOnly:
			p.planDirs = appendPlanDir(p.planDirs, layout, d.UUID)
		}
	}
	p.orphans = gcOrphans(logs.Items, layout)
	return p
}

// planVerified adds the verified disc d to p: to the held discs while
// its wait lasts, to the skipped discs when its catalog INDEX does not
// confirm its items, and else to the freed discs.
func (p *gcPlan) planVerified(items *stage.Log, indexOf gcIndexFunc, layout repoLayout, d stage.DiscInfo, seq uint64, wait time.Duration, now time.Time) {
	packed := items.ItemsOfDiscInState(d.UUID, stage.Packed)
	freeAt := d.VerifiedTime.Add(wait)
	if now.Before(freeAt) {
		if len(packed) > 0 {
			p.held = append(p.held, gcHeld{seq: seq, items: len(packed), verified: true, until: freeAt})
		}
		return
	}
	var objs []gcObj
	if len(packed) > 0 {
		idx, err := indexOf(d.UUID)
		if err != nil {
			p.skipped = append(p.skipped, gcSkip{seq: seq, items: len(packed), noIndex: true})
			return
		}
		var unlisted int
		objs, unlisted = gcPlanDisc(d.UUID, packed, idx, layout)
		if unlisted > 0 {
			p.skipped = append(p.skipped, gcSkip{seq: seq, items: unlisted})
			return
		}
	}
	p.discs = append(p.discs, gcDiscPlan{uuid: d.UUID, items: packed, objs: objs})
	p.planDirs = appendPlanDir(p.planDirs, layout, d.UUID)
}

// printGCPlan prints the lines of gc: the freed line with verb, items
// and bytes, then one line for each held disc, then one line for each
// skipped disc.
func printGCPlan(stdout io.Writer, p *gcPlan, verb string, items int, bytes uint64) {
	_, _ = fmt.Fprintf(stdout, "gc: %s %d item(s), %d bytes\n", verb, items, bytes)
	for _, h := range p.held {
		if h.verified {
			_, _ = fmt.Fprintf(stdout, "gc: disc %d: too soon; %d item(s) held until %s\n", h.seq, h.items, h.until.Local().Format("2006-01-02"))
			continue
		}
		_, _ = fmt.Fprintf(stdout, "gc: disc %d: not verified; %d item(s) held\n", h.seq, h.items)
	}
	for _, s := range p.skipped {
		if s.noIndex {
			_, _ = fmt.Fprintf(stdout, "gc: %d item(s) skipped: disc %d's table is not in the catalog\n", s.items, s.seq)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "gc: %d item(s) skipped: disc %d's table does not list them\n", s.items, s.seq)
	}
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
		row, found := findObjectRow(idx, id)
		if !found {
			unlisted++
			continue
		}
		if row.Kind != format.ObjectKindChunk {
			continue
		}
		path := layout.chunkFile(id)
		var size uint64
		if fi, err := os.Stat(path); err == nil {
			size = diskBytes(fi)
		}
		objs = append(objs, gcObj{id: id, path: path, size: size, discUUID: discUUID})
	}
	return objs, unlisted
}

// recordGC writes the records of the discs of p before gc unlinks a
// chunk file: the Freed events as one synced batch, then the OnDisc
// records of the items of every disc as one synced batch. The events are
// the intent. A crash after the events leaves on disc only discs with
// Packed items: the next command that holds the lock writes their OnDisc
// records, and the next gc then frees their chunk files as orphans.
func recordGC(logs *stage.Logs, p *gcPlan, now time.Time) error {
	if len(p.discs) == 0 {
		return nil
	}
	var items []object.ID
	events := make([]stage.DiscRecord, 0, len(p.discs))
	for _, d := range p.discs {
		items = append(items, d.items...)
		events = append(events, discEvent(now, d.uuid, stage.EventFreed))
	}
	if err := logs.Discs.Append(events...); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	return logs.Items.MarkOnDisc(items...)
}

// applyGC unlinks the chunk files and removes the plan directories of p.
// It returns the items it freed, the bytes it freed, and every path it
// could not remove. An item of a freed disc counts as freed: its OnDisc
// record is durable. An orphan counts when gc unlinked its file.
func applyGC(p *gcPlan) (items int, bytes uint64, failures []gcFailure) {
	for _, d := range p.discs {
		items += len(d.items)
		_, b, f := gcApplyStagingObjects(d.objs)
		bytes += b
		failures = append(failures, f...)
	}
	n, b, f := gcApplyStagingObjects(p.orphans)
	items += n
	bytes += b
	failures = append(failures, f...)
	b, f = gcApplyPlanDirs(p.planDirs)
	bytes += b
	failures = append(failures, f...)
	return items, bytes, failures
}

// gcObj is one chunk file that gc frees: the item, the path of its file,
// the disk space of the file, and the disc that holds the item.
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
		objs = append(objs, gcObj{id: id, path: path, size: diskBytes(fi), discUUID: rec.DiscUUID})
	}
	slices.SortFunc(objs, func(a, b gcObj) int { return strings.Compare(a.path, b.path) })
	return objs
}

// gcFailure names one path that gc could not remove, and why.
type gcFailure struct {
	path string
	err  error
}

// gcApplyStagingObjects unlinks every file of objs. It reports every
// file it could not unlink. A file that is already gone frees nothing
// and is not a failure.
func gcApplyStagingObjects(objs []gcObj) (deleted int, bytesFreed uint64, failures []gcFailure) {
	for _, o := range objs {
		removeErr := gcRemove(o.path)
		if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
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

// findObjectRow returns idx's Objects row for id. It confirms that the
// run holds the object before gc removes the staged copy.
func findObjectRow(idx *format.Index, id object.ID) (format.IndexObjectRecord, bool) {
	for _, row := range idx.Objects {
		if object.ID(row.ContentID) == id {
			return row, true
		}
	}
	return format.IndexObjectRecord{}, false
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

// dirBytes sums the disk space of every regular file below path. It does
// not follow a symlink. It reports ok false when path does not exist.
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
			total += diskBytes(info)
		}
		return nil
	})
	return total, true
}

// gcApplyPlanDirs removes every plan directory of dirs. os.RemoveAll
// removes a symlink and does not follow it.
func gcApplyPlanDirs(dirs []gcPlanDir) (bytesFreed uint64, failures []gcFailure) {
	for _, d := range dirs {
		if err := os.RemoveAll(d.path); err != nil {
			failures = append(failures, gcFailure{path: d.path, err: err})
			continue
		}
		bytesFreed += d.bytes
	}
	return bytesFreed, failures
}

// diskBytes is the disk space that removing the file of fi frees: its
// allocated blocks. A sparse file, such as an image, frees less than its
// apparent size. It is the apparent size when fi has no block count.
func diskBytes(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Blocks) * 512
	}
	return uint64(fi.Size())
}
