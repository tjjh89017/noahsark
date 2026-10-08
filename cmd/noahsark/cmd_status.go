package main

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// discSummary is one disc's row in "status": the row of the disc ledger
// for one disc uuid, and the record of the disc in the disc state log.
type discSummary struct {
	UUID          string
	Seq           uint64
	Label         string
	CapacityBytes uint64
	// Info is the replayed record of the disc. Its state is DiscUnknown
	// when the disc state log does not know the disc.
	Info stage.DiscInfo
	// Items is the number of items whose newest record names the disc.
	Items int
}

func init() {
	register(&command{
		name:    "status",
		usage:   "status",
		summary: "Show what is staged, the state of every disc, and what to do next.",
		flags:   func(*flag.FlagSet) runFunc { return cmdStatus },
	})
}

// cmdStatus implements "noahsark status": the staged total, one line
// for each snapshot that is not complete on discs, the count of the Lost
// items while one exists, one line for each disc, one advice line for
// each verified disc, and the one next block of the repository. It prints a warning on
// standard error for each item that pack cannot take, and one warning
// with the count of the Staged items that have no file, and then exits 1.
// A staging directory that does not exist while a Staged or a Packed
// item needs it gives a warning in place of the staged total and the
// snapshot lines, and exit 1. status takes no lock and changes no file.
func cmdStatus(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "status"
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark status")
		return 2
	}

	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return configExitCode(err)
	}
	v, err := readStatusView(cmd, repoDir, cfg, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	if v.noStaging {
		_, _ = fmt.Fprintf(stderr, "noahsark: status: warning: staging directory %s does not exist; staging.dir in config.yaml names it\n", v.next.staging)
	} else {
		_, _ = fmt.Fprintf(stdout, "staged: %d items, %d bytes\n", v.next.staged, v.stagedBytes)
		for _, g := range v.groups {
			if line := statusSnapshotLine(g); line != "" {
				_, _ = fmt.Fprintln(stdout, line)
			}
		}
	}
	if v.lost > 0 {
		_, _ = fmt.Fprintln(stdout, statusLostLine(v.lost))
	}
	for _, d := range v.discs {
		_, _ = fmt.Fprintln(stdout, statusDiscLine(d))
	}
	for _, line := range v.nextLines() {
		_, _ = fmt.Fprintln(stdout, line)
	}
	warned := v.noStaging
	if v.missingFiles > 0 {
		_, _ = fmt.Fprintln(stderr, missingFilesLine(cmd, v.missingFiles))
		warned = true
	}
	for _, g := range v.groups {
		if g.Unreadable != nil {
			_, _ = fmt.Fprintln(stderr, unreadableLine(cmd, *g.Unreadable, g.OnDisc))
			warned = true
		}
	}
	for _, item := range v.orphans {
		_, _ = fmt.Fprintln(stderr, unreadableLine(cmd, item, false))
		warned = true
	}
	if warned {
		return 1
	}
	return 0
}

// statusView is what status reads from a repository: the lines before
// the next block, and the input of the next block.
type statusView struct {
	// noStaging tells that the staging directory does not exist while a
	// Staged or a Packed item needs it. The staged total and the
	// snapshot groups are then not read.
	noStaging    bool
	stagedBytes  uint64
	missingFiles int
	groups       []image.SnapshotGroup
	orphans      []image.UnreadableItem
	// lost is the number of the Lost items.
	lost  int
	discs []discSummary
	next  nextRepo
}

// nextLines returns the advice lines and the next block.
func (v *statusView) nextLines() []string {
	return append(adviceLines(v.next), nextBlock(v.next)...)
}

// readStatusView reads the repository at repoDir as status does. It
// takes no lock and changes no file. The notes of the open of the logs
// go to stderr.
func readStatusView(cmd, repoDir string, cfg repoConfig, stderr io.Writer) (*statusView, error) {
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return nil, err
	}
	layout := layoutOf(repoDir, cfg)
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		return nil, err
	}
	logs, err := openLogs(cmd, layout, false, stderr)
	if err != nil {
		return nil, err
	}
	noStaging, err := stagingLost(layout, logs.Items)
	if err != nil {
		return nil, err
	}
	discs := summarizeDiscs(ledger.Rows, logs)
	repairs, err := logRepairs(layout, logs)
	if err != nil {
		return nil, err
	}
	c, err := catalog.OpenReadOnly(repoDir)
	if err != nil {
		return nil, err
	}
	newest, err := newestSnapshotTime(c, discs)
	if err != nil {
		return nil, err
	}
	snapshots, err := c.ListSnapshots()
	if err != nil {
		return nil, err
	}

	v := &statusView{noStaging: noStaging, lost: logs.Items.CountState(stage.Lost), discs: discs}
	// The chunk files of the staged items are in the staging directory.
	var stagedItems int
	if !noStaging {
		stagedItems, v.stagedBytes, v.missingFiles, err = image.StagedTotals(layout.objectPath(c), logs.Items)
		if err != nil {
			return nil, err
		}
		ids, err := packStore(layout, c, logs.Items).SnapshotIDs()
		if err != nil {
			return nil, err
		}
		v.groups, v.orphans = image.PackGroups(layout.objectPath(c), ids, logs.Items)
	}
	v.next = nextRepo{
		repo:           repoDir,
		staging:        layout.stagingDir(),
		stagingMissing: noStaging,
		newestSnapshot: newest,
		noSnapshot:     len(snapshots) == 0,
		device:         cfg.PackDevice,
		source:         cfg.SourceRoot,
		staged:         stagedItems,
		discs:          nextDiscs(layout, discs),
		repairs:        nextRepairs(ledger.Rows, discs, repairs),
	}
	return v, nil
}

// printNext ends the output of a command that changed state: the advice
// lines and the next block that status would print now. It reads the
// repository at repoDir again, after the change. The notes of that read
// were printed by the command already, thus they are dropped. When the
// read fails, it points to status.
func printNext(e *env, repoDir string) {
	lines := []string{nextStatusLine}
	if cfg, err := readConfig(configPath(repoDir)); err == nil {
		if v, err := readStatusView("status", repoDir, cfg, io.Discard); err == nil {
			lines = v.nextLines()
		}
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(e.stdout, line)
	}
}

// missingFilesLine is the warning of commit and status about the Staged
// items that have no file in the staging store.
func missingFilesLine(cmd string, n int) string {
	return fmt.Sprintf("noahsark: %s: warning: %d staged item(s) have no file in the staging store; commit the same source again", cmd, n)
}

// statusSnapshotLine is the line of one snapshot group of the pack
// order, or "" when the snapshot needs none. A snapshot whose snapshot
// object no disc holds gets a line. A snapshot whose snapshot object a
// disc holds gets a line while pack still takes Staged items with it:
// items that were on a lost disc. N is the count of the Staged items that
// pack takes with the snapshot. The plural form is fixed, so that a
// script can parse it.
func statusSnapshotLine(g image.SnapshotGroup) string {
	switch {
	case !g.OnDisc:
		return fmt.Sprintf("snapshot %s: %d items staged, not complete on discs; its snapshot object is not on a disc yet", shortID(g.ID), g.StagedItems)
	case g.StagedItems > 0:
		return fmt.Sprintf("snapshot %s: %d items staged, not complete on discs; the discs alone cannot restore all of it", shortID(g.ID), g.StagedItems)
	}
	return ""
}

// statusLostLine is the line of the items whose newest record is Lost.
// The state log holds no size, thus the line holds no byte count. The
// plural form is fixed, so that a script can parse it.
func statusLostLine(n int) string {
	return fmt.Sprintf("lost: %d items; only a lost disc holds them", n)
}

// unreadableLine is the warning of pack and status about an item that
// pack cannot take, with the repair. A snapshot object that a disc holds
// comes back from a disc.
func unreadableLine(cmd string, item image.UnreadableItem, snapshotOnDisc bool) string {
	repair := item.Repair()
	if snapshotOnDisc && item.ID == item.Snapshot {
		repair = "run recover with a disc that holds it"
	}
	if item.Snapshot == (object.ID{}) {
		return fmt.Sprintf("noahsark: %s: warning: cannot pack: %s; %s", cmd, item.Problem(), repair)
	}
	return fmt.Sprintf("noahsark: %s: warning: snapshot %s: cannot pack all of it: %s; %s", cmd, shortID(item.Snapshot), item.Problem(), repair)
}

// statusDiscLine is the line of one disc:
// disc SEQ "LABEL"  STATE  UUID.
func statusDiscLine(d discSummary) string {
	return fmt.Sprintf("%s  %s  %s", discNameShort(d.Seq, d.Label), statusStateWord(d.Info), d.UUID)
}

// statusStateWord is the state word of a disc with the suffix of its
// last check. A verified or on disc only disc shows its last check. A
// packed or burned disc shows only a failed last check.
func statusStateWord(info stage.DiscInfo) string {
	word := info.State.String()
	switch info.State {
	case stage.DiscVerified, stage.DiscOnDiscOnly:
		switch info.LastCheck {
		case stage.CheckResultOK:
			return word + ", last check " + statusDate(info.LastCheckTime)
		case stage.CheckResultFailed:
			return word + ", last check failed " + statusDate(info.LastCheckTime)
		case stage.CheckResultNotChecked:
			return word + ", not checked"
		}
	case stage.DiscPacked, stage.DiscBurned:
		if info.LastCheck == stage.CheckResultFailed {
			return word + ", last check failed " + statusDate(info.LastCheckTime)
		}
	}
	return word
}

// newestSnapshotTime returns the time of the newest snapshot that the
// catalog holds, or the zero time when it holds none. It reads the
// snapshots only when a lost disc waits for a commit. A snapshot object
// that does not read gives no time.
func newestSnapshotTime(c *catalog.Catalog, discs []discSummary) (time.Time, error) {
	if !slices.ContainsFunc(discs, func(d discSummary) bool { return waitsForCommit(d.Info) }) {
		return time.Time{}, nil
	}
	ids, err := c.ListSnapshots()
	if err != nil {
		return time.Time{}, err
	}
	var newest time.Time
	for _, id := range ids {
		snap, err := c.ReadSnapshot(id)
		if err != nil {
			continue
		}
		if at := time.Unix(snap.TimeSec, int64(snap.TimeNsec)); at.After(newest) {
			newest = at
		}
	}
	return newest, nil
}

// nextDiscs gives the next block the discs of status. It names a disc
// by its number, or by its full uuid when another disc has the same
// number. It reads whether the disc root and the image exist. The stat
// follows a symlink, so the tree of a pack --out disc exists only while
// its target exists. The disc root of a pack --out disc is the target
// of its symlink.
func nextDiscs(layout repoLayout, discs []discSummary) []nextDisc {
	seqCount := make(map[uint64]int)
	for _, d := range discs {
		seqCount[d.Seq]++
	}
	out := make([]nextDisc, 0, len(discs))
	for _, d := range discs {
		arg := strconv.FormatUint(d.Seq, 10)
		if seqCount[d.Seq] > 1 {
			arg = d.UUID
		}
		img := layout.planImage(d.Info.UUID)
		tree := layout.planTree(d.Info.UUID)
		if target, err := os.Readlink(tree); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(tree), target)
			}
			tree = target
		}
		_, treeErr := os.Stat(tree)
		_, imgErr := os.Stat(img)
		out = append(out, nextDisc{
			arg:         arg,
			seq:         d.Seq,
			label:       d.Label,
			info:        d.Info,
			tree:        tree,
			image:       img,
			treeExists:  treeErr == nil,
			imageExists: imgErr == nil,
		})
	}
	return out
}

// nextRepairs gives the next block the discs of repairs. It takes the
// number of a disc from its row in rows (the DISCS ledger). It
// names a disc by its number, or by its full uuid when another disc that
// status shows has the same number, or when the ledger has no row of it.
func nextRepairs(rows []format.DiscsRow, discs []discSummary, repairs []stage.Repair) []nextRepair {
	out := make([]nextRepair, 0, len(repairs))
	for _, rp := range repairs {
		arg := uuidText(rp.Disc.UUID)
		if row := discRow(rows, rp.Disc.UUID); row.DiscUUID == rp.Disc.UUID {
			same := slices.ContainsFunc(discs, func(d discSummary) bool {
				return d.Seq == row.DiscSeq && d.Info.UUID != rp.Disc.UUID
			})
			if !same {
				arg = strconv.FormatUint(row.DiscSeq, 10)
			}
		}
		out = append(out, nextRepair{arg: arg, command: rp.Command})
	}
	return out
}

// summarizeDiscs gives one discSummary for each row of rows, a DISCS
// ledger's rows, in ascending disc_seq order: the label and the forced
// capacity of the disc, its record in the disc state log, and the count
// of its items. A disc holds one run, thus the ledger holds one row for
// each disc. An undone disc gets no summary.
func summarizeDiscs(rows []format.DiscsRow, logs *stage.Logs) []discSummary {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b format.DiscsRow) int { return cmp.Compare(a.DiscSeq, b.DiscSeq) })

	discs := make([]discSummary, 0, len(sorted))
	for _, r := range sorted {
		info, _ := logs.Discs.Disc(r.DiscUUID)
		if info.State == stage.DiscUndone {
			continue
		}
		info.UUID = r.DiscUUID
		discs = append(discs, discSummary{
			UUID:          uuidText(r.DiscUUID),
			Seq:           r.DiscSeq,
			Label:         labelText(r.Label[:r.LabelLen]),
			CapacityBytes: r.CapacitySectors * image.SectorSize,
			Info:          info,
			Items:         len(logs.Items.ItemsOfDisc(r.DiscUUID)),
		})
	}
	return discs
}
