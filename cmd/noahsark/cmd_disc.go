package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// cmdDisc implements "noahsark disc". "noahsark status" lists the
// discs, so "disc" carries the burn mark and its undo alone.
//
// The operator burns with growisofs by hand. "disc burned" is how the
// staging state machine learns that a disc was burned: no on-disc
// structure records that moment. See docs/decisions.md, "Burning and
// disc lifecycle".
func cmdDisc(args []string, stdout, stderr io.Writer) int {
	const discUsage = "usage: noahsark disc burned [--undo] DISC [DISC...]"

	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, discUsage)
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, discUsage)
		return 0
	}
	sub := args[0]
	rest := args[1:]

	if strings.HasPrefix(sub, "-") {
		_, _ = fmt.Fprintf(stderr, "noahsark: disc: flags come after the subcommand: noahsark disc burned %s\n", sub)
		return 2
	}

	switch sub {
	case "list":
		_, _ = fmt.Fprintln(stderr, "noahsark: disc list: renamed: run noahsark status")
		return 2
	case "burned":
		return cmdDiscBurned(rest, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "noahsark: disc: unknown subcommand %q\n", sub)
		return 2
	}
}

// cmdDiscBurned implements "noahsark disc burned [--undo] DISC [DISC...]".
// It moves every PACKED object of each named disc's runs to BURNED,
// standing in for the missing `burn` command: the operator runs it
// right after burning both twins by hand. --undo reverses that, for a
// burn that turned out bad, moving BURNED objects back to PACKED with
// the burn-failed reason.
func cmdDiscBurned(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark disc burned [--undo] DISC [DISC...]",
		"Mark a disc burned, moving its PACKED objects to BURNED.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	undo := fs.Bool("undo", false, "undo: move BURNED objects back to PACKED, for a burn that turned out bad")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("disc burned", fs, stderr) {
		return 2
	}
	if fs.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc burned [--undo] DISC [DISC...]")
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 2
	}
	lk, code, ok := lockRepo("disc burned", repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 1
	}
	ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 1
	}
	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 1
	}
	warnIfTruncated("disc burned", stageLog, stderr)

	for _, arg := range fs.Args() {
		discUUID, err := resolveDiscArg(ledger.Rows, arg)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
			return 2
		}
		row := newestDiscRow(ledger.Rows, discUUID)
		label := labelText(row.Label[:row.LabelLen])

		if *undo {
			if n := countInState(stageLog, stage.Clean, discUUID); n > 0 {
				_, _ = fmt.Fprintf(stderr, "noahsark: disc burned: %s is verified and cannot be returned to packed\n", discNameShort(row.DiscSeq, label))
				return 1
			}
			n := undoDiscBurn(stageLog, discUUID)
			_, _ = fmt.Fprintf(stdout, "%s: undo: returned to packed, %d objects\n", discNameShort(row.DiscSeq, label), n)
			continue
		}

		n := markDiscBurned(stageLog, discUUID)
		if n == 0 {
			_, _ = fmt.Fprintf(stdout, "%s: already burned, 0 objects to mark\n", discNameShort(row.DiscSeq, label))
			continue
		}
		_, _ = fmt.Fprintf(stdout, "%s: marked burned, %d object(s) marked\n", discNameShort(row.DiscSeq, label), n)
	}
	return 0
}

// newestDiscRow returns the last ledger row for discUUID, the row whose
// seq and label the output prints.
func newestDiscRow(rows []format.DiscsRow, discUUID [16]byte) format.DiscsRow {
	var out format.DiscsRow
	for _, r := range rows {
		if r.DiscUUID == discUUID {
			out = r
		}
	}
	return out
}

// markDiscBurned moves every object of disc discUUID that is at PACKED
// to BURNED, and reports how many objects it moved. The burned record
// carries the run_seq the packed record already held.
func markDiscBurned(l *stage.Log, discUUID [16]byte) int {
	n := 0
	for _, id := range l.IDsInState(stage.Packed) {
		rec, ok := l.Get(id)
		if !ok || rec.DiscUUID != discUUID {
			continue
		}
		if err := l.MarkBurned(id, rec.RunSeq, discUUID); err == nil {
			n++
		}
	}
	return n
}

// undoDiscBurn moves every object of disc discUUID that is at BURNED
// back to PACKED, and reports how many objects it moved.
func undoDiscBurn(l *stage.Log, discUUID [16]byte) int {
	n := 0
	for _, id := range l.IDsInState(stage.Burned) {
		rec, ok := l.Get(id)
		if !ok || rec.DiscUUID != discUUID {
			continue
		}
		if err := l.MarkBurnUndone(id); err == nil {
			n++
		}
	}
	return n
}
