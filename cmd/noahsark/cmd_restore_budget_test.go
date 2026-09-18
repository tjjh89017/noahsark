package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// TestPlanStagingBudgetRefusesOversizeFile checks that plan, reading
// only the local cache, refuses the same over-budget file restore does:
// same exit code, and the file path named in the message.
func TestPlanStagingBudgetRefusesOversizeFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, _ := discSwapFixture(t)

	code, out := runCmd(t, "plan", "--repo="+repo, "--staging-budget=1", snapID)
	if code != 2 {
		t.Fatalf("plan --staging-budget=1: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "alone needs") {
		t.Fatalf("plan output %q missing the oversize-file message", out)
	}
}

// passLineRe matches one of restore's "disc N ...: pass X/Y" lines,
// capturing X and Y.
var passLineRe = regexp.MustCompile(`pass (\d+)/(\d+)`)

// writeMultiChunkFixtureSource writes a handful of files large enough
// that the default chunker profile (1 MiB minimum) splits each into
// several chunks, so a staging budget forces a disc into more than one
// pass.
func writeMultiChunkFixtureSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	rng := rand.New(rand.NewSource(7))
	for i := range 6 {
		dir := filepath.Join(src, fmt.Sprintf("sub%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 6_000_000)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// TestRestoreStagingBudgetPassCounterMatchesAnnouncedTotal packs a
// snapshot whose chunk objects are not grouped by file in plan order
// (id after id, some belonging to files whose other chunks land far
// apart), and restores it with a staging budget that forces several
// passes. Before plan.group() kept a blob's own chunks adjacent in its
// disc's object list, the pass counter printed during the restore
// ("pass X/Y") could run past the announced total Y, since a real
// restore frees a file's spool bytes only once every one of its chunks
// is read, and a scattered order left many files incomplete, and their
// bytes unfreed, well past where the announced total assumed they
// would free.
func TestRestoreStagingBudgetPassCounterMatchesAnnouncedTotal(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiChunkFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=1GB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	const budget = 7_000_000
	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, treeDir)
	setRestoreStdin(t, &scriptedStdin{})

	outDir := filepath.Join(work, "out")
	code, out = runCmd(t, "restore", "--repo="+repo, "--mount="+mountDir, fmt.Sprintf("--staging-budget=%d", budget), snapID, outDir)
	if code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	for _, m := range passLineRe.FindAllStringSubmatch(out, -1) {
		passNum, _ := strconv.Atoi(m[1])
		total, _ := strconv.Atoi(m[2])
		if passNum > total {
			t.Fatalf("restore output %q printed %q, the pass counter ran past the announced total", out, m[0])
		}
	}
}
