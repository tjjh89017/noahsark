// Package repolock implements the repository lock OPERATIONS.md's
// concurrency and locking rules require: a state-writing command takes
// an exclusive advisory lock on <repo>/lock before it reads the state
// log, and holds it until it exits; a read-only command takes a shared
// lock while it reads the state log. The lock is an flock held on an
// open file, so it is released the instant the holding process exits,
// even after a crash. Nothing about the lock file's own existence or
// content is the lock; a stale file left behind by a dead process never
// blocks the next command.
package repolock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FileName is the repository lock file's name, inside the repository
// directory, matching OPERATIONS.md's concurrency and locking rules.
const FileName = "lock"

// pollInterval is how often a waiting acquire retries the lock while a
// nonzero timeout has not yet passed.
const pollInterval = 50 * time.Millisecond

// Lock is one held lock on a repository's lock file.
type Lock struct {
	f *os.File
}

// AcquireExclusive takes an exclusive lock on repoDir's lock file, for a
// command that writes the state log, the staging store, the config, or
// a burn plan. It waits up to timeout for a competing lock to clear,
// trying repeatedly, and fails at once when timeout is 0, matching
// repo.lock_timeout's default of "fail at once".
func AcquireExclusive(repoDir string, timeout time.Duration) (*Lock, error) {
	return acquire(repoDir, syscall.LOCK_EX, timeout)
}

// AcquireShared takes a shared lock on repoDir's lock file, for a
// read-only command, while it reads the state log.
func AcquireShared(repoDir string, timeout time.Duration) (*Lock, error) {
	return acquire(repoDir, syscall.LOCK_SH, timeout)
}

func acquire(repoDir string, how int, timeout time.Duration) (*Lock, error) {
	path := filepath.Join(repoDir, FileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("repository lock %s: %w", path, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		flockErr := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if flockErr == nil {
			break
		}
		if !errors.Is(flockErr, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("repository lock %s: %w", path, flockErr)
		}
		if time.Now().After(deadline) {
			holder := readHolderPID(path)
			_ = f.Close()
			if holder != "" {
				return nil, fmt.Errorf("repository lock %s is held by pid %s; wait for the other noahsark command to end", path, holder)
			}
			return nil, fmt.Errorf("repository lock %s is held by another process; wait for the other noahsark command to end", path)
		}
		time.Sleep(pollInterval)
	}

	// Record the pid holding the lock, so a command that fails to
	// acquire it can name the holder, matching the rule that "the
	// holder writes its pid into the file". Only the exclusive holder
	// writes its pid: several shared holders could otherwise overwrite
	// each other's record, and the pid is only ever needed to explain a
	// wait on an exclusive holder.
	if how == syscall.LOCK_EX {
		if err := f.Truncate(0); err == nil {
			_, _ = f.Seek(0, 0)
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
		}
	}

	return &Lock{f: f}, nil
}

// Release lets go of the lock and closes the lock file. It never
// removes the file: the file is not the lock, the flock held on its
// open file description is, and deleting it would leave a competing
// acquire watching a different inode than a third command that opened
// the file first.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// readHolderPID reads the pid an exclusive holder wrote into path, or
// "" when the file holds nothing recognizable as one.
func readHolderPID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err != nil {
		return ""
	}
	return s
}
