package main

import (
	"fmt"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

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
			c.note = fmt.Sprintf("%s (%s)", notCountedDisc, verdict)
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
		printNext(e, repoDir)
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
	printNext(e, repoDir)
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
