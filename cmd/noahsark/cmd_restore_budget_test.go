package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// withSpoolBytesObserver installs a hook on restoreSpoolBytesObserved
// for the duration of the test, and returns a function that reports the
// largest value it ever saw.
func withSpoolBytesObserver(t *testing.T) func() uint64 {
	t.Helper()
	old := restoreSpoolBytesObserved
	var mu sync.Mutex
	var peak uint64
	restoreSpoolBytesObserved = func(current uint64) {
		mu.Lock()
		defer mu.Unlock()
		if current > peak {
			peak = current
		}
	}
	t.Cleanup(func() { restoreSpoolBytesObserved = old })
	return func() uint64 {
		mu.Lock()
		defer mu.Unlock()
		return peak
	}
}

// countPrompts counts the "insert disc" prompt lines restore printed.
func countPrompts(out string) int {
	return strings.Count(out, "insert disc")
}

// TestRestoreStagingBudgetSplitsIntoPasses runs a two-disc disc-swap
// restore with a staging budget far below either disc's chunk total,
// and checks the restore still completes, the spool never grows past
// the budget, and the operator is prompted exactly once per disc, not
// once per pass.
func TestRestoreStagingBudgetSplitsIntoPasses(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := planOrderDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}

	// Bigger than any one fixture file's own chunk total (600,000 bytes),
	// so the oversize-file refusal never fires, but well under either
	// disc's whole chunk total, so both discs must split into passes.
	const budget = 700_000
	peak := withSpoolBytesObserver(t)

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, fmt.Sprintf("--staging-budget=%d", budget), snapID, outDir)
	if code != 0 {
		t.Fatalf("restore --staging-budget: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)

	if got := peak(); got > budget {
		t.Fatalf("observed spool peak %d bytes, want at most the %d byte budget", got, budget)
	}
	if countPrompts(out) != 1 {
		t.Fatalf("restore output %q has %d disc-insert prompt(s), want exactly 1 (one per disc swap, not one per pass)", out, countPrompts(out))
	}
	if !strings.Contains(out, "pass 1/") {
		t.Fatalf("restore output %q missing a \"pass N/M\" line", out)
	}
}

// TestRestoreStagingBudgetRefusesOversizeFile checks a budget too small
// for even one file's own chunks is refused up front, naming the file
// and the budget, before any disc is read.
func TestRestoreStagingBudgetRefusesOversizeFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, discRoots := discSwapFixture(t)

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[0])
	setRestoreStdin(t, &scriptedStdin{})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, "--staging-budget=1", snapID, outDir)
	if code != 2 {
		t.Fatalf("restore --staging-budget=1: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "alone needs") {
		t.Fatalf("restore output %q missing the oversize-file message", out)
	}
	if strings.Contains(out, "insert disc") {
		t.Fatalf("restore output %q prompted for a disc, want a refusal before any read", out)
	}
}
