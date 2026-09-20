package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setGCStdin installs r as gc's confirmation reader for the duration of
// the test.
func setGCStdin(t *testing.T, r io.Reader) {
	t.Helper()
	old := gcStdin
	gcStdin = r
	t.Cleanup(func() { gcStdin = old })
}

// TestGCForceAfterConfirmedDeletes runs gc --force-after with a fake
// clock placing an object CLEAN for longer than the forced duration but
// not the configured staging.retain_after_clean, and a stdin pipe
// answering "y": the object must be deleted.
func TestGCForceAfterConfirmedDeletes(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 30d")

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// 2 hours past CLEAN: nowhere near the 30-day config retention, but
	// past a 1-hour --force-after.
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader("y\n"))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code != 0 {
		t.Fatalf("gc --force-after=1h (confirmed): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "delete") || !strings.Contains(out, "bytes?") {
		t.Fatalf("gc output %q missing the confirmation prompt", out)
	}
	if strings.Contains(out, "deleted 0 object") {
		t.Fatalf("gc output %q, want more than 0 objects deleted", out)
	}
}

// TestGCForceAfterDeclinedDeletesNothing checks answering "n" to the
// confirmation leaves every object in place.
func TestGCForceAfterDeclinedDeletesNothing(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 30d")

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader("n\n"))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code == 0 {
		t.Fatalf("gc --force-after=1h (declined): exit 0, want non-zero: %s", out)
	}
	if !strings.Contains(out, "not confirmed") {
		t.Fatalf("gc output %q missing the not-confirmed message", out)
	}

	// A follow-up dry-run still finds the object eligible: nothing was
	// deleted.
	code, out = runCmd(t, "gc", "--repo="+repo, "--force-after=1h", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (after decline): exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 object") {
		t.Fatalf("gc --dry-run (after decline) output %q, want the object still eligible", out)
	}
}

// TestGCForceAfterEmptyStdinDeletesNothing checks that a closed or
// empty stdin answers no: a killed or scripted session must never read
// silence as consent to delete under a shortened retention. A script
// that means yes pipes a "y" in.
func TestGCForceAfterEmptyStdinDeletesNothing(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 30d")

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader(""))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code != 1 {
		t.Fatalf("gc --force-after=1h (empty stdin): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not confirmed") {
		t.Fatalf("gc output %q missing the not-confirmed message", out)
	}

	code, out = runCmd(t, "gc", "--repo="+repo, "--force-after=1h", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (after empty stdin): exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 object") {
		t.Fatalf("gc --dry-run output %q, want the object still eligible", out)
	}
}

// TestGCForceAfterDryRunSkipsConfirmation checks --dry-run with
// --force-after never prompts: it changes nothing either way.
func TestGCForceAfterDryRunSkipsConfirmation(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 30d")

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader(""))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --force-after=1h --dry-run: exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 object") {
		t.Fatalf("gc --dry-run output %q, want more than 0 objects reported", out)
	}
	if strings.Contains(out, "bytes?") {
		t.Fatalf("gc --dry-run output %q, want no confirmation prompt", out)
	}
}
