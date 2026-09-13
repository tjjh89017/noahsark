package progress

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// fakeClock lets a test move Reporter's clock by hand, instead of racing
// a real timer.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func newTestReporter(w *bytes.Buffer, terminal bool, clock *fakeClock) *Reporter {
	return &Reporter{w: w, terminal: terminal, now: clock.now}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1000, "1.00 kB"},
		{1_500_000, "1.50 MB"},
		{2_000_000_000, "2.00 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{65 * time.Second, "1m5s"},
		{3661 * time.Second, "1h1m1s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestPlainWriterPrintsFullLinesOnTimer checks a non-terminal writer
// prints one full line per Add call once the plain interval has passed,
// and none before it.
func TestPlainWriterPrintsFullLinesOnTimer(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := newTestReporter(&buf, false, clock)

	r.Start("commit", 1000)
	r.Add(100)
	if buf.Len() != 0 {
		t.Fatalf("expected no line before the plain interval elapses, got %q", buf.String())
	}

	clock.advance(plainInterval)
	r.Add(100)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one line, got %d: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "commit:") || !strings.Contains(lines[0], "200 B") {
		t.Errorf("unexpected line: %q", lines[0])
	}
	if strings.Contains(lines[0], "\x1b") {
		t.Errorf("a plain writer must not carry a terminal escape: %q", lines[0])
	}
}

// TestTerminalWriterThrottles checks a terminal writer rewrites a single
// line, printing again only once the shorter terminal interval passes.
func TestTerminalWriterThrottles(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := newTestReporter(&buf, true, clock)

	r.Start("pack", 1000)
	r.Add(50)
	first := buf.String()
	if !strings.Contains(first, "\r\x1b[K") {
		t.Fatalf("expected a terminal rewrite sequence, got %q", first)
	}

	buf.Reset()
	r.Add(50)
	if buf.Len() != 0 {
		t.Fatalf("expected no second line before the terminal interval elapses, got %q", buf.String())
	}

	clock.advance(terminalInterval)
	r.Add(50)
	if buf.Len() == 0 {
		t.Fatalf("expected a line once the terminal interval elapsed")
	}
}

// TestDoneAlwaysPrints checks Done prints a final line even when called
// right after Start, before any throttling interval has passed, on both
// a terminal and a plain writer.
func TestDoneAlwaysPrints(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		var buf bytes.Buffer
		clock := &fakeClock{t: time.Unix(0, 0)}
		r := newTestReporter(&buf, terminal, clock)

		r.Start("verify", 500)
		r.Add(500)
		r.Done()

		if buf.Len() == 0 {
			t.Fatalf("terminal=%v: expected Done to print a line", terminal)
		}
		if !strings.Contains(buf.String(), "verify:") {
			t.Errorf("terminal=%v: expected the label in the final line, got %q", terminal, buf.String())
		}
	}
}

// TestNonTerminalGetsLineAtDoneWithNoAdd checks a non-terminal writer
// that never crossed the timer still gets exactly one line, from Done.
func TestNonTerminalGetsLineAtDoneWithNoAdd(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{t: time.Unix(0, 0)}
	r := newTestReporter(&buf, false, clock)

	r.Start("restore", 100)
	r.Done()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("expected exactly one line at Done, got %q", buf.String())
	}
}

// TestNilReporterIsSafe checks every method on a nil *Reporter is a
// no-op, so a disabled Reporter costs nothing and never panics.
func TestNilReporterIsSafe(t *testing.T) {
	var r *Reporter
	r.Start("x", 10)
	r.Add(5)
	r.Done()
}
