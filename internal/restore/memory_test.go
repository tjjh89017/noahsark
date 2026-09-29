package restore

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
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
	w := testWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	treeDir = t.TempDir()
	capSectors := (uint64(768<<20) + image.SectorSize - 1) / image.SectorSize
	opts := image.BuildOptions{
		ObjectPath:            testObjectPath(stagingDir),
		Snapshots:             []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors: capSectors,
		OutputDir:             treeDir,
		RepoUUID:              [16]byte{1, 2, 3, 4},
		DiscUUID:              [16]byte{5, 6, 7, 8},
		Label:                 "restore-mem-test",
		FECEnabled:            true,
		Now:                   fixedClock,
	}
	if _, err := image.Build(opts); err != nil {
		t.Fatal(err)
	}
	return treeDir, snapID
}

// TestDiscSwapRestoreMemoryBounded asserts the peak of one disc-swap
// restore over a fixture of many chunks. The assembler holds one
// disc's object id set, one chunk and one file's blob entries, so its
// peak follows the chunk size, never the size of the data.
func TestDiscSwapRestoreMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("memory assertion test, skipped under -short")
	}
	treeDir, snapID := buildMemoryFixtureTree(t)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := plan.Select(c, snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	a, err := NewAssembler(c, sel, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	sampler := startPeakMemSampler()
	if err := a.Disc(&treeDisc{root: treeDir}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Finish(); err != nil {
		t.Fatal(err)
	}
	peak := sampler.Stop()
	if rep := a.Report(); rep.Failed() {
		t.Fatalf("restore reported %s", rep.Summary())
	}
	t.Logf("disc-swap restore peak heap+stack: %d bytes (%.1f MiB)", peak, float64(peak)/(1<<20))
	if peak > memPeakBudget {
		t.Fatalf("the restore peaked at %d bytes, want under %d (%.1f MiB budget)", peak, memPeakBudget, float64(memPeakBudget)/(1<<20))
	}
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

// planMemDiscs and planMemItems give a catalog of many discs with a
// large INDEX each: together about 80 MB of Objects tables.
const (
	planMemDiscs = 40
	planMemItems = 50_000
)

// planPeakBudget is the peak that the plan of that catalog may reach:
// one INDEX and the sort buffer, never the INDEX of every disc.
const planPeakBudget = 24 << 20

// planMemID returns the content id of item i of disc d.
func planMemID(d, i int) object.ID {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], uint64(d))
	binary.LittleEndian.PutUint64(b[8:], uint64(i))
	return object.ID(sha256.Sum256(b[:]))
}

// TestPlanMemoryBounded plans a restore over many discs. The plan holds
// one catalog INDEX at a time, so its peak does not grow with the number
// of discs.
func TestPlanMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("memory assertion test, skipped under -short")
	}
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var discs []plan.Disc
	for d := range planMemDiscs {
		idx := format.Index{
			Header: format.CommonHeader{
				MagicProject: format.ProjectMagic,
				MagicKind:    format.MagicIndex,
				VersionMajor: 1,
				HeaderLen:    format.IndexHeaderLen,
			},
			ObjectCount: planMemItems,
			Objects:     make([]format.IndexObjectRecord, planMemItems),
		}
		for i := range idx.Objects {
			idx.Objects[i] = format.IndexObjectRecord{ContentID: planMemID(d, i), Kind: format.ObjectKindChunk}
		}
		buf := make([]byte, idx.EncodedLen())
		if _, err := idx.Encode(buf); err != nil {
			t.Fatal(err)
		}
		uuid := [16]byte{byte(d + 1)}
		if err := c.WriteDisc(uuid, buf, nil, nil); err != nil {
			t.Fatal(err)
		}
		discs = append(discs, plan.Disc{DiscUUID: uuid, DiscSeq: uint64(d)})
	}

	sampler := startPeakMemSampler()
	p, err := plan.New(c, nil, discs)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for d := range planMemDiscs {
		for i := 0; i < planMemItems; i += 10 {
			p.Add(planMemID(d, i))
		}
	}
	if err := p.Count(); err != nil {
		t.Fatal(err)
	}
	peak := sampler.Stop()
	if got := len(p.Discs()); got != planMemDiscs {
		t.Fatalf("the plan names %d disc(s), want %d", got, planMemDiscs)
	}
	t.Logf("plan peak heap+stack: %d bytes (%.1f MiB)", peak, float64(peak)/(1<<20))
	if peak > planPeakBudget {
		t.Fatalf("the plan peaked at %d bytes, want under %d (%.1f MiB budget)", peak, planPeakBudget, float64(planPeakBudget)/(1<<20))
	}
}

// manyDirs and manyFilesPerDir give a snapshot of 200 000 small files.
const (
	manyDirs        = 400
	manyFilesPerDir = 500
)

// manyFilesBudget is the peak that a restore of that snapshot may reach.
// A structure with one entry for each file needs more than 20 MiB.
const manyFilesBudget = 8 << 20

// writeCatalogObject encodes one tree or blob with its payload lengths
// set, stores it in c, and returns its content id.
func writeCatalogObject(t *testing.T, c *catalog.Catalog, kind format.ObjectKind, encode func(oh format.ObjectHeader) []byte) object.ID {
	t.Helper()
	oh := format.ObjectHeader{Kind: kind, HashAlgo: format.HashAlgoSHA256}
	buf := encode(oh)
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := object.ComputeID(kind, payload)
	oh.PayloadLen = uint64(len(payload))
	oh.StoredLen = uint64(len(payload))
	if err := c.WriteObject(kind, id, encode(oh)); err != nil {
		t.Fatal(err)
	}
	return id
}

func writeTestTree(t *testing.T, c *catalog.Catalog, entries []format.TreeEntry) object.ID {
	t.Helper()
	return writeCatalogObject(t, c, format.ObjectKindTree, func(oh format.ObjectHeader) []byte {
		tr := format.Tree{
			Header:       format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicTree, VersionMajor: 1, HeaderLen: format.TreeHeaderLen},
			ObjectHeader: oh,
			EntryCount:   uint32(len(entries)),
			Entries:      entries,
		}
		buf := make([]byte, tr.EncodedLen())
		if _, err := tr.Encode(buf); err != nil {
			t.Fatal(err)
		}
		return buf
	})
}

// manyFilesSnapshot writes a catalog snapshot of nDirs directories with
// perDir files each. Every file has one chunk of one byte
// that no disc holds, so every file stays pending to the end. The
// directories share one tree and the files share one blob.
func manyFilesSnapshot(t *testing.T, c *catalog.Catalog, nDirs, perDir int) *format.Snapshot {
	t.Helper()
	blobID := writeCatalogObject(t, c, format.ObjectKindBlob, func(oh format.ObjectHeader) []byte {
		b := format.Blob{
			Header:       format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicBlob, VersionMajor: 1, HeaderLen: format.BlobHeaderLen},
			ObjectHeader: oh,
			EntryCount:   1,
			Entries:      []format.BlobEntry{{ContentID: planMemID(0, 0), Length: 1}},
		}
		buf := make([]byte, b.EncodedLen())
		if _, err := b.Encode(buf); err != nil {
			t.Fatal(err)
		}
		return buf
	})
	files := make([]format.TreeEntry, perDir)
	for i := range files {
		files[i] = format.TreeEntry{EntryType: format.EntryTypeRegular, Size: 1, Mode: 0o644, Name: fmt.Appendf(nil, "f%04d", i), ContentID: blobID}
	}
	dirTree := writeTestTree(t, c, files)
	dirs := make([]format.TreeEntry, nDirs)
	for i := range dirs {
		dirs[i] = format.TreeEntry{EntryType: format.EntryTypeDirectory, Mode: 0o755, Name: fmt.Appendf(nil, "d%04d", i), ContentID: dirTree}
	}
	srcTree := writeTestTree(t, c, dirs)
	rootTree := writeTestTree(t, c, []format.TreeEntry{{EntryType: format.EntryTypeDirectory, Mode: 0o755, Name: []byte(format.EncodeRootName("/src")), ContentID: srcTree}})
	return &format.Snapshot{RootTree: [32]byte(rootTree)}
}

// TestDiscSwapRestoreManyFilesBounded restores a snapshot of many files
// that no disc can complete. The assembler keeps no record in memory for
// each file or each directory, so its peak does not grow with the number
// of files.
func TestDiscSwapRestoreManyFilesBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("memory assertion test, skipped under -short")
	}
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sel, err := plan.Select(c, manyFilesSnapshot(t, c, manyDirs, manyFilesPerDir), nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewAssembler(c, sel, filepath.Join(t.TempDir(), "out"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	sampler := startPeakMemSampler()
	for range 2 {
		if err := a.Disc(&treeDisc{holds: map[object.ID]bool{}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Finish(); err != nil {
		t.Fatal(err)
	}
	peak := sampler.Stop()
	rep := a.Report()
	if got, want := rep.Count(KindFile), manyDirs*manyFilesPerDir; got != want {
		t.Fatalf("the restore reports %d file(s) not restored, want %d", got, want)
	}
	t.Logf("restore of %d files peak heap+stack: %d bytes (%.1f MiB)", manyDirs*manyFilesPerDir, peak, float64(peak)/(1<<20))
	if peak > manyFilesBudget {
		t.Fatalf("the restore peaked at %d bytes, want under %d (%.1f MiB budget)", peak, manyFilesBudget, float64(manyFilesBudget)/(1<<20))
	}
}

// TestDiscSwapRestoreStopsWhenDestChanges replaces a directory of the
// destination between two walks. The later walk then meets other files
// at the numbers of the first walk, and stops instead of using the
// state of the wrong file.
func TestDiscSwapRestoreStopsWhenDestChanges(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sel, err := plan.Select(c, manyFilesSnapshot(t, c, 3, 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	a, err := NewAssembler(c, sel, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	empty := &treeDisc{holds: map[object.ID]bool{}}
	if err := a.Disc(empty, nil); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(outDir, "d0001")
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Disc(empty, nil); !errors.Is(err, errDestChanged) {
		t.Fatalf("the second walk returned %v, want %v", err, errDestChanged)
	}
}
