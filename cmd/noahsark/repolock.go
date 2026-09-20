package main

import (
	"fmt"
	"io"

	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// lockRepo takes the repository's exclusive lock for a command that
// writes the state log, the staging store, the ledgers or the config,
// matching OPERATIONS.md's "a command that writes the state log, the
// staging store, the ledgers or the config takes an exclusive advisory
// lock on it before it reads the state log" rule. A held lock is a
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

// warnIfTruncated prints one warning line on stderr when l's state log
// tail was truncated: OPERATIONS.md requires the tool to report a
// truncated log, and every command that opens the log calls this right
// after stage.Open or stage.OpenReadOnly succeeds.
func warnIfTruncated(cmd string, l *stage.Log, stderr io.Writer) {
	if truncated, ignoredBytes := l.Truncated(); truncated {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: the state log's tail was truncated; %d byte(s) after the last valid record were ignored, matching a crash during an earlier append\n", cmd, ignoredBytes)
	}
}
