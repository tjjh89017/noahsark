package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// "disc lost" tells the tool to stop trusting a disc that is gone. The
// operator runs "disc lost --undo" when the disc turns up again.
func init() {
	register(&command{
		name:    "lost",
		group:   "disc",
		usage:   "disc lost [--undo] DISC",
		summary: "Mark a disc lost and return its items to staged, or remove the mark with --undo.",
		flags:   discLostFlags,
	})
}

// discLostOptions holds the command options of disc lost.
type discLostOptions struct {
	undo bool
}

func discLostFlags(fs *flag.FlagSet) runFunc {
	o := &discLostOptions{}
	fs.BoolVar(&o.undo, "undo", false, "remove the lost mark of a found disc")
	return o.run
}

// discLostRun is the open repository of one disc lost call.
type discLostRun struct {
	e      *env
	layout repoLayout
	logs   *stage.Logs
	disc   discTarget
}

// run implements "noahsark disc lost [--undo] DISC". docs/states.md,
// rows 57 to 66, gives the messages.
func (o *discLostOptions) run(e *env, args []string) int {
	stderr := e.stderr
	const cmd = "disc lost"
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc lost [--undo] DISC")
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
	r := &discLostRun{e: e, layout: layout, logs: logs, disc: discTargetOf(ledger.Rows, logs.Discs, discUUID)}
	if o.undo {
		return r.undo()
	}
	return r.markLost()
}

// fail prints err as the error of the command and returns exit code 1.
func (r *discLostRun) fail(cmd string, err error) int {
	_, _ = fmt.Fprintf(r.e.stderr, "noahsark: %s: %v\n", cmd, err)
	return 1
}

// markLost implements "disc lost DISC". After a critical confirmation, it
// appends the Lost event, then returns the Packed items of the disc and
// its OnDisc items that the catalog holds to Staged, and marks its other
// OnDisc items Lost. Then it removes the plan directory of the disc. It
// keeps the catalog data of the disc. A stop after the event leaves item
// records that the next command with the lock writes.
func (r *discLostRun) markLost() int {
	const cmd = "disc lost"
	e, disc := r.e, r.disc
	switch disc.info.State {
	case stage.DiscPacked, stage.DiscBurned, stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscMissing:
	case stage.DiscLost:
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %s is already marked lost\n", cmd, disc.short())
		return 1
	default:
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %s\n", cmd, discStateRefusal(disc))
		return 1
	}
	warning := []string{disc.warning(stage.DiscLost), "the tool stops trusting this disc"}
	if !e.confirm(confirmCritical, cmd, warning) {
		return 1
	}

	u := disc.info.UUID
	if err := r.logs.Discs.Append(discEvent(e.now(), u, stage.EventLost)); err != nil {
		return r.fail(cmd, err)
	}
	n, err := r.logs.CompleteDisc(u, nil, catalogHolds(r.layout.repo))
	if err != nil {
		return r.fail(cmd, err)
	}
	// RemoveAll does not follow the tree symlink of a pack --out disc: the
	// directory that the symlink names stays.
	if err := os.RemoveAll(r.layout.planDir(u)); err != nil {
		return r.fail(cmd, err)
	}

	switch disc.info.State {
	case stage.DiscOnDiscOnly:
		lost := len(r.logs.Items.ItemsOfDiscInState(u, stage.Lost))
		_, _ = fmt.Fprintf(e.stdout, "%s: marked lost; %d item(s) returned to staged; %d item(s) need a new commit\n", disc.name(), n-lost, lost)
	case stage.DiscMissing:
		_, _ = fmt.Fprintf(e.stdout, "%s: marked lost; its items are not known; a new commit stages what the source still holds\n", disc.name())
	default:
		_, _ = fmt.Fprintf(e.stdout, "%s: marked lost; %d item(s) returned to staged\n", disc.name(), n)
	}
	_, _ = fmt.Fprintln(e.stdout, nextStatusLine)
	return 0
}

// undo implements "disc lost --undo DISC". After an ordinary
// confirmation, it appends the LostUndone event, then gives the items
// back to the found disc. A stop after the event leaves item records
// that the next command with the lock writes.
func (r *discLostRun) undo() int {
	const cmd = "disc lost"
	e, disc := r.e, r.disc
	if disc.info.State != stage.DiscLost {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %s is not marked lost\n", cmd, disc.short())
		return 1
	}
	after, ok := stage.DiscTransition(stage.DiscLost, disc.info.BeforeLost, stage.EventLostUndone)
	if !ok {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %s had no verified record when it was marked lost; its items are staged again; the lost mark stays\n", cmd, disc.short())
		return 1
	}
	warning := []string{
		disc.warning(after),
		"the tool trusts this disc again only after a good check; you must run verify on it",
	}
	if !e.confirm(confirmOrdinary, "disc lost --undo", warning) {
		return 1
	}

	u := disc.info.UUID
	index := catalogIndexItems(r.layout.repo)
	// A Staged record names no disc, thus the INDEX that disc lost kept
	// is the only list of the items of a disc that was verified. The
	// INDEX must be readable before the event.
	if disc.info.BeforeLost == stage.DiscVerified {
		if _, _, err := index(u); err != nil {
			return r.fail(cmd, err)
		}
	}
	if err := r.logs.Discs.Append(discEvent(e.now(), u, stage.EventLostUndone)); err != nil {
		return r.fail(cmd, err)
	}
	back, err := r.logs.CompleteDisc(u, index, catalogHolds(r.layout.repo))
	if err != nil {
		return r.fail(cmd, err)
	}

	if after == stage.DiscMissing {
		_, _ = fmt.Fprintf(e.stdout, "%s: lost mark removed; give it to recover\n", disc.name())
	} else {
		_, _ = fmt.Fprintf(e.stdout, "%s: lost mark removed; %d item(s) back on this disc; verify it now\n", disc.name(), back)
	}
	_, _ = fmt.Fprintln(e.stdout, nextStatusLine)
	return 0
}
