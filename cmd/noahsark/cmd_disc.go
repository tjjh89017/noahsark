package main

import (
	"flag"
	"fmt"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// The operator burns with growisofs by hand. "disc burned" is how the
// staging state machine learns that a disc was burned: no on-disc
// structure records that moment. See docs/decisions.md, "Burning and
// disc lifecycle".
func init() {
	register(&command{
		name:    "burned",
		group:   "disc",
		usage:   "disc burned [--undo] DISC [DISC...]",
		summary: "Mark a disc burned, moving its PACKED objects to BURNED.",
		flags:   discBurnedFlags,
	})
}

// discBurnedOptions holds the command options of disc burned.
type discBurnedOptions struct {
	undo bool
}

func discBurnedFlags(fs *flag.FlagSet) runFunc {
	o := &discBurnedOptions{}
	fs.BoolVar(&o.undo, "undo", false, "undo: move BURNED objects back to PACKED, for a burn that turned out bad")
	return o.run
}

// run implements "noahsark disc burned [--undo] DISC [DISC...]". It
// moves every PACKED object of each named disc's runs to BURNED,
// standing in for the missing `burn` command: the operator runs it
// right after burning both twins by hand. --undo reverses that, for a
// burn that turned out bad, moving BURNED objects back to PACKED with
// the burn-failed reason.
func (o *discBurnedOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc burned [--undo] DISC [DISC...]")
		return 2
	}

	repoDir, err := e.findRepo()
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

	for _, arg := range args {
		discUUID, err := resolveDiscArg(ledger.Rows, arg)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
			return 2
		}
		row := newestDiscRow(ledger.Rows, discUUID)
		label := labelText(row.Label[:row.LabelLen])

		if o.undo {
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
