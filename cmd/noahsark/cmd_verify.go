package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
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

// run implements "noahsark verify DISC-ROOT". A drive mount needs
// root, which this build never assumes, so DISC-ROOT names a mounted
// disc path or an unpacked NOAHSARK tree, the same root image.Read and
// restore.Heal accept. See docs/decisions.md, "Burning and disc
// lifecycle".
//
// verify never moves an object from PACKED to BURNED itself: the
// guide has an operator loop-mount and verify an image before it
// is burned, and that tree's disc uuid is already in the ledger (pack
// writes the ledger, not a burn step), so treating a ledger match alone
// as proof of burning would let verify, and then gc, act on a disc that
// does not exist yet. `noahsark disc burned UUID` is the explicit step
// that records a burn; verify only ever reads that state. When discovery
// finds a repository, and the uuid in DISC.bin matches a row in that
// repository's disc ledger, a successful verify moves every BURNED
// object of that disc to CLEAN, adds 1 to the verify count of every
// CLEAN object of that disc, and warns when a PACKED object of that disc
// remains, naming the `disc burned` command to run. The second identical
// disc raises that count, and gc waits for it. A failed verify moves any
// object still at BURNED back to PACKED, with the verify-failed reason,
// and leaves the CLEAN objects alone.
func (o *verifyOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	prog := e.progress()
	heal, healOut := o.heal, o.out
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [--heal --out=DIR] DISC-ROOT")
		return 2
	}
	if heal && healOut == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify: --heal needs --out; healing in place is no longer supported")
		return 2
	}
	imagePath := args[0]

	target := imagePath
	if heal {
		reports, err := restore.HealWithProgress(imagePath, healOut, prog)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify: heal:", err)
			return 1
		}
		blocks := 0
		for _, r := range reports {
			blocks += len(r.DataColumns) + len(r.ParityColumns)
		}
		_, _ = fmt.Fprintf(stdout, "heal: repaired %d block(s)\n", blocks)
		target = healOut
	}

	ident, identOK := identifyDiscAndRun(target)

	rr, verifyErr := image.ReadWithProgress(target, prog)

	// verify takes the repository lock only when discovery finds a
	// repository. With none, verify never touches any state, so there is
	// nothing to lock, the same way "image build" and an "ls" or "log"
	// of a disc root touch no repository.
	// The state lines applyVerifyOutcome writes belong after the disc
	// line, not before it, so the report reads as one result. They are
	// collected here and written once the disc line is out.
	var outcome bytes.Buffer
	var trailingHint string
	if heal {
		// A healed tree lives on the hard disk, not on a disc. It is not
		// a copy that gc can rely on: only a verify of an
		// actual disc, burned from this healed tree, moves its objects
		// on or raises their verify count. Skip the whole repository
		// state update, whatever --repo names.
		trailingHint = "heal: burn the healed tree to a new disc, then verify that disc; healing alone does not verify or count as a copy"
	} else if repoDir, err := e.findRepo(); err == nil {
		if _, err := readConfig(configPath(repoDir)); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
			return 2
		}
		lk, code, ok := lockRepo("verify", repoDir, stderr)
		if !ok {
			return code
		}

		hint, notInRepo := applyVerifyOutcome(repoDir, target, ident, identOK, verifyErr, &outcome, stderr)
		if notInRepo {
			releaseLock(lk)
			_, _ = fmt.Fprintf(stderr, "noahsark: verify: %s is not in repository %s; check --repo, or run noahsark recover %s to add it\n",
				ident.name(), repoDir, target)
			return 1
		}
		releaseLock(lk)
		trailingHint = hint
	}

	if verifyErr != nil {
		_, _ = io.Copy(stdout, &outcome)
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", verifyErr)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "disc %d %q: %d objects, ok\n",
		rr.Disc.DiscSeq, labelText(rr.Disc.Label[:rr.Disc.LabelLen]), rr.ObjectsVerified)
	_, _ = io.Copy(stdout, &outcome)
	if trailingHint != "" {
		_, _ = fmt.Fprintln(stdout, trailingHint)
	}
	return 0
}

func labelText(b []byte) string {
	return string(b)
}

// discIdentity is DISC.bin's uuid and label, read straight off target
// without running the full object and FEC checks a verify performs. It
// lets a failed verify still resolve which disc to move back to PACKED.
type discIdentity struct {
	DiscUUID [16]byte
	DiscSeq  uint64
	Label    string
}

// name renders the disc the way every operator message names one.
func (d discIdentity) name() string { return discName(d.DiscSeq, d.Label, d.DiscUUID) }

// identifyDiscAndRun reads DISC.bin and the newest run's RUN.bin under
// target, and reports the disc identity, and whether both were
// readable. A read failure here is not itself a verify failure; it only
// means the caller cannot treat target as a burned disc.
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

	runsDir := filepath.Join(base, names.Resolve(base, "runs"))
	runDir, err := image.NewestRunDir(runsDir)
	if err != nil {
		return discIdentity{DiscUUID: disc.DiscUUID, DiscSeq: disc.DiscSeq, Label: label}, false
	}
	runBuf, err := os.ReadFile(filepath.Join(runDir, names.Resolve(runDir, "RUN.bin")))
	if err != nil {
		return discIdentity{DiscUUID: disc.DiscUUID, DiscSeq: disc.DiscSeq, Label: label}, false
	}
	var run format.Run
	if err := run.Decode(runBuf[:format.RunLen]); err != nil {
		return discIdentity{DiscUUID: disc.DiscUUID, DiscSeq: disc.DiscSeq, Label: label}, false
	}
	return discIdentity{DiscUUID: disc.DiscUUID, DiscSeq: disc.DiscSeq, Label: label}, true
}

// applyVerifyOutcome updates repoDir's staging state and disc ledger for
// a verify of target, when target's disc uuid is in the repository's
// disc ledger; it prints what it did along the way, or that it did
// nothing because target could not be identified as a disc at all.
// verifyErr is the error, if any, that the full verify reported; it is
// nil for a clean pass. When no object was BURNED, so nothing moved to
// CLEAN, it returns the not-marked-burned hint instead of printing it,
// so the caller can print that hint alone, after the "verify: ok" line
// rather than ahead of it.
//
// When target's disc is readable but its uuid names no row in repoDir's
// disc ledger, it reports notInRepo instead of doing anything: this
// disc belongs to some other repository, or repoDir's ledger has not
// heard of it, and neither case is the "never verified yet" case the
// not-a-burned-disc message covers.
func applyVerifyOutcome(repoDir, target string, ident discIdentity, identOK bool, verifyErr error, stdout, stderr io.Writer) (hint string, notInRepo bool) {
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		return "", false
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return "", false
	}

	if !identOK {
		_, _ = fmt.Fprintln(stdout, "verify: this tree is not a burned disc; the staging state was not changed")
		return "", false
	}
	layout := layoutOf(repoDir, cfg)
	if !ledgerHasDiscUUID(layout.discsLedgerFile(), repoUUID, ident.DiscUUID) {
		return "", true
	}

	stageLog, err := stage.Open(layout.stateDir())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		return "", false
	}
	warnIfTruncated("verify", stageLog, stderr)

	if verifyErr != nil {
		n := markVerifyFailed(stageLog, ident.DiscUUID)
		_, _ = fmt.Fprintf(stdout, "verify: %s failed; the burn mark is removed; %d object(s) returned to packed\n", ident.name(), n)
		_, _ = fmt.Fprintf(stdout, "next: burn a new disc from the same tree, then run: noahsark disc burned %d\n", ident.DiscSeq)
		return "", false
	}

	n, err := markVerifyClean(stageLog, ident.DiscUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		return "", false
	}
	haveClean := countInState(stageLog, stage.Clean, ident.DiscUUID) > 0
	if haveClean {
		if err := catalogRunFromDisc(repoDir, target); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		}
	}
	if n > 0 {
		_, _ = fmt.Fprintf(stdout, "verify: %d object(s) verified on %s\n", n, ident.name())
	}
	if haveClean {
		_, _ = fmt.Fprintln(stdout, "verify: verified")
	}

	stillPacked := countInState(stageLog, stage.Packed, ident.DiscUUID)
	if n == 0 && !haveClean && stillPacked == 0 {
		// Every object of the disc is on the disc alone: gc freed the
		// staged files, or recover read the disc into an empty
		// staging. The verify still read every object back; there is
		// simply no staging state left to move.
		_, _ = fmt.Fprintf(stdout, "verify: %s holds no staged object; nothing to mark\n", ident.name())
		return "", false
	}
	if stillPacked == 0 {
		return "", false
	}
	row, found := ledgerRow(layout.discsLedgerFile(), repoUUID, ident.DiscUUID)
	discSeq := uint64(0)
	if found {
		discSeq = row.DiscSeq
	}
	hintLine := fmt.Sprintf("verify: disc %d is not marked burned; run: noahsark disc burned %d", discSeq, discSeq)
	if n > 0 || haveClean {
		_, _ = fmt.Fprintln(stdout, hintLine)
		return "", false
	}
	return hintLine, false
}

// ledgerHasDiscUUID reports whether the disc ledger at ledgerPath names
// a row for discUUID.
func ledgerHasDiscUUID(ledgerPath string, repoUUID, discUUID [16]byte) bool {
	ledger, err := image.LoadDiscsLedger(ledgerPath, repoUUID)
	if err != nil {
		return false
	}
	for _, row := range ledger.Rows {
		if row.DiscUUID == discUUID {
			return true
		}
	}
	return false
}

// ledgerRow returns the row for discUUID of the disc ledger at
// ledgerPath.
func ledgerRow(ledgerPath string, repoUUID, discUUID [16]byte) (format.DiscsRow, bool) {
	ledger, err := image.LoadDiscsLedger(ledgerPath, repoUUID)
	if err != nil {
		return format.DiscsRow{}, false
	}
	for _, row := range ledger.Rows {
		if row.DiscUUID == discUUID {
			return row, true
		}
	}
	return format.DiscsRow{}, false
}

// countInState counts the objects of disc discUUID that are currently
// in state.
func countInState(l *stage.Log, state stage.State, discUUID [16]byte) int {
	n := 0
	for _, id := range l.IDsInState(state) {
		rec, ok := l.Get(id)
		if ok && rec.DiscUUID == discUUID {
			n++
		}
	}
	return n
}

// markVerifyClean moves every object of disc discUUID that is at
// BURNED to CLEAN with verify count 1, and adds 1 to the verify count
// of every object of that disc that is already CLEAN. It reports how
// many objects it moved from BURNED. A PACKED object of the
// same disc is left untouched: verify never marks anything BURNED
// itself, only `disc burned` does.
func markVerifyClean(l *stage.Log, discUUID [16]byte) (int, error) {
	burned := idsOfDiscInState(l, stage.Burned, discUUID)
	alreadyClean := idsOfDiscInState(l, stage.Clean, discUUID)
	for _, id := range append(burned, alreadyClean...) {
		if err := l.MarkVerified(id); err != nil {
			return 0, err
		}
	}
	return len(burned), nil
}

// idsOfDiscInState returns the ids of the objects of disc discUUID that
// are currently in state.
func idsOfDiscInState(l *stage.Log, state stage.State, discUUID [16]byte) []object.ID {
	var ids []object.ID
	for _, id := range l.IDsInState(state) {
		rec, ok := l.Get(id)
		if !ok || rec.DiscUUID != discUUID {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// markVerifyFailed moves every object of disc discUUID that is still
// only at BURNED back to PACKED, with the verify-failed reason, and
// reports how many objects it moved.
func markVerifyFailed(l *stage.Log, discUUID [16]byte) int {
	n := 0
	for _, id := range l.IDsInState(stage.Burned) {
		rec, ok := l.Get(id)
		if !ok || rec.DiscUUID != discUUID {
			continue
		}
		if err := l.MarkVerifyFailed(id); err == nil {
			n++
		}
	}
	return n
}

// catalogRunFromDisc copies target's run catalog, snapshots and trees
// into the catalog, the same way recover does, so gc can
// later confirm an object's presence through the catalog INDEX without
// asking for the disc again.
func catalogRunFromDisc(repoDir, target string) error {
	c, err := catalog.Open(repoDir)
	if err != nil {
		return err
	}
	_, err = catalog.WriteFromRoot(c, target)
	return err
}
