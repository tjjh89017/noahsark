package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/restore"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	register(&command{
		name:  "restore",
		usage: "restore [--overwrite] [--dry-run] --disc=DIR SNAPSHOT [PATH...] DEST",
		summary: "Restore a snapshot into DEST. Plan from the catalog, then read one disc at a time from the mount point DIR. " +
			"PATH is relative to the source root, as ls prints it; a trailing slash puts the content of a directory into DEST.",
		flags: restoreFlags,
	})
}

// restoreOptions holds the command options of restore.
type restoreOptions struct {
	overwrite bool
	dryRun    bool
	disc      oneValue
}

func restoreFlags(fs *flag.FlagSet) runFunc {
	o := &restoreOptions{}
	fs.BoolVar(&o.overwrite, "overwrite", false, "unlink an existing path first and then create it")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the plan and stop")
	fs.Var(&o.disc, "disc", "the mount point of the one drive; one value; required")
	return o.run
}

// oneValue is a string option that takes one value only.
type oneValue struct {
	value string
	set   bool
}

func (v *oneValue) String() string { return v.value }

func (v *oneValue) Set(s string) error {
	if v.set {
		return errors.New("the option takes one value only")
	}
	v.value, v.set = s, true
	return nil
}

// errNoRepoForRestore is the refusal of restore with no repository.
var errNoRepoForRestore = errors.New("no repository; run recover first, one time for each disc")

// run implements "noahsark restore". It plans from the catalog and reads
// one disc at a time from --disc. It changes no file of the repository.
func (o *restoreOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	if o.disc.value == "" || len(args) < 2 {
		if o.disc.value == "" {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore: --disc=DIR is required")
		}
		_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--overwrite] [--dry-run] --disc=DIR SNAPSHOT [PATH...] DEST")
		return 2
	}
	snapArg, paths, destArg := args[0], args[1:len(args)-1], args[len(args)-1]

	repoDir, err := e.findRepo()
	if err != nil {
		if errors.Is(err, errNoRepo) {
			err = errNoRepoForRestore
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return configExitCode(err)
	}
	layout := layoutOf(repoDir, cfg)
	c, err := catalog.OpenReadOnly(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	src := &catalogSource{c: c, refsPath: layout.refsFile()}
	snapID, err := src.ParseSnapshotArg(snapArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return exitForSnapshotArg(err)
	}
	if c.Partial(snapID) {
		_, _ = fmt.Fprintf(stderr, "noahsark: restore: snapshot %s is partial; run recover with more discs\n", shortID(snapID))
		return 1
	}
	snap, err := src.Snapshot(snapID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	sel, err := plan.Select(c, snap, paths)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		if _, ok := errors.AsType[*plan.PathError](err); ok {
			return 2
		}
		return 1
	}

	dest, err := e.abs(destArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	mountDir, err := e.abs(o.disc.value)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	discs, err := restoreDiscs(layout, cfg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	p, err := plan.New(c, sel, discs.list)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	defer p.Close()
	if err := restore.Scan(c, sel, dest, o.overwrite, p.Add); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	if err := p.Count(); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	needed := p.Discs()
	printRestorePlan(stdout, needed, p.NoDisc())
	if o.dryRun {
		return 0
	}

	a, err := restore.NewAssembler(c, sel, dest, o.overwrite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	defer a.Close()
	s := &discSwap{
		e:        e,
		mountDir: mountDir,
		dirArg:   o.disc.value,
		names:    discs.names,
		plan:     p,
		input:    bufio.NewScanner(e.stdin),
	}
	prog := e.progress()
	prog.Start("restore: bytes written", 0)
	err = s.readDiscs(a, needed, prog)
	prog.Done()
	if stop, ok := errors.AsType[*insertDiscError](err); ok {
		_, _ = fmt.Fprintf(stderr, "restore: insert %s into %s and run restore again\n", stop.disc, s.dirArg)
		return 1
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	if err := a.Finish(); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	rep := a.Report()
	printProblems(stderr, rep)
	_, _ = fmt.Fprintf(stdout, "restored snapshot %s into %s\n", shortID(snapID), destArg)
	if rep.Resumed > 0 {
		_, _ = fmt.Fprintf(stdout, "skipped: %d file(s) already restored\n", rep.Resumed)
	}
	if rep.Failed() {
		return 1
	}
	return 0
}

// printRestorePlan prints the plan: one line for each needed disc, the
// line of the items with no known disc, and the totals line.
func printRestorePlan(w io.Writer, discs []plan.DiscEntry, noDisc int) {
	items, bytes := 0, uint64(0)
	for _, d := range discs {
		lost := ""
		if d.Lost {
			lost = " (lost)"
		}
		_, _ = fmt.Fprintf(w, "%s: %d items, %d bytes%s\n", discName(d.DiscSeq, d.Label, d.DiscUUID), d.Items, d.Bytes, lost)
		items += d.Items
		bytes += d.Bytes
	}
	if noDisc > 0 {
		_, _ = fmt.Fprintf(w, "restore: %d item(s) have no disc known to the catalog; run recover with more discs\n", noDisc)
	}
	_, _ = fmt.Fprintf(w, "totals: %d discs, %d items, %d bytes\n", len(discs), items, bytes)
}

// printProblems writes one warning line for each problem that the
// report holds, then the count of the problems it does not hold, then
// the summary line.
func printProblems(stderr io.Writer, rep restore.Report) {
	const prefix = "noahsark: restore: warning:"
	for _, p := range rep.Problems {
		_, _ = fmt.Fprintf(stderr, "%s %s: %s\n", prefix, p.Path, p.Err)
	}
	if dropped := rep.Dropped(); dropped > 0 {
		_, _ = fmt.Fprintf(stderr, "%s %d more problem(s) not shown\n", prefix, dropped)
	}
	if summary := rep.Summary(); summary != "" {
		_, _ = fmt.Fprintf(stderr, "%s %s\n", prefix, summary)
	}
}

// restoreDiscList is the discs that a restore can plan with, and the
// name of each disc that the repository knows.
type restoreDiscList struct {
	list  []plan.Disc
	names map[[16]byte]plan.Disc
}

// restoreDiscs reads the discs of the repository from the disc ledger
// and their states from the disc state log. It leaves out an undone
// disc. It reads only.
func restoreDiscs(layout repoLayout, cfg repoConfig) (restoreDiscList, error) {
	out := restoreDiscList{names: make(map[[16]byte]plan.Disc)}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return out, err
	}
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		return out, err
	}
	discLog, err := stage.OpenDiscLogReadOnly(layout.stateDir())
	if err != nil {
		return out, err
	}
	var order [][16]byte
	for _, row := range ledger.Rows {
		if _, ok := out.names[row.DiscUUID]; !ok {
			order = append(order, row.DiscUUID)
		}
		out.names[row.DiscUUID] = plan.Disc{DiscUUID: row.DiscUUID, DiscSeq: row.DiscSeq, Label: labelText(row.Label[:row.LabelLen])}
	}
	for _, uuid := range order {
		d := out.names[uuid]
		info, _ := discLog.Disc(uuid)
		switch info.State {
		case stage.DiscUndone:
			delete(out.names, uuid)
			continue
		case stage.DiscLost:
			d.Lost = true
		}
		out.list = append(out.list, d)
	}
	return out, nil
}

// insertDiscError stops a restore that needs another disc and cannot
// ask for it: standard input is not a terminal, or it ended.
type insertDiscError struct{ disc string }

func (e *insertDiscError) Error() string { return "insert " + e.disc }

// discSwapRetries is how many times a restore reads an unreadable
// DISC.bin again before it asks for the disc, and discSwapRetryPause is
// the pause between two reads.
const discSwapRetries = 3

var discSwapRetryPause = 300 * time.Millisecond

// discSwap reads the discs of a plan one at a time from one mount point.
type discSwap struct {
	e *env
	// mountDir is the absolute mount point; dirArg is the text the
	// operator gave for it.
	mountDir string
	dirArg   string
	names    map[[16]byte]plan.Disc
	plan     *plan.Plan
	input    *bufio.Scanner
}

// readDiscs reads each disc of discs that is not lost, in disc_seq
// order. A disc that is already at the mount point and still needed
// comes first. With no disc to read, it still walks the selection one
// time, to write what needs no disc.
func (s *discSwap) readDiscs(a *restore.Assembler, discs []plan.DiscEntry, prog *progress.Reporter) error {
	var todo []plan.DiscEntry
	for _, d := range discs {
		if !d.Lost {
			todo = append(todo, d)
		}
	}
	if len(todo) == 0 {
		return a.Disc(noDisc{}, prog)
	}
	for len(todo) > 0 {
		next := 0
		if id, err := restore.ReadDiscIdentity(s.mountDir); err == nil {
			for i, d := range todo {
				if d.DiscUUID == id.UUID {
					next = i
					break
				}
			}
		}
		d := todo[next]
		todo = append(todo[:next], todo[next+1:]...)
		md := &mountedDisc{swap: s, disc: d.Disc}
		if err := a.Disc(md, prog); err != nil {
			if fatal, ok := errors.AsType[*restore.FatalDiscError](err); ok {
				return fatal.Err
			}
			return err
		}
	}
	return nil
}

// noDisc is a disc that holds nothing.
type noDisc struct{}

func (noDisc) Has(object.ID) bool { return false }

func (noDisc) Read(id object.ID) ([]byte, error) {
	return nil, fmt.Errorf("chunk %s: no disc holds it", id.TextForm())
}

// mountedDisc is one plan disc, read through the mount point. It checks
// the disc at the first chunk that the walk reads from it, so a disc
// that the restore no longer needs is never asked for.
type mountedDisc struct {
	swap  *discSwap
	disc  plan.Disc
	found bool
}

func (m *mountedDisc) Has(id object.ID) bool { return m.swap.plan.Holds(m.disc.DiscUUID, id) }

// Read returns one verified chunk payload, after the disc is at the
// mount point. A disc that the restore cannot get stops the whole
// restore; a chunk that does not read or does not verify fails its own
// file only.
func (m *mountedDisc) Read(id object.ID) ([]byte, error) {
	if !m.found {
		if err := m.swap.waitFor(m.disc); err != nil {
			return nil, &restore.FatalDiscError{Err: err}
		}
		m.found = true
	}
	return restore.ReadChunkFromRoot(m.swap.mountDir, id)
}

// waitFor returns when the disc d is at the mount point. It prints
// "disc SEQ "LABEL": found" for it. For a wrong disc it names both
// discs; for an unreadable DISC.bin it reads again a few times first.
// Then it asks for d on a terminal, or returns an *insertDiscError. The
// operator swaps the disc; restore never unmounts and never ejects.
func (s *discSwap) waitFor(d plan.Disc) error {
	want := discName(d.DiscSeq, d.Label, d.DiscUUID)
	stderr := s.e.stderr
	unreadable := 0
	for {
		found, err := restore.ReadDiscIdentity(s.mountDir)
		switch {
		case err != nil:
			unreadable++
			if unreadable <= discSwapRetries {
				time.Sleep(discSwapRetryPause)
				continue
			}
		case found.UUID == d.DiscUUID:
			_, _ = fmt.Fprintf(s.e.stdout, "%s: found\n", discNameShort(d.DiscSeq, d.Label))
			return nil
		default:
			_, _ = fmt.Fprintf(stderr, "expected %s, found %s\n", want, s.foundName(found))
		}
		if !s.e.stdinTTY {
			return &insertDiscError{disc: want}
		}
		_, _ = fmt.Fprintf(stderr, "insert %s into %s and press Enter\n", want, s.dirArg)
		if !s.input.Scan() {
			return &insertDiscError{disc: want}
		}
		unreadable = 0
	}
}

// foundName names the disc at the mount point: by the name that the
// repository gives it, or by its uuid when the repository does not know
// it.
func (s *discSwap) foundName(found restore.DiscIdentity) string {
	d, ok := s.names[found.UUID]
	if !ok {
		return "disc " + uuidText(found.UUID)
	}
	return discName(d.DiscSeq, d.Label, d.DiscUUID)
}
