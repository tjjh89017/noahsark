package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// discSummary is one disc's row in "status": every row the ledger
// records for a disc_uuid, folded into a single line.
type discSummary struct {
	UUID          string
	Seq           uint64
	Label         string
	CapacityBytes uint64
	OnDiscObjects int
	PackedObjects int
	BurnedObjects int
	CleanObjects  int
	// OnDiscOnlyObjects is how many of the disc's objects staging holds
	// no file for: gc freed them, or recover read them from the
	// disc itself. Such an object waits for no burn and no verify.
	OnDiscOnlyObjects int
	// VerifiedCopies is the lowest verify count of the CLEAN objects of
	// the disc, and 0 when the disc has no CLEAN object. gc frees an
	// object at gc.min_verified_copies verifies, so this is the number
	// the operator watches.
	VerifiedCopies uint8
	MinCopies      int
}

// cmdStatus implements "noahsark status": what waits for a pack, the
// state of every disc in one word, and the one action to take next. It
// replaces the counter list "disc list" printed. A counter answers a
// question the operator did not ask; the state word and the next line
// answer the one they did.
func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark status", "Show what is staged, the state of every disc, and what to do next.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("status", fs, stderr) {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark status")
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 2
	}
	if refuseBadConfig("status", cfg, stderr, "gc.min_verified_copies") {
		return 2
	}

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}
	stageLog, err := stage.OpenReadOnly(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}
	warnIfTruncated("status", stageLog, stderr)

	discs := summarizeDiscs(ledger.Rows, stageLog, cfg.MinVerifiedCopies)

	stagedObjects, stagedBytes, err := stagedTotals(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: status:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "staged: %d objects, %d bytes\n", stagedObjects, stagedBytes)
	for _, d := range discs {
		_, _ = fmt.Fprintf(stdout, "disc %d %q  %s  %s\n", d.Seq, d.Label, discStateWord(d), d.UUID)
	}
	_, _ = fmt.Fprintln(stdout, nextStepLine(discs, stagedObjects))
	return 0
}

// discStateWord renders one disc's state as the single word the
// operator acts on.
func discStateWord(d discSummary) string {
	switch {
	case notFed(d):
		return "not fed"
	case d.OnDiscObjects > 0 && d.OnDiscOnlyObjects == d.OnDiscObjects:
		return "on disc only"
	case d.PackedObjects > 0:
		return "packed"
	case d.BurnedObjects > 0:
		return "burned"
	case d.CleanObjects > 0 && int(d.VerifiedCopies) < d.MinCopies:
		return fmt.Sprintf("verified %d/%d", d.VerifiedCopies, d.MinCopies)
	case d.CleanObjects > 0:
		return "verified"
	default:
		return "packed"
	}
}

// notFed reports whether the disc ledger names this disc but the state
// log holds no object of it at all. A partial recover leaves exactly
// this: the ledger came from a disc that was fed, and this disc was
// not. The objects of the disc are unknown until it is fed too.
func notFed(d discSummary) bool {
	return d.OnDiscObjects == 0 && d.PackedObjects == 0 && d.BurnedObjects == 0 && d.CleanObjects == 0
}

// nextStepLine names the one action to take next, in the order of the
// disc cycle: feed a disc to recover, burn, verify, verify the second
// copy, pack, commit. A repository with nothing waiting says so.
func nextStepLine(discs []discSummary, stagedObjects int) string {
	for _, d := range discs {
		if notFed(d) {
			return fmt.Sprintf("next: mount disc %d, then run: noahsark recover <MOUNT>", d.Seq)
		}
	}
	for _, d := range discs {
		if d.PackedObjects > 0 {
			return fmt.Sprintf("next: burn disc %d, then run: noahsark disc burned %d", d.Seq, d.Seq)
		}
	}
	for _, d := range discs {
		if d.BurnedObjects > 0 {
			return fmt.Sprintf("next: mount disc %d, then run: noahsark verify <MOUNT>", d.Seq)
		}
	}
	for _, d := range discs {
		if d.CleanObjects > 0 && int(d.VerifiedCopies) < d.MinCopies {
			return fmt.Sprintf("next: verify the second copy of disc %d", d.Seq)
		}
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
// discSummary: the label and forced capacity of its newest run, the sum
// of used_sectors across every run, the disc's on-disc, packed, burned,
// clean and on-disc-only object counts, and its verify count against
// minCopies.
func summarizeDiscs(rows []format.DiscsRow, stageLog *stage.Log, minCopies int) []discSummary {
	onDiscByDisc := stageLog.OnDiscCountByDisc()
	packedByDisc := stageLog.PackedCountByDisc()
	burnedByDisc := stageLog.CountByDiscInState(stage.Burned)
	cleanByDisc := stageLog.CleanCountByDisc()
	onDiscOnlyByDisc := stageLog.CountByDiscInState(stage.OnDiscOnly)
	verifiedByDisc := stageLog.MinCleanVerifyCountByDisc()

	order := make([]string, 0)
	byUUID := make(map[string][]format.DiscsRow)
	for _, r := range rows {
		key := uuidText(r.DiscUUID)
		if _, ok := byUUID[key]; !ok {
			order = append(order, key)
		}
		byUUID[key] = append(byUUID[key], r)
	}
	sort.Slice(order, func(i, j int) bool {
		return byUUID[order[i]][0].DiscSeq < byUUID[order[j]][0].DiscSeq
	})

	discs := make([]discSummary, 0, len(order))
	for _, key := range order {
		discRows := byUUID[key]
		newest := discRows[len(discRows)-1]
		discs = append(discs, discSummary{
			UUID:              key,
			Seq:               newest.DiscSeq,
			Label:             labelText(newest.Label[:newest.LabelLen]),
			CapacityBytes:     newest.CapacitySectors * image.SectorSize,
			OnDiscObjects:     onDiscByDisc[newest.DiscUUID],
			PackedObjects:     packedByDisc[newest.DiscUUID],
			BurnedObjects:     burnedByDisc[newest.DiscUUID],
			CleanObjects:      cleanByDisc[newest.DiscUUID],
			OnDiscOnlyObjects: onDiscOnlyByDisc[newest.DiscUUID],
			VerifiedCopies:    verifiedByDisc[newest.DiscUUID],
			MinCopies:         minCopies,
		})
	}
	return discs
}
