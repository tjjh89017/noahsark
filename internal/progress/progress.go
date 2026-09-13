// Package progress reports byte-count progress for a long-running
// command to a stream, without buffering the data it measures.
//
// A nil *Reporter is always safe to call: every method is a no-op on a
// nil receiver, so a command that disables progress reporting can pass
// a nil Reporter through its whole call chain at no cost.
package progress

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// terminalInterval and plainInterval are the minimum time between two
// printed lines, on a terminal and on a plain stream.
const (
	terminalInterval = 200 * time.Millisecond
	plainInterval    = 5 * time.Second
)

// Reporter prints one label's progress to a writer: bytes done against a
// total, a percentage, a throughput and an ETA. On a terminal it rewrites
// one line in place; on a plain stream it prints a full line on a timer
// and once more at Done.
type Reporter struct {
	w        io.Writer
	terminal bool
	now      func() time.Time

	mu     sync.Mutex
	label  string
	total  int64
	done   int64
	start  time.Time
	lastAt time.Time
}

// New returns a Reporter that writes to w, rewriting one line when w is a
// terminal and printing a timed line otherwise. Terminal detection uses
// only the standard library: w is a terminal when it is an *os.File whose
// mode carries the character-device bit.
func New(w io.Writer) *Reporter {
	return &Reporter{w: w, terminal: isTerminal(w), now: time.Now}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Start begins reporting a new phase labelled label, out of total bytes.
// A total of zero means the total is not known in advance; Start still
// reports bytes done and throughput, without a percentage or an ETA.
func (r *Reporter) Start(label string, total int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.label = label
	r.total = total
	r.done = 0
	r.start = r.now()
	// A terminal shows its first update right away; a plain stream waits
	// out its first full interval, so a short-lived command does not spam
	// a log with a line for every phase.
	if r.terminal {
		r.lastAt = time.Time{}
	} else {
		r.lastAt = r.start
	}
}

// Add records n more bytes done and, at most once per throttling
// interval, prints an updated line.
func (r *Reporter) Add(n int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done += n

	now := r.now()
	interval := plainInterval
	if r.terminal {
		interval = terminalInterval
	}
	if r.lastAt.IsZero() || now.Sub(r.lastAt) >= interval {
		r.print(now)
		r.lastAt = now
	}
}

// Done prints one final line, always, even when no interval has elapsed
// since the last one, and closes out a terminal's rewritten line with a
// newline.
func (r *Reporter) Done() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.print(r.now())
	if r.terminal {
		_, _ = fmt.Fprintln(r.w)
	}
}

// print writes one progress line. Caller holds r.mu.
func (r *Reporter) print(now time.Time) {
	elapsed := now.Sub(r.start).Seconds()
	var mbps float64
	if elapsed > 0 {
		mbps = float64(r.done) / 1e6 / elapsed
	}

	var pct, eta string
	if r.total > 0 {
		pct = fmt.Sprintf(" (%.1f%%)", 100*float64(r.done)/float64(r.total))
		if mbps > 0 && r.done < r.total {
			remain := float64(r.total-r.done) / 1e6 / mbps
			eta = " ETA " + formatDuration(time.Duration(remain*float64(time.Second)))
		} else if r.done >= r.total {
			eta = " ETA 0s"
		}
	}

	var line string
	if r.total > 0 {
		line = fmt.Sprintf("%s: %s / %s%s, %.2f MB/s%s", r.label, formatBytes(r.done), formatBytes(r.total), pct, mbps, eta)
	} else {
		line = fmt.Sprintf("%s: %s, %.2f MB/s", r.label, formatBytes(r.done), mbps)
	}

	if r.terminal {
		_, _ = fmt.Fprintf(r.w, "\r\x1b[K%s", line)
	} else {
		_, _ = fmt.Fprintln(r.w, line)
	}
}

// formatBytes renders n bytes as a fixed-point value in the largest unit
// that keeps the number readable.
func formatBytes(n int64) string {
	const unit = 1000.0
	f := float64(n)
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.2f %s", f, units[i])
}

// formatDuration renders d as Hh Mm Ss, dropping leading zero units.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
