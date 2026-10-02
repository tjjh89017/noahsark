package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

const verifyUsage = "verify [--no-mark] DISC-ROOT\nverify --undo DISC"

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
	undo   bool
}

func verifyFlags(fs *flag.FlagSet) runFunc {
	o := &verifyOptions{}
	fs.BoolVar(&o.noMark, "no-mark", false, "check the disc and write nothing")
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
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [--no-mark] DISC-ROOT")
		_, _ = fmt.Fprintln(stderr, "       noahsark verify --undo DISC")
		return 2
	}
	if o.undo {
		if o.noMark {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify: --undo takes no other option")
			return 2
		}
		return undoVerify(e, args[0])
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
	c.note = notCountedNoRepo
	c.reason = reasonDiscRootDamaged
	rr, checkErr := image.ReadWithProgress(root, e.progress())
	printNotices(e.stderr, "verify", rr)
	return c.report(rr, checkErr)
}

// verifyInRepo checks root with the repository at repoDir. It records
// the check only when root is a counted mount, and --no-mark is not
// given.
func (o *verifyOptions) verifyInRepo(e *env, repoDir string, layout repoLayout, root string, ident discIdentity) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "verify"

	verdict, err := countedMount(e, root, repoDir, layout.stagingDir())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	record := verdict == mountCounted && !o.noMark
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
	rr, checkErr := image.ReadWithProgress(root, e.progress())
	printNotices(stderr, cmd, rr)
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
	// its INDEX without the disc. The disc passed its check: a failure
	// here is a failure of the host, and the check is not recorded.
	if err := catalogRunFromDisc(repoDir, root, rr); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: the disc passed its check, but the catalog write failed: %v; the check is not recorded; correct the cause and run verify again\n", cmd, err)
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
// DISC.bin. It names the disc of a failed check too.
type discIdentity struct {
	DiscUUID [16]byte
	DiscSeq  uint64
	Label    string
}

// readDiscIdentity reads DISC.bin under root, without the object checks.
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
	return ident, nil
}

// printNotices prints each notice of the read rr to stderr. A nil rr
// prints nothing.
func printNotices(stderr io.Writer, cmd string, rr *image.ReadResult) {
	if rr == nil {
		return
	}
	for _, n := range rr.Notices {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, n)
	}
}

// catalogRunFromDisc copies the tables and objects of root that the
// catalog does not hold into the catalog, the same way recover does. rr
// is the check of root; it reads no chunk again.
func catalogRunFromDisc(repoDir, root string, rr *image.ReadResult) error {
	c, err := catalog.Open(repoDir)
	if err != nil {
		return err
	}
	_, err = catalog.WriteFromRead(c, root, rr)
	return err
}
