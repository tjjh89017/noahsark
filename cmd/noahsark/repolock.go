package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// lockRepo takes the repository's exclusive lock for a command that
// writes the state log, the staging store, the ledgers or the config,
// matching OPERATIONS.md's "Concurrency and locking". A held lock is a
// failure at run time: on failure this prints the standard message on
// stderr and returns exit code 1.
func lockRepo(cmd, repoDir string, stderr io.Writer) (*repolock.Lock, int, bool) {
	lk, err := repolock.Acquire(repoDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, 1, false
	}
	return lk, 0, true
}

// releaseLock releases lk, ignoring a nil lock so every caller can defer
// it unconditionally.
func releaseLock(lk *repolock.Lock) {
	_ = lk.Release()
}

// lockHeld reports whether a command holds the lock of the repository at
// repoDir now. It never creates the lock file, and it lets go of the
// lock at once.
func lockHeld(repoDir string) bool {
	f, err := os.Open(filepath.Join(repoDir, repolock.FileName))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// markFile is the sequence mark of the logs, in the staging directory.
func markFile(layout repoLayout) string {
	return filepath.Join(layout.stagingDir(), stage.MarkFileName)
}

// openLogs opens the item log and the disc state log of the repository
// for the command cmd, and prints the torn-tail warnings. holdsLock
// tells whether cmd holds the repository lock: a holder cuts a torn
// tail, and a command without the lock changes no file.
//
// A log that went back to an older version stops a holder of the lock
// with an error. A command without the lock prints a warning and goes
// on. A holder of the lock then writes the item records that an earlier
// command did not write after its disc event. A holder of the lock
// creates state/ when it does not exist, because each append of a log
// needs it. It leaves catalog/ to the commands that write the catalog. A
// command without the lock creates nothing.
func openLogs(cmd string, layout repoLayout, holdsLock bool, stderr io.Writer) (*stage.Logs, error) {
	if holdsLock {
		if err := mkdirDurable(layout.stateDir()); err != nil {
			return nil, err
		}
	}
	logs, err := stage.OpenLogs(layout.stateDir(), holdsLock)
	if err != nil {
		return nil, err
	}
	warnTornTails(cmd, logs, holdsLock, stderr)
	back, err := logs.UseMark(markFile(layout))
	if err != nil {
		return nil, err
	}
	if len(back) > 0 {
		if holdsLock {
			return nil, errors.New(rollbackText(back, markFile(layout)))
		}
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: warning: %s; this command changes no file\n", cmd, rollbackText(back, markFile(layout)))
	}
	if holdsLock {
		if err := completeItemRecords(cmd, layout, logs, stderr); err != nil {
			return nil, err
		}
	}
	return logs, nil
}

// warnRollback prints the warning of openLogs for a command without the
// lock that does not open both logs.
func warnRollback(cmd string, layout repoLayout, stderr io.Writer) {
	back, err := stage.MarkRollbacks(layout.stateDir(), markFile(layout))
	if err != nil || len(back) == 0 {
		return
	}
	_, _ = fmt.Fprintf(stderr, "noahsark: %s: warning: %s; this command changes no file\n", cmd, rollbackText(back, markFile(layout)))
}

// rollbackText says which log went back, and how to repair it.
func rollbackText(back []stage.Rollback, mark string) string {
	parts := make([]string, 0, len(back))
	for _, b := range back {
		parts = append(parts, fmt.Sprintf("the %s ends at record %d, but %s names record %d", b.Name, b.Last, mark, b.Marked))
	}
	return "state/ went back to an older version: " + strings.Join(parts, "; ") +
		"; put state/ and catalog/ forward again with git, or run recover with each disc into a new repository"
}

// completeItemRecords writes the item records that an earlier command
// did not write after its disc event, and prints one note for each disc.
func completeItemRecords(cmd string, layout repoLayout, logs *stage.Logs, stderr io.Writer) error {
	done, err := logs.Complete(catalogIndexItems(layout.repo))
	for _, r := range done {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s: an earlier %s stopped before it wrote the records of its items; %d item record(s) now written\n",
			cmd, repairDiscName(layout, r.Disc), r.Command, r.Items)
	}
	return err
}

// logRepairs returns each disc whose item records do not follow its
// state. It writes nothing. status calls it.
func logRepairs(layout repoLayout, logs *stage.Logs) ([]stage.Repair, error) {
	return logs.Repairs(catalogIndexItems(layout.repo))
}

// catalogIndexItems reads the items of a disc from its INDEX in the
// catalog of the repository at repo.
func catalogIndexItems(repo string) stage.IndexItems {
	return func(disc [16]byte) ([]object.ID, uint64, error) {
		c, err := catalog.OpenReadOnly(repo)
		if err != nil {
			return nil, 0, err
		}
		idx, err := c.IndexForDisc(disc)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, nil
		}
		if err != nil {
			return nil, 0, err
		}
		ids := make([]object.ID, 0, len(idx.Objects))
		for _, row := range idx.Objects {
			ids = append(ids, object.ID(row.ContentID))
		}
		return ids, idx.RunSeq, nil
	}
}

// repairDiscName names the disc d as disc SEQ "LABEL" from the disc
// ledger, or by its uuid when the ledger has no row of it.
func repairDiscName(layout repoLayout, d stage.DiscInfo) string {
	cfg, err := readConfig(layout.configFile())
	if err != nil {
		return "disc " + uuidText(d.UUID)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return "disc " + uuidText(d.UUID)
	}
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		return "disc " + uuidText(d.UUID)
	}
	row := newestDiscRow(ledger.Rows, d.UUID)
	if row.DiscUUID != d.UUID {
		return "disc " + uuidText(d.UUID)
	}
	return discNameShort(row.DiscSeq, labelText(row.Label[:row.LabelLen]))
}

// warnTornTails prints one warning line on stderr for each log whose
// open found a torn tail.
func warnTornTails(cmd string, logs *stage.Logs, holdsLock bool, stderr io.Writer) {
	for _, tail := range logs.TornTails() {
		warnTornTail(cmd, tail, holdsLock, stderr)
	}
}

// warnDiscLogTornTail prints the torn-tail warning of a disc state log
// that a command without the lock opened read-only.
func warnDiscLogTornTail(cmd string, l *stage.DiscLog, stderr io.Writer) {
	if n := l.TornBytes(); n > 0 {
		warnTornTail(cmd, stage.TornTail{Name: "disc state log", Path: l.Path(), Bytes: n}, false, stderr)
	}
}

// warnTornTail prints the warning of one torn tail. A holder of the lock
// found what a crash during an earlier append left. A command without
// the lock can also read the part of a record that another command
// writes at this time: the warning says so when the lock is held.
func warnTornTail(cmd string, tail stage.TornTail, holdsLock bool, stderr io.Writer) {
	if !holdsLock && lockHeld(filepath.Dir(filepath.Dir(tail.Path))) {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: the %s ends in a part of a record; %d byte(s) after the last valid record were ignored; another noahsark command writes the log at this time\n",
			cmd, tail.Name, tail.Bytes)
		return
	}
	verb := "ignored"
	if holdsLock {
		verb = "cut off"
	}
	_, _ = fmt.Fprintf(stderr, "noahsark: %s: the %s's tail was truncated; %d byte(s) after the last valid record were %s, matching a crash during an earlier append\n",
		cmd, tail.Name, tail.Bytes, verb)
}
