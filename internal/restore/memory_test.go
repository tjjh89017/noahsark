package restore

import (
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// memFixtureBytes is the pseudo-random content size the memory
// assertion test stages: at least 256 MiB, well past the 16 MiB max
// chunk size and the one FEC stripe the hard memory bound allows.
const memFixtureBytes = 256 << 20

// memPeakBudget is the peak heap-plus-stack budget this test asserts
// against: bounded by the chunk size and one FEC stripe, never by the
// fixture size.
const memPeakBudget = 128 << 20

// peakMemSampler samples runtime.MemStats HeapInuse plus StackInuse
// every 20ms in a background goroutine and reports the highest growth it
// saw over its own starting baseline, between startPeakMemSampler and
// Stop. Measuring growth over a forced-GC baseline, rather than the
// absolute value, keeps the result about the call under test: a
// process-wide fixed cost paid once before the sampler starts (such as a
// shared compressor's own buffers) does not count against it. Each
// sample forces a GC first, so the reading is the live set at that
// instant, not garbage still waiting for Go's own GC pacing to reclaim
// it; without that, a large fixed baseline elsewhere in the process
// delays collection and can make a bounded call look unbounded.
type peakMemSampler struct {
	stop     chan struct{}
	done     chan struct{}
	baseline uint64
	peak     atomic.Uint64
}

func startPeakMemSampler() *peakMemSampler {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	s := &peakMemSampler{
		stop: make(chan struct{}), done: make(chan struct{}),
		baseline: base.HeapInuse + base.StackInuse,
	}
	go func() {
		defer close(s.done)
		var m runtime.MemStats
		sample := func() {
			runtime.GC()
			runtime.ReadMemStats(&m)
			cur := m.HeapInuse + m.StackInuse
			for {
				old := s.peak.Load()
				if cur <= old || s.peak.CompareAndSwap(old, cur) {
					break
				}
			}
		}
		sample()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				sample()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return s
}

// Stop ends sampling and returns the peak growth, in bytes, over the
// baseline recorded when sampling started.
func (s *peakMemSampler) Stop() uint64 {
	close(s.stop)
	<-s.done
	peak := s.peak.Load()
	if peak <= s.baseline {
		return 0
	}
	return peak - s.baseline
}

// writeStreamedRandomFile streams n deterministic pseudo-random bytes to
// path, one fixed-size buffer at a time, so building a large fixture
// never holds it whole in memory.
func writeStreamedRandomFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	r := rand.New(rand.NewSource(99))
	buf := make([]byte, 1<<20)
	for remaining := n; remaining > 0; {
		chunk := min(remaining, len(buf))
		r.Read(buf[:chunk])
		if _, err := f.Write(buf[:chunk]); err != nil {
			t.Fatal(err)
		}
		remaining -= chunk
	}
}

// buildMemoryFixtureTree stages at least memFixtureBytes of pseudo-random
// content, streamed to disk, and builds one run's NOAHSARK tree from it.
func buildMemoryFixtureTree(t *testing.T) (treeDir string, snapID object.ID) {
	t.Helper()
	srcDir := t.TempDir()
	writeStreamedRandomFile(t, filepath.Join(srcDir, "big.bin"), memFixtureBytes)

	stagingDir := t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	treeDir = t.TempDir()
	capSectors := (uint64(768<<20) + image.SectorSize - 1) / image.SectorSize
	opts := image.BuildOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "LATEST", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors:   capSectors,
		PhysicalCapacitySectors: capSectors,
		OutputDir:               treeDir,
		RepoUUID:                [16]byte{1, 2, 3, 4},
		DiscUUID:                [16]byte{5, 6, 7, 8},
		Label:                   "restore-mem-test",
		Now:                     fixedClock,
	}
	if _, err := image.Build(opts); err != nil {
		t.Fatal(err)
	}
	return treeDir, snapID
}

func TestHealMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("memory assertion test, skipped under -short")
	}
	treeDir, _ := buildMemoryFixtureTree(t)

	paths, sizes, layout := streamLayout(t, treeDir)
	corruptDataBlockAt(t, paths, sizes, layout, 3, 0)
	corruptParityBlock(t, treeDir, 0, 0)

	sampler := startPeakMemSampler()
	if _, err := Heal(treeDir, ""); err != nil {
		t.Fatal(err)
	}
	peak := sampler.Stop()
	t.Logf("Heal peak heap+stack: %d bytes (%.1f MiB)", peak, float64(peak)/(1<<20))
	if peak > memPeakBudget {
		t.Fatalf("Heal peaked at %d bytes, want under %d (%.1f MiB budget)", peak, memPeakBudget, float64(memPeakBudget)/(1<<20))
	}
}
