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
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/restore"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// cmdVerify implements "noahsark verify DISC-ROOT". A drive mount needs
// root, which this build never assumes, so DISC-ROOT here names a
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
// object of that disc to CLEAN, adds 1 to the verify count of every
// CLEAN object of that disc, and warns when a PACKED object of that disc
// remains, naming the `disc burned` command to run. The second identical
// disc raises that count, and gc waits for it. A failed verify moves any
// object still at BURNED back to PACKED, with the verify-failed reason,
// and leaves the CLEAN objects alone.
func cmdVerify(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	fs := newFlagSet("noahsark verify [--repo=DIR] [--heal] [--out=DIR] DISC-ROOT",
		"Read a disc tree back and check it, optionally healing it first.", stderr)
	repoFlag := fs.String("repo", "", "repository directory, to update its staging state on a burned disc")
	heal := fs.Bool("heal", false, "repair the disc with Reed-Solomon parity before reporting")
	healOut := fs.String("out", "", "heal into this directory instead of in place")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("verify", fs, stderr) {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [--repo=DIR] [--heal] [--out=DIR] DISC-ROOT")
		return 2
	}
	imagePath := fs.Arg(0)

	target := imagePath
	if *heal {
		reports, err := restore.HealWithProgress(imagePath, *healOut, prog)
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

	// verify takes the repository lock only when --repo resolves: with no
	// --repo, verify never touches any repository's state, so there is
	// nothing to lock, the same way "image build" and a --repo-less "ls"
	// or "log" touch no repository.
	var trailingHint string
	if repoDir, err := discoverRepo(*repoFlag); err == nil {
		cfg, cfgErr := readConfig(configPath(repoDir))
		if cfgErr != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify:", cfgErr)
			return 1
		}
		lk, code, ok := lockExclusive("verify", repoDir, cfg.LockTimeout, stderr)
		if !ok {
			return code
		}

		hint, notInRepo := applyVerifyOutcome(repoDir, target, ident, identOK, verifyErr, stdout, stderr)
		if notInRepo {
			releaseLock(lk)
			_, _ = fmt.Fprintf(stderr, "noahsark: verify: disc %s (%s) is not in repository %s; check --repo, or run noahsark rebuild-cache --disc=%s to add it\n",
				uuidText(ident.DiscUUID), ident.Label, repoDir, target)
			return 1
		}
		releaseLock(lk)
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

// discIdentity is DISC.bin's uuid and label, read straight off target
// without running the full object and FEC checks a verify performs. It
// lets a failed verify still resolve which disc to move back to PACKED.
type discIdentity struct {
	DiscUUID [16]byte
	Label    string
}

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
	return discIdentity{DiscUUID: disc.DiscUUID, Label: label}, true
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
	warnIfTruncated("verify", stageLog, stderr)

	if verifyErr != nil {
		n := markVerifyFailed(stageLog, ident.DiscUUID)
		_, _ = fmt.Fprintf(stdout, "verify: disc %s failed; returned %d object(s) from BURNED to PACKED\n", uuidText(ident.DiscUUID), n)
		return "", false
	}

	n, err := markVerifyClean(stageLog, ident.DiscUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		return "", false
	}
	count, haveClean := discVerifyCount(stageLog, ident.DiscUUID)
	if haveClean {
		if err := recordLedgerVerify(cfg.StagingDir, repoUUID, ident.DiscUUID); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		}
		if err := cacheRunFromDisc(cfg, repoUUID, target); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify:", err)
		}
	}
	if n > 0 {
		_, _ = fmt.Fprintf(stdout, "verify: marked %d object(s) CLEAN (disc %s)\n", n, uuidText(ident.DiscUUID))
	}
	if haveClean {
		_, _ = fmt.Fprintln(stdout, verifyCountLine(count, cfg.MinVerifiedCopies))
	}

	stillPacked := countInState(stageLog, stage.Packed, ident.DiscUUID)
	if n == 0 && !haveClean && stillPacked == 0 {
		// Every object of the disc is on the disc alone: gc freed the
		// staged files, or rebuild-cache read the disc into an empty
		// staging. The verify still read every object back; there is
		// simply no staging state left to move.
		_, _ = fmt.Fprintf(stdout, "verify: disc %s holds no staged object; nothing to mark\n", uuidText(ident.DiscUUID))
		return "", false
	}
	if stillPacked == 0 {
		return "", false
	}
	row, found := ledgerRow(cfg.StagingDir, repoUUID, ident.DiscUUID)
	discSeq := uint64(0)
	if found {
		discSeq = row.DiscSeq
	}
	hintLine := fmt.Sprintf("verify: disc %d is not marked burned; run: noahsark disc burned --repo=%s %s",
		discSeq, repoDir, uuidText(ident.DiscUUID))
	if n > 0 || haveClean {
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

// ledgerRow returns stagingDir's ledger row for discUUID.
func ledgerRow(stagingDir string, repoUUID, discUUID [16]byte) (format.DiscsRow, bool) {
	ledger, err := image.LoadDiscsLedger(stagingDir, repoUUID)
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
// many objects it moved from BURNED. The operator verifies the second
// identical disc this way: the disc uuid is the same, so the count, not
// the state, is what says both copies read back. A PACKED object of the
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

// discVerifyCount returns the lowest verify count of the CLEAN objects
// of disc discUUID, and whether the disc has a CLEAN object at all. gc
// acts on the lowest count, so verify reports the same one.
func discVerifyCount(l *stage.Log, discUUID [16]byte) (uint8, bool) {
	var lowest uint8
	found := false
	for _, id := range idsOfDiscInState(l, stage.Clean, discUUID) {
		rec, ok := l.Get(id)
		if !ok {
			continue
		}
		if !found || rec.VerifyCount < lowest {
			lowest = rec.VerifyCount
			found = true
		}
	}
	return lowest, found
}

// verifyCountLine is the line verify prints for the verify count of a
// run: it tells the operator whether gc still waits for another copy.
func verifyCountLine(count uint8, minCopies int) string {
	if int(count) < minCopies {
		return fmt.Sprintf("verify: copy %d of %d verified; verify the second copy before gc", count, minCopies)
	}
	return fmt.Sprintf("verify: %d of %d copies verified", count, minCopies)
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

// recordLedgerVerify sets LastVerifySec to now on the disc ledger row
// for discUUID, and writes the ledger back. OPERATIONS.md's local cache
// layout also allows a health log; this build reuses the ledger row's
// own LastVerifySec field instead of adding a second file for the same
// fact.
func recordLedgerVerify(stagingDir string, repoUUID, discUUID [16]byte) error {
	ledger, err := image.LoadDiscsLedger(stagingDir, repoUUID)
	if err != nil {
		return err
	}
	found := false
	for i := range ledger.Rows {
		if ledger.Rows[i].DiscUUID == discUUID {
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
