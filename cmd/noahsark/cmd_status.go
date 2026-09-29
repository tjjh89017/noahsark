package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// discSummary is one disc's row in "status": the rows of the disc ledger
// for one disc uuid, folded into a single line, and the record of the
// disc in the disc state log.
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
// for each snapshot that is not complete on discs, one line for each
// disc, and the one next block of the repository. It prints a warning on
// standard error for each item that pack cannot take, and then exits 1.
// status takes no lock and changes no file.
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

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	layout := layoutOf(repoDir, cfg)
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}
	logs, err := openLogs(cmd, layout, false, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	discs := summarizeDiscs(ledger.Rows, logs)

	c, err := catalog.OpenReadOnly(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}
	stagedItems, stagedBytes, err := image.StagedTotals(layout.objectPath(c), logs.Items)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	ids, err := packStore(layout, c, logs.Items).SnapshotIDs()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}
	groups, orphans := image.PackGroups(layout.objectPath(c), ids, logs.Items)

	_, _ = fmt.Fprintf(stdout, "staged: %d items, %d bytes\n", stagedItems, stagedBytes)
	for _, g := range groups {
		if line := statusSnapshotLine(g); line != "" {
			_, _ = fmt.Fprintln(stdout, line)
		}
	}
	for _, d := range discs {
		_, _ = fmt.Fprintln(stdout, statusDiscLine(d))
	}
	r := nextRepo{
		repo:   repoDir,
		device: cfg.PackDevice,
		source: cfg.SourceRoot,
		staged: stagedItems,
		now:    e.now(),
		discs:  nextDiscs(layout, discs),
	}
	for _, line := range nextBlock(r) {
		_, _ = fmt.Fprintln(stdout, line)
	}
	warned := false
	for _, g := range groups {
		if g.Unreadable != nil {
			_, _ = fmt.Fprintln(stderr, unreadableLine(cmd, *g.Unreadable, g.OnDisc))
			warned = true
		}
	}
	for _, item := range orphans {
		_, _ = fmt.Fprintln(stderr, unreadableLine(cmd, item, false))
		warned = true
	}
	if warned {
		return 1
	}
	return 0
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
		return fmt.Sprintf("snapshot %s: %d items staged, not complete on discs; recover cannot find it from the discs alone", shortID(g.ID), g.StagedItems)
	case g.StagedItems > 0:
		return fmt.Sprintf("snapshot %s: %d items staged, not complete on discs; the discs alone cannot restore all of it", shortID(g.ID), g.StagedItems)
	}
	return ""
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
// disc SEQ "LABEL"  STATE  [fec  ]UUID.
func statusDiscLine(d discSummary) string {
	fec := ""
	if d.Info.FEC {
		fec = "fec  "
	}
	return fmt.Sprintf("%s  %s  %s%s", discNameShort(d.Seq, d.Label), statusStateWord(d.Info), fec, d.UUID)
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

// nextDiscs gives the next block the discs of status. It names a disc
// by its number, or by its full uuid when another disc has the same
// number. It reads whether the disc root and the image exist. The stat
// follows a symlink, so the tree of a pack --out disc exists only while
// its target exists.
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
		_, treeErr := os.Stat(layout.planTree(d.Info.UUID))
		_, imgErr := os.Stat(img)
		out = append(out, nextDisc{
			arg:         arg,
			label:       d.Label,
			info:        d.Info,
			image:       img,
			treeExists:  treeErr == nil,
			imageExists: imgErr == nil,
		})
	}
	return out
}

// summarizeDiscs groups rows (a DISCS ledger's rows) by disc_uuid, in
// ascending disc_seq order, and folds each disc's rows into one
// discSummary: the label and forced capacity of its newest run, its
// record in the disc state log, and the count of its items. An undone
// disc gets no summary.
func summarizeDiscs(rows []format.DiscsRow, logs *stage.Logs) []discSummary {
	order := make([]string, 0)
	byUUID := make(map[string][]format.DiscsRow)
	for _, r := range rows {
		key := uuidText(r.DiscUUID)
		if _, ok := byUUID[key]; !ok {
			order = append(order, key)
		}
		byUUID[key] = append(byUUID[key], r)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return byUUID[order[i]][0].DiscSeq < byUUID[order[j]][0].DiscSeq
	})

	discs := make([]discSummary, 0, len(order))
	for _, key := range order {
		discRows := byUUID[key]
		newest := discRows[len(discRows)-1]
		info, _ := logs.Discs.Disc(newest.DiscUUID)
		if info.State == stage.DiscUndone {
			continue
		}
		info.UUID = newest.DiscUUID
		discs = append(discs, discSummary{
			UUID:          key,
			Seq:           newest.DiscSeq,
			Label:         labelText(newest.Label[:newest.LabelLen]),
			CapacityBytes: newest.CapacitySectors * image.SectorSize,
			Info:          info,
			Items:         len(logs.Items.ItemsOfDisc(newest.DiscUUID)),
		})
	}
	return discs
}
