package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/restore"
	"github.com/tjjh89017/noahsark/internal/stage"
)

const verifyUsage = "verify [--no-mark] [--heal --out=DIR] DISC-ROOT\nverify --undo DISC"

func init() {
	register(&command{
		name:    "verify",
		usage:   verifyUsage,
		summary: "Check a disc root, and record the check of a counted mount; or remove a verified record with --undo.",
		flags:   verifyFlags,
	})
}

// verifyOptions holds the command options of verify.
type verifyOptions struct {
	noMark bool
	heal   bool
	out    string
	undo   bool
}

func verifyFlags(fs *flag.FlagSet) runFunc {
	o := &verifyOptions{}
	fs.BoolVar(&o.noMark, "no-mark", false, "check the disc and write nothing")
	fs.BoolVar(&o.heal, "heal", false, "repair the disc root with the parity of the run into --out, then check --out")
	fs.StringVar(&o.out, "out", "", "with --heal, the directory that receives the healed disc root")
	fs.BoolVar(&o.undo, "undo", false, "remove the verified record of a verified disc")
	return o.run
}

// Texts of the line after the ok line or the bad line.
const (
	notCountedDisc   = "not counted: this is not a disc"
	notCountedNoRepo = "not counted: no repository"
	notMarked        = "not marked"
)

// Reasons of a failed check that records nothing. The detail of the
// check goes to standard error.
const (
	reasonDiscRootDamaged   = "the disc root is damaged"
	reasonPackedTreeDamaged = "the packed tree is damaged"
)

// run implements "noahsark verify". docs/states.md, rows 31 to 51, gives
// the lines.
func (o *verifyOptions) run(e *env, args []string) int {
	stderr := e.stderr
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [--no-mark] [--heal --out=DIR] DISC-ROOT")
		_, _ = fmt.Fprintln(stderr, "       noahsark verify --undo DISC")
		return 2
	}
	if o.undo {
		if o.noMark || o.heal || o.out != "" {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify: --undo takes no other option")
			return 2
		}
		return undoVerify(e, args[0])
	}
	if o.heal && o.out == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify: --heal needs --out")
		return 2
	}
	if o.out != "" && !o.heal {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify: --out needs --heal")
		return 2
	}

	repoDir, err := e.findRepo()
	switch {
	case errors.Is(err, errNoRepo):
		repoDir = ""
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "noahsark: verify: %v\n", err)
		return 2
	}
	var cfg repoConfig
	if repoDir != "" {
		if cfg, err = readConfig(configPath(repoDir)); err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: verify: %v\n", err)
			return configExitCode(err)
		}
	}

	root := args[0]
	ident, err := readDiscIdentity(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: verify: %s: cannot read the disc: %v\n", root, err)
		return 1
	}
	if repoDir == "" {
		return o.verifyWithoutRepo(e, root, ident)
	}
	return o.verifyInRepo(e, repoDir, layoutOf(repoDir, cfg), root, ident)
}

// verifyWithoutRepo checks root with no repository. It records nothing
// and names the disc by its uuid.
func (o *verifyOptions) verifyWithoutRepo(e *env, root string, ident discIdentity) int {
	c := verifyCheck{
		e:     e,
		name:  fmt.Sprintf("disc %s %q", uuidText(ident.DiscUUID), ident.Label),
		short: "disc " + uuidText(ident.DiscUUID),
	}
	if o.heal {
		return c.heal(root, o.out, ident)
	}
	c.note = notCountedNoRepo
	c.reason = reasonDiscRootDamaged
	rr, checkErr := image.ReadWithProgress(root, e.progress())
	return c.report(rr, checkErr)
}

// verifyInRepo checks root with the repository at repoDir. It records
// the check only when root is a counted mount, and neither --no-mark
// nor --heal is given.
func (o *verifyOptions) verifyInRepo(e *env, repoDir string, layout repoLayout, root string, ident discIdentity) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "verify"

	verdict := mountNotMountPoint
	if !o.heal {
		v, err := countedMount(e, root, repoDir, layout.stagingDir())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
			return 1
		}
		verdict = v
	}
	record := verdict == mountCounted && !o.noMark && !o.heal
	if record {
		lk, code, ok := lockRepo(cmd, repoDir, stderr)
		if !ok {
			return code
		}
		defer releaseLock(lk)
	}
	logs, err := openLogs(cmd, layout, record, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}

	disc, known := logs.Discs.Disc(ident.DiscUUID)
	if refusal := verifyRefusal(disc, known, ident); refusal != "" {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, refusal)
		return 1
	}

	c := verifyCheck{
		e:     e,
		name:  discNameShort(ident.DiscSeq, ident.Label),
		short: fmt.Sprintf("disc %d", ident.DiscSeq),
	}
	if o.heal {
		return c.heal(root, o.out, ident)
	}

	rr, checkErr := image.ReadWithProgress(root, e.progress())
	if changed := discChangedRefusal(root, ident, rr); changed != "" {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, changed)
		return 1
	}
	if !record {
		c.reason = reasonDiscRootDamaged
		if isPackedTree(e, layout, root, ident.DiscUUID) {
			c.reason = reasonPackedTreeDamaged
		}
		switch {
		case verdict != mountCounted:
			c.note = notCountedDisc
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s is not counted: %s\n", cmd, root, verdict)
		case disc.State == stage.DiscPacked && checkErr == nil:
			c.note = fmt.Sprintf("%s; to record this burn, run: noahsark disc burned %d", notMarked, ident.DiscSeq)
		default:
			c.note = notMarked
		}
		return c.report(rr, checkErr)
	}

	now := e.now()
	if checkErr != nil {
		if err := logs.Discs.Append(discEvent(now, ident.DiscUUID, stage.EventCheckFailed)); err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "%s: bad; %s\n", c.name, verifyFailedText(disc.State, ident.DiscSeq))
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, checkErr)
		_, _ = fmt.Fprintln(stdout, nextStatusLine)
		return 1
	}

	// The catalog takes the tables and objects of the disc that it does
	// not hold yet, so that gc can confirm the items of the disc against
	// its INDEX without the disc.
	if err := catalogRunFromDisc(repoDir, root); err != nil {
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
	_, _ = fmt.Fprintf(stdout, "%s: %d items, ok\n", c.name, rr.ObjectsVerified)
	_, _ = fmt.Fprintln(stdout, verifyOKText(disc.State))
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	return 0
}

// verifyRefusal returns the refusal of verify for the disc of ident, or
// an empty string when verify can check it. An undone disc is out of the
// repository.
func verifyRefusal(disc stage.DiscInfo, known bool, ident discIdentity) string {
	switch {
	case !known || disc.State == stage.DiscUndone || disc.State == stage.DiscUnknown:
		return fmt.Sprintf("disc %s is not in this repository", uuidText(ident.DiscUUID))
	case disc.State == stage.DiscLost:
		return fmt.Sprintf("disc %d is marked lost", ident.DiscSeq)
	case disc.State == stage.DiscMissing:
		return fmt.Sprintf("disc %d is missing; give it to recover", ident.DiscSeq)
	}
	return ""
}

// discChangedRefusal returns the refusal of a check whose disc is not the
// disc of ident, or an empty string. The disc uuid after the check comes
// from rr, or from a new read of DISC.bin when the check failed. A disc
// root whose DISC.bin cannot be read again is not refused here.
func discChangedRefusal(root string, ident discIdentity, rr *image.ReadResult) string {
	after := ident.DiscUUID
	if rr != nil {
		after = rr.Disc.DiscUUID
	} else if again, err := readDiscIdentity(root); err == nil {
		after = again.DiscUUID
	}
	if after == ident.DiscUUID {
		return ""
	}
	return fmt.Sprintf("%s: the disc changed during the check: disc %s before, disc %s after; nothing is recorded",
		root, uuidText(ident.DiscUUID), uuidText(after))
}

// verifyCheck prints the lines of a check that records nothing.
type verifyCheck struct {
	e *env
	// name is the disc name of the ok line and the bad line. short is
	// the disc name of a refusal.
	name  string
	short string
	// note is the line after the ok line or the bad line.
	note string
	// reason is the text after "bad; " of a failed check.
	reason string
}

// report prints the lines of the check result rr and checkErr, and
// returns the exit code.
func (c verifyCheck) report(rr *image.ReadResult, checkErr error) int {
	stdout := c.e.stdout
	if checkErr != nil {
		_, _ = fmt.Fprintf(stdout, "%s: bad; %s\n", c.name, c.reason)
		_, _ = fmt.Fprintln(stdout, c.note)
		_, _ = fmt.Fprintf(c.e.stderr, "noahsark: verify: %v\n", checkErr)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: %d items, ok\n", c.name, rr.ObjectsVerified)
	_, _ = fmt.Fprintln(stdout, c.note)
	return 0
}

// heal writes the healed disc root of root into out and checks out. A
// healed tree is never a counted mount, so heal records nothing.
func (c verifyCheck) heal(root, out string, ident discIdentity) int {
	e := c.e
	if ident.RunRead && !ident.FEC {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: verify: %s has no FEC; --heal needs a disc with FEC\n", c.short)
		return 1
	}
	reports, err := restore.HealWithProgress(root, out, e.progress())
	if err == nil {
		var files int
		files, err = healedFileCount(out, reports)
		if err == nil {
			_, _ = fmt.Fprintf(e.stdout, "%s: healed %d file(s) into %s\n", c.name, files, out)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(e.stdout, "%s: bad; cannot heal; %v\n", c.name, err)
		return 1
	}
	c.note = notCountedDisc
	c.reason = reasonDiscRootDamaged
	rr, checkErr := image.ReadWithProgress(out, e.progress())
	return c.report(rr, checkErr)
}

// healedFileCount counts the files of the disc root dir that the repair
// reports changed: the stream files that a repaired data block falls in,
// and the parity files of a repaired parity block.
func healedFileCount(dir string, reports []restore.StripeReport) (int, error) {
	if len(reports) == 0 {
		return 0, nil
	}
	cache := image.NewNameCache()
	base, err := image.FindNoahsark(dir, cache)
	if err != nil {
		return 0, err
	}
	runDir, err := image.NewestRunDir(cache.Join(base, "runs"))
	if err != nil {
		return 0, err
	}
	_, sizes, _, err := image.StreamFilesWithCache(base, runDir, cache)
	if err != nil {
		return 0, err
	}
	layout, err := fec.NewStreamLayout(sizes, fec.K)
	if err != nil {
		return 0, err
	}
	stripes := layout.StripeCount()
	streamFiles := map[int]bool{}
	parityFiles := map[int]bool{}
	for _, r := range reports {
		for _, col := range r.DataColumns {
			block := uint64(col)*stripes + r.Stripe
			if block >= layout.BlockCount() {
				continue
			}
			idx, off, err := layout.Locate(block)
			if err != nil {
				return 0, err
			}
			if off < sizes[idx] {
				streamFiles[idx] = true
			}
		}
		for _, j := range r.ParityColumns {
			parityFiles[j] = true
		}
	}
	return len(streamFiles) + len(parityFiles), nil
}

// isPackedTree tells whether root is the disc root that pack wrote for
// the disc discUUID: the tree under staging, or the target of its
// symlink for a pack --out disc.
func isPackedTree(e *env, layout repoLayout, root string, discUUID [16]byte) bool {
	resolved, err := resolvePath(e, root)
	if err != nil {
		return false
	}
	tree, err := filepath.EvalSymlinks(layout.planTree(discUUID))
	if err != nil {
		return false
	}
	return resolved == tree
}

// undoVerify implements "verify --undo DISC": it removes the verified
// record of a verified disc after an ordinary confirmation. It reads no
// disc.
func undoVerify(e *env, arg string) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "verify"
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
	discUUID, err := resolveDisc(ledger.Rows, logs.Discs, arg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	disc := discTargetOf(ledger.Rows, logs.Discs, discUUID)

	switch disc.info.State {
	case stage.DiscVerified:
	case stage.DiscOnDiscOnly:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s is on disc only; gc already freed the staged copy; verify cannot be undone\n", cmd, disc.short())
		return 1
	case stage.DiscPacked, stage.DiscBurned:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s has no verified record\n", cmd, disc.short())
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, discStateRefusal(disc))
		return 1
	}
	warning := []string{
		disc.warning(stage.DiscBurned),
		"the disc is no longer verified, and gc holds its data",
	}
	if !e.confirm(confirmOrdinary, "verify --undo", warning) {
		return 1
	}
	if err := logs.Discs.Append(discEvent(e.now(), discUUID, stage.EventVerifyUndone)); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: verified record removed; burn record kept\n", disc.name())
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	return 0
}

// verifyOKText is the line after the ok line of a good counted check of
// a disc in state from.
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

// verifyFailedText is the text after "bad; " of a failed counted check
// of the disc discSeq in state from.
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

// discIdentity is the uuid, the number and the label of a disc from its
// DISC.bin, and the FEC flag of its newest run. It names the disc of a
// failed check too.
type discIdentity struct {
	DiscUUID [16]byte
	DiscSeq  uint64
	Label    string
	// RunRead is true when the RUN.bin of the newest run decoded. FEC is
	// valid only then.
	RunRead bool
	FEC     bool
}

// readDiscIdentity reads DISC.bin and the RUN.bin of the newest run
// under root, without the object and FEC checks. Damage to RUN.bin only
// clears RunRead: the full check reports it.
func readDiscIdentity(root string) (discIdentity, error) {
	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		return discIdentity{}, err
	}
	discBuf, err := os.ReadFile(filepath.Join(base, names.Resolve(base, "DISC.bin")))
	if err != nil {
		return discIdentity{}, fmt.Errorf("DISC.bin: %w", err)
	}
	var disc format.Disc
	if err := disc.Decode(discBuf); err != nil {
		return discIdentity{}, fmt.Errorf("DISC.bin: %w", err)
	}
	ident := discIdentity{
		DiscUUID: disc.DiscUUID,
		DiscSeq:  disc.DiscSeq,
		Label:    labelText(disc.Label[:min(int(disc.LabelLen), len(disc.Label))]),
	}

	runDir, err := image.NewestRunDir(filepath.Join(base, names.Resolve(base, "runs")))
	if err != nil {
		return ident, nil
	}
	runBuf, err := os.ReadFile(filepath.Join(runDir, names.Resolve(runDir, "RUN.bin")))
	if err != nil || len(runBuf) < format.RunLen {
		return ident, nil
	}
	var run format.Run
	if run.Decode(runBuf[:format.RunLen]) != nil {
		return ident, nil
	}
	ident.RunRead = true
	ident.FEC = run.FECScheme == format.FECSchemeRS255GF8
	return ident, nil
}

// catalogRunFromDisc copies the tables and objects of root that the
// catalog does not hold into the catalog, the same way recover does.
func catalogRunFromDisc(repoDir, root string) error {
	c, err := catalog.Open(repoDir)
	if err != nil {
		return err
	}
	_, err = catalog.WriteFromRoot(c, root)
	return err
}
