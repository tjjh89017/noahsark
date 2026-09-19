package main

import (
	"fmt"
	"io"
	"time"

	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// lockExclusive takes the repository lock for a state-writing command,
// matching OPERATIONS.md's "a command that writes the state log, the
// staging store, the config, or a burn plan takes an exclusive advisory
// lock on it before it reads the state log" rule. On failure it prints
// the standard message on stderr and returns exit code 2, the code the
// design gives a command that cannot get its lock.
func lockExclusive(cmd, repoDir string, lockTimeout time.Duration, stderr io.Writer) (*repolock.Lock, int, bool) {
	lk, err := repolock.AcquireExclusive(repoDir, lockTimeout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, 2, false
	}
	return lk, 0, true
}

// lockShared takes the repository lock for a read-only command, for the
// span in which it reads the state log, matching the same section's
// "a read-only command takes a shared lock while it reads the state
// log" rule. On failure it prints the standard message on stderr and
// returns exit code 2.
func lockShared(cmd, repoDir string, lockTimeout time.Duration, stderr io.Writer) (*repolock.Lock, int, bool) {
	lk, err := repolock.AcquireShared(repoDir, lockTimeout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, 2, false
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
// after stage.Open succeeds.
func warnIfTruncated(cmd string, l *stage.Log, stderr io.Writer) {
	if truncated, ignoredBytes := l.Truncated(); truncated {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: the state log's tail was truncated; %d byte(s) after the last valid record were ignored, matching a crash during an earlier append\n", cmd, ignoredBytes)
	}
}
