package repolock

import (
	"strings"
	"testing"
	"time"
)

// TestAcquireFailsFastWhenHeld holds the lock, then checks that a second
// exclusive acquire fails at once with a message naming the lock file,
// matching the rule that a held lock is a failure at run time, not a
// wait.
func TestAcquireFailsFastWhenHeld(t *testing.T) {
	dir := t.TempDir()
	held, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer func() { _ = held.Release() }()

	start := time.Now()
	_, err = Acquire(dir)
	if err == nil {
		t.Fatal("second acquire: want an error while the first holds the lock")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("second acquire took %v; it must fail at once", elapsed)
	}
	if !strings.Contains(err.Error(), "lock") {
		t.Errorf("error %q does not name the lock file", err)
	}
}

// TestLockFreeAfterRelease checks that releasing a lock lets a
// following acquire succeed, so a lock never outlives the command that
// took it.
func TestLockFreeAfterRelease(t *testing.T) {
	dir := t.TempDir()
	held, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	defer func() { _ = again.Release() }()
}

// TestLockFreeAfterProcessEnd checks that a second open file
// description on the same lock file, standing in for a second process
// the way the kernel sees it, can take the lock once the first one
// closes: the kernel releases an flock the instant its holder's last
// file descriptor closes, even without an explicit Release call, which
// is what makes the lock safe across a crash.
func TestLockFreeAfterProcessEnd(t *testing.T) {
	dir := t.TempDir()
	held, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	if _, err := Acquire(dir); err == nil {
		t.Fatal("second acquire: want an error while the first holds the lock")
	}

	// Closing the file descriptor, not calling Release, is what stands
	// in for the holding process ending: the kernel drops the flock.
	if err := held.f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire after the holder's file closed: %v", err)
	}
	defer func() { _ = again.Release() }()
}
