package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setGCStdin installs r as gc's confirmation reader and terminal answer
// for the duration of the test.
func setGCStdin(t *testing.T, r io.Reader, terminal bool) {
	t.Helper()
	oldStdin, oldTerm := gcStdin, gcStdinIsTerminal
	gcStdin = r
	gcStdinIsTerminal = func() bool { return terminal }
	t.Cleanup(func() { gcStdin, gcStdinIsTerminal = oldStdin, oldTerm })
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

	setGCStdin(t, strings.NewReader("y\n"), true)
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

	setGCStdin(t, strings.NewReader("n\n"), true)
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

// TestGCForceAfterRefusesNonTerminalWithoutYes checks that a
// non-terminal stdin without --yes is refused rather than silently
// deleting under a shortened retention.
func TestGCForceAfterRefusesNonTerminalWithoutYes(t *testing.T) {
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

	setGCStdin(t, strings.NewReader(""), false)
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code != 2 {
		t.Fatalf("gc --force-after=1h (non-terminal, no --yes): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "not a terminal") {
		t.Fatalf("gc output %q missing the non-terminal refusal", out)
	}
}

// TestGCForceAfterDevNullIsNotATerminal checks the real terminal check,
// not a faked one: a process whose stdin is /dev/null must be refused
// the same way a script's closed or redirected stdin is. /dev/null is a
// character device, so a mode-bit check alone misreads it as a
// terminal; only an ioctl TCGETS check tells them apart.
func TestGCForceAfterDevNullIsNotATerminal(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()
	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()

	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	os.Stdin = null

	oldGCStdin, oldTerm := gcStdin, gcStdinIsTerminal
	gcStdin = null
	gcStdinIsTerminal = func() bool { return isTerminal(os.Stdin) }
	defer func() { gcStdin, gcStdinIsTerminal = oldGCStdin, oldTerm }()

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

	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code != 2 {
		t.Fatalf("gc --force-after=1h (stdin /dev/null): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "stdin is not a terminal, pass --yes") {
		t.Fatalf("gc output %q missing the non-terminal refusal", out)
	}
}

// TestGCForceAfterRefusesNonTerminalEvenWithNothingEligible checks that
// a non-terminal stdin without --yes is refused before the eligibility
// scan runs, even when no object would turn out to be eligible: the
// confirmation requirement must not depend on what the scan finds.
func TestGCForceAfterRefusesNonTerminalEvenWithNothingEligible(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	// Left at gc's default retention: nothing is CLEAN long enough to
	// be GC-ELIGIBLE under --force-after=1h either, so the eligibility
	// scan finds nothing.
	packAndVerifyDisc(t, work, repo, src)

	setGCStdin(t, strings.NewReader(""), false)
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code != 2 {
		t.Fatalf("gc --force-after=1h (non-terminal, no --yes, nothing eligible): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "not a terminal") {
		t.Fatalf("gc output %q missing the non-terminal refusal", out)
	}
	if strings.Contains(out, "deleted 0 object") {
		t.Fatalf("gc output %q ran the delete step instead of refusing up front", out)
	}
}

// TestGCForceAfterYesSkipsConfirmation checks --yes deletes without
// reading any confirmation, even on a non-terminal stdin.
func TestGCForceAfterYesSkipsConfirmation(t *testing.T) {
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

	setGCStdin(t, strings.NewReader(""), false)
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h", "--yes")
	if code != 0 {
		t.Fatalf("gc --force-after=1h --yes: exit %d: %s", code, out)
	}
	if strings.Contains(out, "deleted 0 object") {
		t.Fatalf("gc output %q, want more than 0 objects deleted", out)
	}
}

// TestGCForceAfterDryRunSkipsConfirmation checks --dry-run with
// --force-after never prompts, even on a non-terminal stdin with no
// --yes.
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

	setGCStdin(t, strings.NewReader(""), false)
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
