package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/restore"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	register(&command{
		name:    "verify",
		usage:   "verify [--heal --out=DIR] DISC-ROOT",
		summary: "Read a disc tree back and check it, optionally healing it first.",
		flags:   verifyFlags,
	})
}

// verifyOptions holds the command options of verify.
type verifyOptions struct {
	heal bool
	out  string
}

func verifyFlags(fs *flag.FlagSet) runFunc {
	o := &verifyOptions{}
	fs.BoolVar(&o.heal, "heal", false, "repair the disc with Reed-Solomon parity before reporting; requires --out")
	fs.StringVar(&o.out, "out", "", "heal into this directory; required with --heal")
	return o.run
}

// run implements "noahsark verify DISC-ROOT". DISC-ROOT names a mounted
// disc, or a directory that holds a NOAHSARK tree, the same root that
// image.Read and restore.Heal accept.
//
// With a repository, verify records the check of the disc in the disc
// state log: a good check records the burn of a packed disc and the
// verified record, and a failed check removes one record.
// docs/states.md, rows 31 to 46, gives the lines. A heal records
// nothing: a healed tree is not a disc.
func (o *verifyOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	prog := e.progress()
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [--heal --out=DIR] DISC-ROOT")
		return 2
	}
	if o.heal && o.out == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify: --heal needs --out; healing in place is no longer supported")
		return 2
	}

	target := args[0]
	if o.heal {
		reports, err := restore.HealWithProgress(target, o.out, prog)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify: heal:", err)
			return 1
		}
		blocks := 0
		for _, r := range reports {
			blocks += len(r.DataColumns) + len(r.ParityColumns)
		}
		_, _ = fmt.Fprintf(stdout, "heal: repaired %d block(s)\n", blocks)
		target = o.out
	}

	ident, identOK := identifyDiscAndRun(target)
	rr, verifyErr := image.ReadWithProgress(target, prog)

	if !o.heal {
		if repoDir, err := e.findRepo(); err == nil {
			return recordVerify(e, repoDir, target, ident, identOK, rr, verifyErr)
		}
	}

	if verifyErr != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", verifyErr)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: %d items, ok\n", ident.short(), rr.ObjectsVerified)
	if o.heal {
		_, _ = fmt.Fprintln(stdout, "heal: burn the healed tree to a new disc, then verify that disc; healing alone does not verify or count as a copy")
	}
	return 0
}

// recordVerify records the check of target in the repository repoDir,
// and prints the lines of the check. verifyErr is the error of the full
// check, or nil for a good check.
func recordVerify(e *env, repoDir, target string, ident discIdentity, identOK bool, rr *image.ReadResult, verifyErr error) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "verify"
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	lk, code, ok := lockRepo(cmd, repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	if !identOK {
		_, _ = fmt.Fprintln(stdout, "verify: this tree is not a disc root; the state was not changed")
		if verifyErr != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, verifyErr)
		}
		return 1
	}

	layout := layoutOf(repoDir, cfg)
	logs, err := openLogs(cmd, layout, true, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	disc, known := logs.Discs.Disc(ident.DiscUUID)
	switch {
	case !known || disc.State == stage.DiscUndone || disc.State == stage.DiscUnknown:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: disc %s is not in this repository\n", cmd, uuidText(ident.DiscUUID))
		return 1
	case disc.State == stage.DiscLost:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: disc %d is marked lost\n", cmd, ident.DiscSeq)
		return 1
	case disc.State == stage.DiscMissing:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: disc %d is missing; give it to recover\n", cmd, ident.DiscSeq)
		return 1
	}

	now := e.now()
	if verifyErr != nil {
		if err := logs.Discs.Append(discEvent(now, ident.DiscUUID, stage.EventCheckFailed)); err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "%s: bad; %s\n", ident.short(), verifyFailedText(disc.State, ident.DiscSeq))
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, verifyErr)
		_, _ = fmt.Fprintln(stdout, nextStatusLine)
		return 1
	}

	// The catalog takes the tables and objects of the disc that it does
	// not hold yet, so that gc can confirm the items of the disc against
	// its INDEX without the disc.
	if err := catalogRunFromDisc(repoDir, target); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	var events []stage.DiscRecord
	if disc.State == stage.DiscPacked {
		events = append(events, discEvent(now, ident.DiscUUID, stage.EventBurnRecorded))
	}
	events = append(events, discEvent(now, ident.DiscUUID, stage.EventCheckOK))
	if err := logs.Discs.Append(events...); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: %d items, ok\n", ident.short(), len(logs.Items.ItemsOfDisc(ident.DiscUUID)))
	_, _ = fmt.Fprintln(stdout, verifyOKText(disc.State))
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	return 0
}

// verifyOKText is the line after the ok line of a good check of a disc
// in state from.
func verifyOKText(from stage.DiscState) string {
	switch from {
	case stage.DiscPacked:
		return "burn recorded; verified"
	case stage.DiscBurned:
		return "verified"
	case stage.DiscVerified:
		return "already verified; check logged"
	}
	return "check logged"
}

// verifyFailedText is the text after "bad; " of a failed check of the
// disc discSeq in state from.
func verifyFailedText(from stage.DiscState, discSeq uint64) string {
	switch from {
	case stage.DiscPacked:
		return "this disc is bad; no record to remove"
	case stage.DiscBurned:
		return "this disc is bad; burn record removed"
	case stage.DiscVerified:
		return "this disc is bad; verified record removed; gc holds the data"
	}
	return fmt.Sprintf("the staged copy is already freed; copy this disc now, or use your second copy, or run: noahsark disc lost %d", discSeq)
}

func labelText(b []byte) string {
	return string(b)
}

// discIdentity is DISC.bin's uuid and label, read straight off target
// without running the full object and FEC checks a verify performs. It
// lets a failed verify still name the disc that it checked.
type discIdentity struct {
	DiscUUID [16]byte
	DiscSeq  uint64
	Label    string
}

// short names the disc the way a message that reports a check does.
func (d discIdentity) short() string { return discNameShort(d.DiscSeq, d.Label) }

// identifyDiscAndRun reads DISC.bin and the newest run's RUN.bin under
// target, and reports the disc identity, and whether both were
// readable. A read failure here is not itself a verify failure; it only
// means the caller cannot name the disc of target.
func identifyDiscAndRun(target string) (discIdentity, bool) {
	names := image.NewNameCache()
	base, err := image.FindNoahsark(target, names)
	if err != nil {
		return discIdentity{}, false
	}
	discBuf, err := os.ReadFile(filepath.Join(base, names.Resolve(base, "DISC.bin")))
	if err != nil {
		return discIdentity{}, false
	}
	var disc format.Disc
	if err := disc.Decode(discBuf); err != nil {
		return discIdentity{}, false
	}

	label := labelText(disc.Label[:min(int(disc.LabelLen), len(disc.Label))])
	ident := discIdentity{DiscUUID: disc.DiscUUID, DiscSeq: disc.DiscSeq, Label: label}

	runsDir := filepath.Join(base, names.Resolve(base, "runs"))
	runDir, err := image.NewestRunDir(runsDir)
	if err != nil {
		return ident, false
	}
	runBuf, err := os.ReadFile(filepath.Join(runDir, names.Resolve(runDir, "RUN.bin")))
	if err != nil {
		return ident, false
	}
	var run format.Run
	if len(runBuf) < format.RunLen || run.Decode(runBuf[:format.RunLen]) != nil {
		return ident, false
	}
	return ident, true
}

// catalogRunFromDisc copies the tables and objects of target that the
// catalog does not hold into the catalog, the same way recover does.
func catalogRunFromDisc(repoDir, target string) error {
	c, err := catalog.Open(repoDir)
	if err != nil {
		return err
	}
	_, err = catalog.WriteFromRoot(c, target)
	return err
}
