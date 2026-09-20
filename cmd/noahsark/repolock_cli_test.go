package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/repolock"
)

// TestGCFailsFastWhenRepoLockHeld checks bug 1: gc must refuse to run
// while another command holds the repository's exclusive lock, instead
// of replaying a state log another process may change underneath it.
// It asserts the clear message and the exit code OPERATIONS.md's
// concurrency and locking rules give a command that cannot get its
// lock.
func TestGCFailsFastWhenRepoLockHeld(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	held, err := repolock.AcquireExclusive(repo, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 2 {
		t.Fatalf("gc while locked: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("gc while locked output %q, want it to name the repository lock", out)
	}
	if !strings.Contains(out, "wait for the other noahsark command to end") {
		t.Fatalf("gc while locked output %q, want the wait-for-the-other-command message", out)
	}
}

// TestPackFailsFastWhenRepoLockHeld checks the same rule for pack: two
// state-writing commands must never replay the state log into their
// own stale snapshot at the same time.
func TestPackFailsFastWhenRepoLockHeld(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	held, err := repolock.AcquireExclusive(repo, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 2 {
		t.Fatalf("pack while locked: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("pack while locked output %q, want it to name the repository lock", out)
	}
}

// TestRepoLockFreeAfterGC checks that gc releases the repository lock on
// exit, so the next command never finds a lock a crashed or finished
// process left behind.
func TestRepoLockFreeAfterGC(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}

	lk, err := repolock.AcquireExclusive(repo, 0)
	if err != nil {
		t.Fatalf("acquire after gc: %v, want the lock free", err)
	}
	_ = lk.Release()
}

// TestGCWarnsOnTruncatedStateLog checks bug 3: a command that opens a
// state log whose tail was truncated by a crash during an earlier
// append must print one warning line, matching OPERATIONS.md's "the
// tool reports a truncated log" rule.
func TestGCWarnsOnTruncatedStateLog(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	// Corrupt the last byte of state.db's last record's CRC, simulating
	// a crash mid-append, the same way internal/stage's own truncated
	// tail tests do.
	statePath := filepath.Join(repo, "staging", "state.db")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 0 {
		t.Fatalf("gc after a truncated log: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "state log's tail was truncated") {
		t.Fatalf("gc after a truncated log output %q, want the truncated-tail warning", out)
	}
}

// TestRebuildCacheFailsFastWhenRepoLockHeld checks that rebuild-cache
// takes the repository's exclusive lock, not the shared lock
// OPERATIONS.md's own list would suggest: rebuild-cache writes the
// state log and the disc and ref ledgers, and this build has no
// separate cache lock, so it must not run alongside another
// state-writing command, or another rebuild-cache.
func TestRebuildCacheFailsFastWhenRepoLockHeld(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	held, err := repolock.AcquireExclusive(repo, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	code, out := runCmd(t, "rebuild-cache", "--repo="+repo, "--disc="+treeDir)
	if code != 2 {
		t.Fatalf("rebuild-cache while locked: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("rebuild-cache while locked output %q, want it to name the repository lock", out)
	}
}

// TestGCWaitsForRepoLockTimeout checks that repo.lock_timeout makes gc
// wait for a competing holder to release, instead of failing at once,
// matching the config key's role.
func TestGCWaitsForRepoLockTimeout(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "repo.lock_timeout = 2")

	held, err := repolock.AcquireExclusive(repo, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = held.Release()
	}()

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 0 {
		t.Fatalf("gc with repo.lock_timeout: exit %d, want 0 once the holder released: %s", code, out)
	}
}
