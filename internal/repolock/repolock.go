// Package repolock implements the repository lock OPERATIONS.md's
// concurrency and locking rules require: a state-writing command takes
// a non-blocking exclusive advisory lock on <repo>/lock before it reads
// the state log, and holds it until it exits. The lock is an flock held
// on an open file, so it is released the instant the holding process
// exits, even after a crash. Nothing about the lock file's own existence
// or content is the lock; a stale file left behind by a dead process
// never blocks the next command.
package repolock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FileName is the repository lock file's name, inside the repository
// directory, matching OPERATIONS.md's concurrency and locking rules.
const FileName = "lock"

// Lock is one held lock on a repository's lock file.
type Lock struct {
	f *os.File
}

// Acquire takes a non-blocking exclusive lock on repoDir's lock file,
// for a command that writes the state log, the staging store, the
// ledgers or the config. It fails at once when another command already
// holds the lock, instead of waiting.
func Acquire(repoDir string) (*Lock, error) {
	path := filepath.Join(repoDir, FileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("repository lock %s: %w", path, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("repository lock %s is held; another noahsark command runs on this repository", path)
		}
		return nil, fmt.Errorf("repository lock %s: %w", path, err)
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
