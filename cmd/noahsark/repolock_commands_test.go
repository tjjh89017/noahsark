package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestLockingCommandsFailWhileTheLockIsHeld runs each command that takes
// the repository lock while this process holds that lock. Each command
// must exit 1, name the repository lock, and change no file of the
// repository: not the state log, not the disc state log, and no other
// file.
func TestLockingCommandsFailWhileTheLockIsHeld(t *testing.T) {
	packed := repoWithDisc(t, stage.DiscPacked)
	burned := repoWithDisc(t, stage.DiscBurned)
	verified := repoWithDisc(t, stage.DiscVerified)
	newSource := writeSeededSource(t, 7, 1)

	cases := []struct {
		name string
		fx   *discFixture
		args []string
	}{
		{"commit", packed, []string{"commit", newSource}},
		{"pack", packed, []string{"pack", "--capacity=64MiB"}},
		{"pack --undo", packed, []string{"--yes", "pack", "--undo", "0"}},
		{"disc burned", packed, []string{"disc", "burned", packed.uuid}},
		{"verify", burned, []string{"verify", burned.root}},
		{"disc verified", burned, []string{"--yes", "disc", "verified", burned.uuid}},
		{"verify --undo", verified, []string{"--yes", "verify", "--undo", verified.uuid}},
		{"disc lost", verified, []string{"--yes", "disc", "lost", verified.uuid}},
		{"gc", verified, []string{"gc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := repoSnapshot(t, c.fx.repo)
			held := holdRepoLock(t, c.fx.repo)
			code, out := c.fx.run(t, c.args...)
			_ = held.Release()
			if code != 1 {
				t.Fatalf("%v while the lock is held: exit %d, want 1: %s", c.args, code, out)
			}
			if !strings.Contains(out, "repository lock") || !strings.Contains(out, "another noahsark command runs on this repository") {
				t.Fatalf("%v while the lock is held: output %q, want the lock message", c.args, out)
			}
			assertRepoUnchanged(t, c.fx.repo, before)
		})
	}
}

// TestInitFailsWhileTheLockIsHeld runs init in a directory whose lock
// another process holds. init must exit 1, name the repository lock,
// and make no repository.
func TestInitFailsWhileTheLockIsHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	held := holdRepoLock(t, dir)
	defer func() { _ = held.Release() }()

	code, out := runIn(t, dir, "init")
	if code != 1 {
		t.Fatalf("init while the lock is held: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("init while the lock is held: output %q, want the lock message", out)
	}
	if got := listFilesUnder(t, dir); !slices.Equal(got, []string{repolock.FileName}) {
		t.Fatalf("init while the lock is held left %v, want only the lock file", got)
	}
}

// holdRepoLock takes the lock of the repository at repo for the test.
func holdRepoLock(t *testing.T, repo string) *repolock.Lock {
	t.Helper()
	held, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatalf("hold the lock: %v", err)
	}
	return held
}

// repoState is the file list of a repository and the bytes of its two
// logs.
type repoState struct {
	files   []string
	itemLog []byte
	discLog []byte
}

// repoSnapshot records the state of the repository at repo.
func repoSnapshot(t *testing.T, repo string) repoState {
	t.Helper()
	layout := testLayout(t, repo)
	return repoState{
		files:   listFilesUnder(t, repo),
		itemLog: readFileOrNil(t, layout.stateLogFile()),
		discLog: readFileOrNil(t, layout.discLogFile()),
	}
}

// assertRepoUnchanged fails the test when the repository at repo is not
// as before.
func assertRepoUnchanged(t *testing.T, repo string, before repoState) {
	t.Helper()
	after := repoSnapshot(t, repo)
	if !slices.Equal(after.files, before.files) {
		t.Errorf("repository files changed:\nbefore %v\nafter  %v", before.files, after.files)
	}
	if !slices.Equal(after.itemLog, before.itemLog) {
		t.Errorf("the state log changed: %d bytes before, %d bytes after", len(before.itemLog), len(after.itemLog))
	}
	if !slices.Equal(after.discLog, before.discLog) {
		t.Errorf("the disc state log changed: %d bytes before, %d bytes after", len(before.discLog), len(after.discLog))
	}
}

// readFileOrNil returns the bytes of the file at path, or nil when the
// file does not exist.
func readFileOrNil(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}
