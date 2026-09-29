package main

import (
	"flag"
	"fmt"
	"sort"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
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

// cmdStatus implements "noahsark status": what waits for a pack, the
// state of every disc in one word, and the one action to take next.
// status takes no lock and changes no log.
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
		return 2
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

	c, err := catalog.Open(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}
	stagedObjects, stagedBytes, err := image.StagedTotals(layout.objectPath(c), logs.Items)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "staged: %d objects, %d bytes\n", stagedObjects, stagedBytes)
	for _, d := range discs {
		_, _ = fmt.Fprintf(stdout, "disc %d %q  %s  %s\n", d.Seq, d.Label, d.Info.State, d.UUID)
	}
	_, _ = fmt.Fprintln(stdout, nextStepLine(discs, stagedObjects))
	return 0
}

// nextStepLine names the one action to take next, in the order of the
// disc cycle: give a missing disc to recover, burn, verify, pack,
// commit.
func nextStepLine(discs []discSummary, stagedObjects int) string {
	first := func(state stage.DiscState) (discSummary, bool) {
		for _, d := range discs {
			if d.Info.State == state {
				return d, true
			}
		}
		return discSummary{}, false
	}
	if d, ok := first(stage.DiscMissing); ok {
		return fmt.Sprintf("next: mount disc %d, then run: noahsark recover <MOUNT>", d.Seq)
	}
	if d, ok := first(stage.DiscPacked); ok {
		return fmt.Sprintf("next: burn disc %d, then run: noahsark disc burned %d", d.Seq, d.Seq)
	}
	if d, ok := first(stage.DiscBurned); ok {
		return fmt.Sprintf("next: mount disc %d, then run: noahsark verify <MOUNT>", d.Seq)
	}
	if stagedObjects > 0 {
		return "next: pack a disc, run: noahsark pack"
	}
	if len(discs) == 0 {
		return "next: commit your files, run: noahsark commit <SOURCE>"
	}
	return "next: nothing to do"
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
