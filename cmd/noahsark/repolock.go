package main

import (
	"fmt"
	"io"

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

// openLogs opens the item log and the disc state log of the repository
// for the command cmd, and prints the torn-tail warnings. holdsLock
// tells whether cmd holds the repository lock: a holder cuts a torn
// tail, and a command without the lock changes no file.
func openLogs(cmd string, layout repoLayout, holdsLock bool, stderr io.Writer) (*stage.Logs, error) {
	logs, err := stage.OpenLogs(layout.stateDir(), holdsLock)
	if err != nil {
		return nil, err
	}
	warnTornTails(cmd, logs, holdsLock, stderr)
	return logs, nil
}

// warnTornTails prints one warning line on stderr for each log whose
// open found a torn tail: a crash during an earlier append leaves one.
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

func warnTornTail(cmd string, tail stage.TornTail, holdsLock bool, stderr io.Writer) {
	verb := "ignored"
	if holdsLock {
		verb = "cut off"
	}
	_, _ = fmt.Fprintf(stderr, "noahsark: %s: the %s's tail was truncated; %d byte(s) after the last valid record were %s, matching a crash during an earlier append\n",
		cmd, tail.Name, tail.Bytes, verb)
}
