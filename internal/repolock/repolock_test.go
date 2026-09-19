package repolock

import (
	"strings"
	"testing"
	"time"
)

// TestAcquireExclusiveFailsFastWhenHeld holds the lock, then checks that
// a second exclusive acquire with a zero timeout fails at once with a
// message naming the lock file and the holder's pid, matching
// OPERATIONS.md's "a command that cannot get its lock ... exits ...
// with a message that names the lock file and the holder's pid" rule.
func TestAcquireExclusiveFailsFastWhenHeld(t *testing.T) {
	dir := t.TempDir()
	held, err := AcquireExclusive(dir, 0)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer func() { _ = held.Release() }()

	start := time.Now()
	_, err = AcquireExclusive(dir, 0)
	if err == nil {
		t.Fatal("second acquire: want an error while the first holds the lock")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("second acquire took %v; a zero timeout must fail at once", elapsed)
	}
	if !strings.Contains(err.Error(), "lock") {
		t.Errorf("error %q does not name the lock file", err)
	}
	if !strings.Contains(err.Error(), "pid") {
		t.Errorf("error %q does not name the holder's pid", err)
	}
}

// TestLockFreeAfterRelease checks that releasing a lock lets a
// following acquire succeed, so a lock never outlives the command that
// took it.
func TestLockFreeAfterRelease(t *testing.T) {
	dir := t.TempDir()
	held, err := AcquireExclusive(dir, 0)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	again, err := AcquireExclusive(dir, 0)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	defer func() { _ = again.Release() }()
}

// TestAcquireExclusiveWaitsForTimeout checks that a nonzero timeout
// retries until the holder releases, instead of failing at once.
func TestAcquireExclusiveWaitsForTimeout(t *testing.T) {
	dir := t.TempDir()
	held, err := AcquireExclusive(dir, 0)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = held.Release()
		close(released)
	}()

	start := time.Now()
	second, err := AcquireExclusive(dir, time.Second)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer func() { _ = second.Release() }()
	<-released
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("second acquire returned after %v; it should have waited for the release", elapsed)
	}
}

// TestSharedLocksCoexist checks that two shared locks can both be held
// at once, matching a read-only command never blocking another
// read-only command.
func TestSharedLocksCoexist(t *testing.T) {
	dir := t.TempDir()
	a, err := AcquireShared(dir, 0)
	if err != nil {
		t.Fatalf("first shared acquire: %v", err)
	}
	defer func() { _ = a.Release() }()

	b, err := AcquireShared(dir, 0)
	if err != nil {
		t.Fatalf("second shared acquire: %v", err)
	}
	defer func() { _ = b.Release() }()
}

// TestExclusiveWaitsForShared checks that an exclusive acquire fails
// fast while a shared lock is held, matching a state-writing command
// never running alongside a read-only one.
func TestExclusiveWaitsForShared(t *testing.T) {
	dir := t.TempDir()
	shared, err := AcquireShared(dir, 0)
	if err != nil {
		t.Fatalf("shared acquire: %v", err)
	}
	defer func() { _ = shared.Release() }()

	if _, err := AcquireExclusive(dir, 0); err == nil {
		t.Fatal("exclusive acquire: want an error while a shared lock is held")
	}
}
