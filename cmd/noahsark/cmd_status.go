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
// for each snapshot that is packed in parts, one line for each disc, and the one next block of the repository. status takes
// no lock and changes no file.
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

	parts, err := snapshotsPackedInParts(layout, c, logs.Items)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "staged: %d items, %d bytes\n", stagedItems, stagedBytes)
	for _, s := range parts {
		_, _ = fmt.Fprintln(stdout, statusSnapshotLine(s))
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
	return 0
}

// snapshotsPackedInParts returns the snapshots whose snapshot object is
// Staged and that reach one or more items that are not Staged, in the
// order in which pack takes them.
func snapshotsPackedInParts(layout repoLayout, c *catalog.Catalog, items *stage.Log) ([]image.StagedSnapshot, error) {
	ids, err := packStore(layout, c, items).SnapshotIDs()
	if err != nil {
		return nil, err
	}
	onDisc := func(id object.ID) bool {
		rec, ok := items.Get(id)
		return ok && rec.State.OnDisc()
	}
	staged, err := image.StagedSnapshots(layout.objectPath(c), ids, onDisc)
	if err != nil {
		return nil, err
	}
	var parts []image.StagedSnapshot
	for _, s := range staged {
		if s.PackedInParts {
			parts = append(parts, s)
		}
	}
	return parts, nil
}

// statusSnapshotLine is the line of one snapshot that is packed in
// parts. The plural form is fixed, so that a script can parse it.
func statusSnapshotLine(s image.StagedSnapshot) string {
	return fmt.Sprintf("snapshot %s: %d items staged, not complete on discs; recover cannot find it from the discs alone", shortID(s.ID), s.StagedItems)
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
