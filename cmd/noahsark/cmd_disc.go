package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// The operator burns a disc with a tool outside noahsark. "disc burned"
// records the burn: no on-disc structure records that moment. See
// docs/decisions.md, "Burning and disc lifecycle".
func init() {
	register(&command{
		name:    "burned",
		group:   "disc",
		usage:   "disc burned [--undo] DISC",
		summary: "Record the burn of a packed disc, or remove it with --undo.",
		flags:   discBurnedFlags,
	})
}

// discBurnedOptions holds the command options of disc burned.
type discBurnedOptions struct {
	undo bool
}

func discBurnedFlags(fs *flag.FlagSet) runFunc {
	o := &discBurnedOptions{}
	fs.BoolVar(&o.undo, "undo", false, "remove the burn record of a burned disc")
	return o.run
}

// discTarget is one disc that a DISC argument named: its record in the
// disc state log, and its number and label from the disc ledger.
type discTarget struct {
	info  stage.DiscInfo
	seq   uint64
	label string
}

// name is the disc name of a message that reports a change.
func (d discTarget) name() string { return discNameShort(d.seq, d.label) }

// short is the disc name of a refusal.
func (d discTarget) short() string { return fmt.Sprintf("disc %d", d.seq) }

// warning is the first line of a confirmation: the disc, the state now,
// and the state after.
func (d discTarget) warning(after stage.DiscState) string {
	return fmt.Sprintf("warning: %s: %s -> %s", discName(d.seq, d.label, d.info.UUID), d.info.State, after)
}

// resolveDisc resolves a DISC argument against the rows of the disc
// ledger. An undone disc matches no argument.
func resolveDisc(rows []format.DiscsRow, discs *stage.DiscLog, arg string) ([16]byte, error) {
	return resolveDiscArgExcept(rows, arg, func(uuid [16]byte) bool {
		d, ok := discs.Disc(uuid)
		return ok && d.State == stage.DiscUndone
	})
}

// discTargetOf returns the disc discUUID with its number and label from
// the newest ledger row of that disc.
func discTargetOf(rows []format.DiscsRow, discs *stage.DiscLog, discUUID [16]byte) discTarget {
	info, _ := discs.Disc(discUUID)
	info.UUID = discUUID
	row := discRow(rows, discUUID)
	return discTarget{info: info, seq: row.DiscSeq, label: labelText(row.Label[:row.LabelLen])}
}

// discEvent is a disc state log record of event e for the disc discUUID
// at the time now.
func discEvent(now time.Time, discUUID [16]byte, e stage.DiscEvent) stage.DiscRecord {
	return stage.DiscRecord{TimeSec: now.Unix(), DiscUUID: discUUID, Event: e}
}

// run implements "noahsark disc burned [--undo] DISC". docs/states.md,
// rows 20 to 26, gives the messages.
func (o *discBurnedOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "disc burned"
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc burned [--undo] DISC")
		return 2
	}

	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return configExitCode(err)
	}
	lk, code, ok := lockRepo(cmd, repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	layout := layoutOf(repoDir, cfg)
	logs, err := openLogs(cmd, layout, true, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	discUUID, err := resolveDisc(ledger.Rows, logs.Discs, args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	disc := discTargetOf(ledger.Rows, logs.Discs, discUUID)

	if o.undo {
		return undoDiscBurn(e, logs.Discs, disc)
	}
	if refusal := discBurnedRefusal(disc); refusal != "" {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, refusal)
		return 1
	}
	if err := logs.Discs.Append(discEvent(e.now(), discUUID, stage.EventBurnRecorded)); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: burn recorded\n", disc.name())
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	return 0
}

// discBurnedRefusal returns the refusal of disc burned for the state of
// disc, or an empty string when disc burned records the burn.
func discBurnedRefusal(disc discTarget) string {
	switch disc.info.State {
	case stage.DiscPacked:
		return ""
	case stage.DiscBurned:
		return disc.short() + " already has a burn record"
	case stage.DiscVerified, stage.DiscOnDiscOnly:
		return disc.short() + " is already verified"
	}
	return discStateRefusal(disc)
}

// discStateRefusal is the refusal for a disc that is lost, missing, or
// not in the disc state log.
func discStateRefusal(disc discTarget) string {
	switch disc.info.State {
	case stage.DiscLost:
		return disc.short() + " is marked lost"
	case stage.DiscMissing:
		return disc.short() + " is missing"
	}
	return disc.short() + " has no record in the disc state log"
}

// undoDiscBurn implements "disc burned --undo DISC": it removes the
// burn record of a burned disc after an ordinary confirmation.
func undoDiscBurn(e *env, discs *stage.DiscLog, disc discTarget) int {
	const cmd = "disc burned"
	switch disc.info.State {
	case stage.DiscBurned:
	case stage.DiscPacked:
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %s has no burn record\n", cmd, disc.short())
		return 1
	default:
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %s is not burned\n", cmd, disc.short())
		return 1
	}
	warning := []string{
		disc.warning(stage.DiscPacked),
		"the burn record is removed; burn the disc again from its disc root",
	}
	if !e.confirm(confirmOrdinary, "disc burned --undo", warning) {
		return 1
	}
	if err := discs.Append(discEvent(e.now(), disc.info.UUID, stage.EventBurnRemoved)); err != nil {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	_, _ = fmt.Fprintf(e.stdout, "%s: burn record removed\n", disc.name())
	_, _ = fmt.Fprintln(e.stdout, nextStatusLine)
	return 0
}

// discRow returns the ledger row of discUUID, the row whose seq and
// label the output prints. The ledger holds one row for each disc. It
// returns a zero row when the ledger has no row of the disc.
func discRow(rows []format.DiscsRow, discUUID [16]byte) format.DiscsRow {
	for _, r := range rows {
		if r.DiscUUID == discUUID {
			return r
		}
	}
	return format.DiscsRow{}
}
