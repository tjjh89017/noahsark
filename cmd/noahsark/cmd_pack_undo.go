package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// packUndoUsage is the usage line of pack --undo.
const packUndoUsage = "usage: noahsark pack --undo DISC"

// hasPackOption reports whether the call gave an option of pack other
// than --undo.
func (o *packOptions) hasPackOption() bool {
	return o.capacity != "" || o.outDir != "" || o.closeDisc || o.dryRun
}

// runUndo implements "noahsark pack --undo DISC". It returns the items
// of the newest disc to Staged while that disc is packed. docs/states.md,
// rows 11 to 14, gives the messages.
func (o *packOptions) runUndo(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	if o.hasPackOption() {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack: --undo takes no other option")
		_, _ = fmt.Fprintln(stderr, packUndoUsage)
		return 2
	}
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, packUndoUsage)
		return 2
	}

	s, code, ok := e.openLockedSession("pack")
	if !ok {
		return code
	}
	defer s.close()
	disc, code, ok := s.disc(args[0])
	if !ok {
		return code
	}
	repoDir, layout, repoUUID := s.repoDir, s.layout, s.repoUUID
	logs, ledger, discUUID := s.logs, s.ledger, disc.info.UUID
	if refusal := packUndoRefusal(disc, ledger.Rows, logs.Discs); refusal != "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", refusal)
		return 1
	}

	warning := []string{
		disc.warning(stage.DiscUndone),
		fmt.Sprintf("the items return to staged; the disc number %d is not used again", disc.seq),
	}
	if !e.confirm(confirmOrdinary, "pack --undo", warning) {
		return 1
	}

	c, err := catalog.Open(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	// The PackUndone event goes first: it takes the disc out of the
	// repository. The item records follow it. A stop after the event
	// leaves item records that the next command with the lock writes, and
	// files that the next pack removes.
	if err := logs.Discs.Append(discEvent(e.now(), discUUID, stage.EventPackUndone)); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	items, err := logs.CompleteDisc(discUUID, nil, nil)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	keptRoot, err := removeUndoneDisc(layout, c, repoUUID, discUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: %s is undone, but its files stay: %v; the next pack removes them\n", disc.short(), err)
		// The disc state changed, thus the next block comes before the exit.
		printNext(e, repoDir)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "%s: pack undone, %d item(s) returned to staged\n", disc.name(), items)
	if keptRoot != "" {
		_, _ = fmt.Fprintf(stdout, "disc root %s kept; delete it yourself\n", keptRoot)
	}
	printNext(e, repoDir)
	return 0
}

// packUndoRefusal returns the refusal of pack --undo for disc, or an
// empty string when the disc is packed and is the newest disc.
func packUndoRefusal(disc discTarget, rows []format.DiscsRow, discs *stage.DiscLog) string {
	switch disc.info.State {
	case stage.DiscPacked:
		if !isNewestDisc(disc, rows, discs) {
			return disc.short() + " is not the newest disc; pack cannot be undone"
		}
		return ""
	case stage.DiscBurned:
		return fmt.Sprintf("%s has a burn record; pack cannot be undone; when no burn happened, run noahsark disc burned --undo %d first", disc.short(), disc.seq)
	case stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscLost, stage.DiscMissing:
		refusal := disc.short() + " is no longer packed; pack cannot be undone"
		if u, seq, ok := newestDisc(rows, discs); ok {
			if d, known := discs.Disc(u); known && d.State == stage.DiscPacked {
				refusal += fmt.Sprintf("; the newest packed disc is %d", seq)
			}
		}
		return refusal
	}
	return disc.short() + " has no record in the disc state log; pack cannot be undone"
}

// isNewestDisc reports whether no other disc has a disc number as high
// as the number of disc.
func isNewestDisc(disc discTarget, rows []format.DiscsRow, discs *stage.DiscLog) bool {
	newest := true
	eachDiscNumber(rows, discs, func(u [16]byte, seq uint64) {
		if u != disc.info.UUID && seq >= disc.seq {
			newest = false
		}
	})
	return newest
}

// newestDisc returns the uuid and the number of the disc with the
// highest disc number. ok is false when the repository has no disc.
func newestDisc(rows []format.DiscsRow, discs *stage.DiscLog) (uuid [16]byte, seq uint64, ok bool) {
	eachDiscNumber(rows, discs, func(u [16]byte, s uint64) {
		if !ok || s > seq {
			uuid, seq, ok = u, s, true
		}
	})
	return uuid, seq, ok
}

// eachDiscNumber calls fn with the uuid and the disc number of each disc.
// The numbers come from the rows of the disc ledger and from the Packed
// events. An undone disc does not count. A disc can come more than once.
func eachDiscNumber(rows []format.DiscsRow, discs *stage.DiscLog, fn func(u [16]byte, seq uint64)) {
	undone := func(u [16]byte) bool {
		d, ok := discs.Disc(u)
		return ok && d.State == stage.DiscUndone
	}
	for _, r := range rows {
		if !undone(r.DiscUUID) {
			fn(r.DiscUUID, r.DiscSeq)
		}
	}
	for _, d := range discs.Discs() {
		// A Packed event always has a run_seq of 1 or more. A disc with
		// a run_seq of 0 has no Packed event, and its number is in the
		// ledger rows.
		if d.RunSeq > 0 && d.State != stage.DiscUndone {
			fn(d.UUID, d.DiscSeq)
		}
	}
}

// removeUndoneDisc removes the files of the undone disc discUUID in this
// order: its disc ledger row, its catalog tables, and its plan directory.
// Each step does nothing when its file is already gone. For a pack
// --out=DIR disc, the plan tree is a symlink to DIR. removeUndoneDisc
// removes the symlink, keeps DIR, and returns the path of DIR.
func removeUndoneDisc(layout repoLayout, c *catalog.Catalog, repoUUID, discUUID [16]byte) (keptRoot string, err error) {
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		return "", err
	}
	isDisc := func(r format.DiscsRow) bool { return r.DiscUUID == discUUID }
	if slices.ContainsFunc(ledger.Rows, isDisc) {
		rows := slices.DeleteFunc(slices.Clone(ledger.Rows), isDisc)
		if err := image.SaveDiscsLedger(layout.discsLedgerFile(), repoUUID, rows); err != nil {
			return "", err
		}
	}
	if err := c.RemoveDisc(discUUID); err != nil {
		return "", err
	}
	if fi, err := os.Lstat(layout.planTree(discUUID)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if keptRoot, err = os.Readlink(layout.planTree(discUUID)); err != nil {
			return "", err
		}
	}
	if err := os.RemoveAll(layout.planDir(discUUID)); err != nil {
		return "", err
	}
	return keptRoot, nil
}

// finishUndonePacks finishes each pack --undo that stopped before it
// removed the files of its disc. openLogs already wrote the item records
// of each undone disc. For each undone disc, finishUndonePacks removes
// the files that removeUndoneDisc removes. pack calls it before it
// writes a disc, thus the DISCS table of a new disc never names an
// undone disc.
func finishUndonePacks(layout repoLayout, c *catalog.Catalog, repoUUID [16]byte, logs *stage.Logs, stderr io.Writer) error {
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		return err
	}
	for _, d := range logs.Discs.InState(stage.DiscUndone) {
		inLedger := slices.ContainsFunc(ledger.Rows, func(r format.DiscsRow) bool { return r.DiscUUID == d.UUID })
		if !inLedger && !exists(filepath.Join(c.Dir(), "discs", uuidText(d.UUID))) && !exists(layout.planDir(d.UUID)) {
			continue
		}
		keptRoot, err := removeUndoneDisc(layout, c, repoUUID, d.UUID)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: disc %d: an earlier pack --undo stopped before it removed the records of the disc; they are now removed\n", d.DiscSeq)
		if keptRoot != "" {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: disc root %s kept; delete it yourself\n", keptRoot)
		}
	}
	return nil
}

// exists reports whether path names a file, a directory or a symlink.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}
