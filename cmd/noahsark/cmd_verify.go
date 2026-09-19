package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/restore"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// cmdVerify implements "noahsark verify --image=PATH". A drive mount
// needs root, which this build never assumes, so --image here names a
// mounted disc path or an unpacked NOAHSARK tree, the same root
// image.Read and restore.Heal already accept, instead of OPERATIONS.md's
// raw image file plus --mapfile. See docs/decisions.md,
// "16. CLI reference".
//
// verify never moves an object from PACKED to BURNED itself: the
// guide has an operator loop-mount and verify an image before it
// is burned, and that tree's disc uuid is already in the ledger (pack
// writes the ledger, not a burn step), so treating a ledger match alone
// as proof of burning would let verify, and then gc, act on a disc that
// does not exist yet. `noahsark disc burned UUID` is the explicit step
// that records a burn; verify only ever reads that state. When --repo
// resolves to a repository, and DISC.bin's uuid matches a row in that
// repository's disc ledger, a successful verify moves every BURNED
// object of the newest run to CLEAN, and warns when a PACKED object of
// that run remains, naming the `disc burned` command to run. A failed
// verify moves any object still at BURNED back to PACKED, with the
// verify-failed reason.
func cmdVerify(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if refuseNotYetImplementedFlags("verify", args, stderr) {
		return 2
	}

	fs := newFlagSet("noahsark verify [DISC-ROOT] [--repo=DIR] --image=PATH [--heal] [--out=DIR]",
		"Read a disc tree back and check it, optionally healing it first. DISC-ROOT and --image name the same thing; give only one.", stderr)
	repoFlag := fs.String("repo", "", "repository directory, to update its staging state on a burned disc")
	imagePath := fs.String("image", "", "mounted disc path or unpacked NOAHSARK tree")
	heal := fs.Bool("heal", false, "repair the disc with Reed-Solomon parity before reporting")
	healOut := fs.String("out", "", "heal into this directory instead of in place")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("verify", fs, stderr) {
		return 2
	}
	if fs.NArg() > 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [DISC-ROOT] [--repo=DIR] --image=PATH [--heal] [--out=DIR]")
		return 2
	}
	if fs.NArg() == 1 && *imagePath != "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify: give DISC-ROOT or --image, not both")
		return 2
	}
	if fs.NArg() == 1 {
		*imagePath = fs.Arg(0)
	}
	if *imagePath == "" {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [DISC-ROOT] [--repo=DIR] --image=PATH [--heal] [--out=DIR]")
		return 2
	}

	target := *imagePath
	if *heal {
		reports, err := restore.HealWithProgress(*imagePath, *healOut, prog)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify: heal:", err)
			return 1
		}
		for _, r := range reports {
			_, _ = fmt.Fprintf(stdout, "stripe %d: repaired data columns %v, parity columns %v\n", r.Stripe, r.DataColumns, r.ParityColumns)
		}
		_, _ = fmt.Fprintf(stdout, "heal: %d stripe(s) repaired\n", len(reports))
		if *healOut != "" {
			target = *healOut
		}
	}

	ident, identOK := identifyDiscAndRun(target)

	rr, verifyErr := image.ReadWithProgress(target, prog)

	var trailingHint string
	if repoDir, err := discoverRepo(*repoFlag); err == nil {
		hint, notInRepo := applyVerifyOutcome(repoDir, target, ident, identOK, verifyErr, stdout, stderr)
		if notInRepo {
			_, _ = fmt.Fprintf(stderr, "noahsark: verify: disc %s (%s) is not in repository %s; check --repo, or run noahsark rebuild-cache --from-disc --disc=%s to add it\n",
				uuidText(ident.DiscUUID), ident.Label, repoDir, target)
			return 1
		}
		trailingHint = hint
	}

	if verifyErr != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", verifyErr)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "disc label: %q\n", labelText(rr.Disc.Label[:rr.Disc.LabelLen]))
	_, _ = fmt.Fprintf(stdout, "disc capacity: %d sectors, forced %d sectors, capacity_is_forced=%d\n",
		rr.Disc.CapacitySectors, rr.Disc.CapacityForcedSectors, rr.Disc.CapacityIsForced)
	_, _ = fmt.Fprintf(stdout, "run: %d objects verified, %d run header copies\n", rr.ObjectsVerified, rr.RunCopies)
	_, _ = fmt.Fprintf(stdout, "refs: %d, discs: %d\n", len(rr.Refs.Records), len(rr.Discs.Rows))
	_, _ = fmt.Fprintln(stdout, "verify: ok")
	if trailingHint != "" {
		_, _ = fmt.Fprintln(stdout, trailingHint)
	}
	return 0
}

func labelText(b []byte) string {
	return string(b)
}

// discIdentity is DISC.bin's uuid and the newest run's run_seq and disc
// uuid, read straight off target without running the full object and
// FEC checks a verify performs. It lets a failed verify still resolve
// which run to move back to PACKED.
type discIdentity struct {
	DiscUUID [16]byte
	RunSeq   uint64
	Label    string
}

// identifyDiscAndRun reads DISC.bin and the newest run's RUN.bin under
// target, and reports the disc and run identity, and whether both were
// readable. A read failure here is not itself a verify failure; it only
// means the caller cannot resolve a run to move back to PACKED.
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
		return discIdentity{DiscUUID: disc.DiscUUID, Label: label}, false
	}
	runBuf, err := os.ReadFile(filepath.Join(runDir, names.Resolve(runDir, "RUN.bin")))
	if err != nil {
		return discIdentity{DiscUUID: disc.DiscUUID, Label: label}, false
	}
	var run format.Run
	if err := run.Decode(runBuf[:format.RunLen]); err != nil {
		return discIdentity{DiscUUID: disc.DiscUUID, Label: label}, false
	}
	return discIdentity{DiscUUID: disc.DiscUUID, RunSeq: run.RunSeq, Label: label}, true
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
	if !ledgerHasDiscUUID(cfg.StagingDir, repoUUID, ident.DiscUUID) {
		return "", true
	}

	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		return "", false
	}

	if verifyErr != nil {
		n := markVerifyFailed(stageLog, ident.DiscUUID, ident.RunSeq)
		_, _ = fmt.Fprintf(stdout, "verify: disc %s failed; returned %d object(s) from BURNED to PACKED\n", uuidText(ident.DiscUUID), n)
		return "", false
	}

	n, err := markVerifyClean(stageLog, ident.DiscUUID, ident.RunSeq)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		return "", false
	}
	if n > 0 {
		if err := recordLedgerVerify(cfg.StagingDir, repoUUID, ident.DiscUUID, ident.RunSeq); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		}
		if err := cacheRunFromDisc(cfg, repoUUID, target); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		}
		_, _ = fmt.Fprintf(stdout, "verify: marked %d object(s) CLEAN (disc %s, run %d)\n", n, uuidText(ident.DiscUUID), ident.RunSeq)
	}

	stillPacked := countInState(stageLog, stage.Packed, ident.DiscUUID, ident.RunSeq)
	if stillPacked == 0 {
		return "", false
	}
	row, found := ledgerRow(cfg.StagingDir, repoUUID, ident.DiscUUID, ident.RunSeq)
	discSeq := uint64(0)
	if found {
		discSeq = row.DiscSeq
	}
	hintLine := fmt.Sprintf("verify: disc %d is not marked burned; run: noahsark disc burned --repo=%s %s",
		discSeq, repoDir, uuidText(ident.DiscUUID))
	if n > 0 {
		_, _ = fmt.Fprintln(stdout, hintLine)
		return "", false
	}
	return hintLine, false
}

// ledgerHasDiscUUID reports whether stagingDir's disc ledger names a
// row for discUUID.
func ledgerHasDiscUUID(stagingDir string, repoUUID, discUUID [16]byte) bool {
	ledger, err := image.LoadDiscsLedger(stagingDir, repoUUID)
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

// ledgerRow returns stagingDir's ledger row for discUUID and runSeq.
func ledgerRow(stagingDir string, repoUUID, discUUID [16]byte, runSeq uint64) (format.DiscsRow, bool) {
	ledger, err := image.LoadDiscsLedger(stagingDir, repoUUID)
	if err != nil {
		return format.DiscsRow{}, false
	}
	for _, row := range ledger.Rows {
		if row.DiscUUID == discUUID && row.RunSeq == runSeq {
			return row, true
		}
	}
	return format.DiscsRow{}, false
}

// countInState counts the objects of run runSeq on disc discUUID that
// are currently in state.
func countInState(l *stage.Log, state stage.State, discUUID [16]byte, runSeq uint64) int {
	n := 0
	for _, id := range l.IDsInState(state) {
		rec, ok := l.Get(id)
		if ok && rec.RunSeq == runSeq && rec.DiscUUID == discUUID {
			n++
		}
	}
	return n
}

// markVerifyClean moves every object of run runSeq on disc discUUID
// that is at BURNED to CLEAN, and reports how many objects it moved. An
// object already CLEAN is left alone, so a repeat verify of an
// already-clean disc is a no-op. A PACKED object of the same run is
// left untouched: verify never marks anything BURNED itself, only
// `disc burned` does.
func markVerifyClean(l *stage.Log, discUUID [16]byte, runSeq uint64) (int, error) {
	n := 0
	for _, id := range l.IDsInState(stage.Burned) {
		rec, ok := l.Get(id)
		if !ok || rec.RunSeq != runSeq || rec.DiscUUID != discUUID {
			continue
		}
		if err := l.MarkClean(id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// markVerifyFailed moves every object of run runSeq on disc discUUID
// that is still only at BURNED back to PACKED, with the verify-failed
// reason, and reports how many objects it moved.
func markVerifyFailed(l *stage.Log, discUUID [16]byte, runSeq uint64) int {
	n := 0
	for _, id := range l.IDsInState(stage.Burned) {
		rec, ok := l.Get(id)
		if !ok || rec.RunSeq != runSeq || rec.DiscUUID != discUUID {
			continue
		}
		if err := l.MarkVerifyFailed(id); err == nil {
			n++
		}
	}
	return n
}

// recordLedgerVerify sets LastVerifySec to now on the disc ledger row
// for runSeq, and writes the ledger back. OPERATIONS.md's local cache
// layout also allows a health log; this build reuses the ledger row's
// own LastVerifySec field instead of adding a second file for the same
// fact.
func recordLedgerVerify(stagingDir string, repoUUID, discUUID [16]byte, runSeq uint64) error {
	ledger, err := image.LoadDiscsLedger(stagingDir, repoUUID)
	if err != nil {
		return err
	}
	found := false
	for i := range ledger.Rows {
		if ledger.Rows[i].RunSeq == runSeq && ledger.Rows[i].DiscUUID == discUUID {
			ledger.Rows[i].LastVerifySec = time.Now().Unix()
			found = true
		}
	}
	if !found {
		return nil
	}
	return image.SaveDiscsLedger(stagingDir, repoUUID, ledger.Rows)
}

// cacheRunFromDisc copies target's run catalog, snapshots and trees
// into the local cache, the same way rebuild-cache does, so gc can
// later confirm an object's presence through the cached INDEX without
// asking for the disc again.
func cacheRunFromDisc(cfg repoConfig, repoUUID [16]byte, target string) error {
	dir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		return err
	}
	c, err := cache.Open(dir)
	if err != nil {
		return err
	}
	_, err = cache.WriteFromRoot(c, target)
	return err
}
