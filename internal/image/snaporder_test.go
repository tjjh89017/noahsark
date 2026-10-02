package image

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// commitAt commits a source of files pseudo-random files of fileSize
// bytes each into stagingDir, with the snapshot time at. The content
// depends on name, thus two names give two snapshots that share no
// chunk. It marks every object of the snapshot Staged in l.
func commitAt(t *testing.T, stagingDir string, l *stage.Log, name string, at time.Time, files, fileSize int) object.ID {
	t.Helper()
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	srcDir := t.TempDir()
	for i := range files {
		dir := filepath.Join(srcDir, fmt.Sprintf("sub%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, fileSize)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := testWriter(stagingDir)
	w.Now = func() time.Time { return at }
	w.Profile = packFixtureChunkProfile
	id, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, id, l)
	return id
}

// reachableSet returns the id of every object that snapID reaches, the
// snapshot object included.
func reachableSet(t *testing.T, stagingDir string, snapID object.ID) map[object.ID]bool {
	t.Helper()
	objs, err := CollectReachable(testObjectPath(stagingDir), []object.ID{snapID})
	if err != nil {
		t.Fatal(err)
	}
	set := make(map[object.ID]bool, len(objs))
	for _, o := range objs {
		set[o.ID] = true
	}
	return set
}

// testCandidateOrder returns the pack order of every snapshot of the test
// repository stagingDir, as Pack builds it.
func testCandidateOrder(t *testing.T, stagingDir string, l *stage.Log) []packUnit {
	t.Helper()
	ids, err := testSnapshotIDs(stagingDir)()
	if err != nil {
		t.Fatal(err)
	}
	return buildPackPlan(testObjectPath(stagingDir), ids, l, nil, true, false).units
}

// unitIndex returns the index of id in order, or -1.
func unitIndex(order []packUnit, id object.ID) int {
	for i, u := range order {
		if u.ID == id {
			return i
		}
	}
	return -1
}

// TestPackTakesTheRestOfAnOldSnapshotFirst packs a part of an old
// snapshot, then commits a newer snapshot whose id sorts before the old
// one. The rest of the old snapshot, and its snapshot object, must come
// before each object of the new snapshot.
func TestPackTakesTheRestOfAnOldSnapshotFirst(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	t1 := fixedClock()
	old := commitAt(t, stagingDir, l, "old", t1, 8, 1_000_000)

	opts := packOpts(stagingDir, old, t.TempDir(), sectorsFor(7_000_000), 1, l)
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if rec, _ := l.Get(old); rec.State != stage.Staged {
		t.Fatalf("the old snapshot object is %v after the first pack, want Staged: the capacity must hold a part only", rec.State)
	}

	var newer object.ID
	for i := range 64 {
		id := commitAt(t, stagingDir, l, fmt.Sprintf("new-%d", i), t1.Add(time.Hour), 2, 100_000)
		if lessBytes(id[:], old[:]) {
			newer = id
			break
		}
	}
	if newer == (object.ID{}) {
		t.Fatal("no new snapshot id sorts before the old snapshot id")
	}

	groups, _ := PackGroups(testObjectPath(stagingDir), mustSnapshotIDs(t, stagingDir), l)
	if len(groups) < 2 || groups[0].ID != old || groups[0].OnDisc {
		t.Fatalf("PackGroups() = %+v, want the old snapshot first, its snapshot object staged", groups)
	}
	oldReach := reachableSet(t, stagingDir, old)
	wantStaged := 0
	for id := range oldReach {
		if rec, _ := l.Get(id); rec.State == stage.Staged {
			wantStaged++
		}
	}
	if groups[0].StagedItems != wantStaged {
		t.Fatalf("StagedItems = %d, want %d", groups[0].StagedItems, wantStaged)
	}

	order := testCandidateOrder(t, stagingDir, l)
	oldAt := unitIndex(order, old)
	if oldAt < 0 {
		t.Fatal("the old snapshot object is not in the pack order")
	}
	for i, u := range order[:oldAt] {
		if !oldReach[u.ID] {
			t.Fatalf("unit %d (%s) of another snapshot goes before the old snapshot object at %d", i, u.ID.TextForm(), oldAt)
		}
	}
	if newAt := unitIndex(order, newer); newAt < oldAt {
		t.Fatalf("the new snapshot object is at %d, before the old snapshot object at %d", newAt, oldAt)
	}
}

// TestStagedSnapshotsGoInTimeOrder commits two snapshots whose id order
// is the reverse of their time order, with no item on a disc. The older
// snapshot goes first. Two snapshots with the same time go in id order.
func TestStagedSnapshotsGoInTimeOrder(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	t1 := fixedClock()
	var older, newer object.ID
	for i := range 64 {
		a := commitAt(t, stagingDir, l, fmt.Sprintf("older-%d", i), t1, 1, 50_000)
		b := commitAt(t, stagingDir, l, fmt.Sprintf("newer-%d", i), t1.Add(time.Minute), 1, 50_000)
		if lessBytes(b[:], a[:]) {
			older, newer = a, b
			break
		}
		// Start again with an empty repository, so that only the last
		// pair is in it.
		stagingDir = t.TempDir()
		if l, err = stage.Open(stagingDir); err != nil {
			t.Fatal(err)
		}
	}
	if older == (object.ID{}) {
		t.Fatal("no pair of snapshots has an id order that is the reverse of the time order")
	}
	same := commitAt(t, stagingDir, l, "same-time", t1.Add(time.Minute), 1, 50_000)

	groups, _ := PackGroups(testObjectPath(stagingDir), mustSnapshotIDs(t, stagingDir), l)
	var got []object.ID
	for _, g := range groups {
		got = append(got, g.ID)
	}
	want := []object.ID{older, newer, same}
	if lessBytes(same[:], newer[:]) {
		want = []object.ID{older, same, newer}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("PackGroups order = %v, want %v", got, want)
	}

	order := testCandidateOrder(t, stagingDir, l)
	if o, n := unitIndex(order, older), unitIndex(order, newer); o < 0 || n < 0 || o > n {
		t.Fatalf("pack order: older snapshot at %d, newer at %d, want older first", o, n)
	}
}

// synthRepo is a synthetic repository for the benchmarks: blob, tree and
// snapshot files under dir, and one chunk file that stands for each chunk.
type synthRepo struct {
	dir   string
	chunk string
	ids   []object.ID
}

// objectPath gives the chunk file for each chunk id, and the file of its
// own id for each other object.
func (r *synthRepo) objectPath(kind format.ObjectKind, id object.ID) string {
	if kind == format.ObjectKindChunk {
		return r.chunk
	}
	return testObjectPath(r.dir)(kind, id)
}

// write encodes a tree, blob or snapshot object with enc, writes it and
// returns its id.
func (r *synthRepo) write(tb testing.TB, kind format.ObjectKind, enc func(payloadLen uint64) []byte) object.ID {
	tb.Helper()
	buf := enc(0)
	payload := buf[format.CommonHeaderLen+format.ObjectHeaderLen:]
	id := object.ComputeID(kind, payload)
	buf = enc(uint64(len(payload)))
	path := r.objectPath(kind, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		tb.Fatal(err)
	}
	r.ids = append(r.ids, id)
	return id
}

func synthHeader(magic format.Magic, headerLen int) format.CommonHeader {
	return format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: magic, VersionMajor: 1, HeaderLen: uint16(headerLen)}
}

// blob writes a blob of the one chunk id.
func (r *synthRepo) blob(tb testing.TB, chunkID object.ID) object.ID {
	return r.write(tb, format.ObjectKindBlob, func(n uint64) []byte {
		b := format.Blob{
			Header:       synthHeader(format.MagicBlob, format.BlobHeaderLen),
			ObjectHeader: format.ObjectHeader{Kind: format.ObjectKindBlob, HashAlgo: format.HashAlgoSHA256, PayloadLen: n, StoredLen: n},
			EntryCount:   1, Entries: []format.BlobEntry{{ContentID: chunkID, Length: 100}},
		}
		buf := make([]byte, b.EncodedLen())
		if _, err := b.Encode(buf); err != nil {
			tb.Fatal(err)
		}
		return buf
	})
}

// tree writes a tree of entries.
func (r *synthRepo) tree(tb testing.TB, entries []format.TreeEntry) object.ID {
	return r.write(tb, format.ObjectKindTree, func(n uint64) []byte {
		tr := format.Tree{
			Header:       synthHeader(format.MagicTree, format.TreeHeaderLen),
			ObjectHeader: format.ObjectHeader{Kind: format.ObjectKindTree, HashAlgo: format.HashAlgoSHA256, PayloadLen: n, StoredLen: n},
			EntryCount:   uint32(len(entries)), Entries: entries,
		}
		buf := make([]byte, tr.EncodedLen())
		if _, err := tr.Encode(buf); err != nil {
			tb.Fatal(err)
		}
		return buf
	})
}

// snapshot writes a snapshot of root at the time at.
func (r *synthRepo) snapshot(tb testing.TB, root object.ID, at time.Time) object.ID {
	return r.write(tb, format.ObjectKindSnapshot, func(n uint64) []byte {
		s := format.Snapshot{
			Common:   synthHeader(format.MagicSnapshot, format.SnapshotHeaderLen),
			Object:   format.ObjectHeader{Kind: format.ObjectKindSnapshot, HashAlgo: format.HashAlgoSHA256, PayloadLen: n, StoredLen: n},
			RootTree: root, TimeSec: at.Unix(), TimeNsec: uint32(at.Nanosecond()),
		}
		buf := make([]byte, s.EncodedLen())
		if _, err := s.Encode(buf); err != nil {
			tb.Fatal(err)
		}
		return buf
	})
}

// synthEntry is one tree entry named name.
func synthEntry(kind uint8, name string, id object.ID) format.TreeEntry {
	mode := uint32(0o100644)
	if kind == format.EntryTypeDirectory {
		mode = 0o40755
	}
	return format.TreeEntry{EntryType: kind, Mode: mode, Name: []byte(name), ContentID: id}
}

// synthDir writes a directory tree of files files, each one blob of one
// chunk. The chunk ids come from seed and the file number.
func (r *synthRepo) synthDir(tb testing.TB, seed string, files int) object.ID {
	entries := make([]format.TreeEntry, files)
	for i := range files {
		chunkID := object.ComputeID(format.ObjectKindChunk, fmt.Appendf(nil, "%s/%d", seed, i))
		r.ids = append(r.ids, chunkID)
		entries[i] = synthEntry(format.EntryTypeRegular, fmt.Sprintf("f%07d", i), r.blob(tb, chunkID))
	}
	return r.tree(tb, entries)
}

// newSynthRepo makes the directory and the chunk file of a synthetic
// repository.
func newSynthRepo(tb testing.TB) *synthRepo {
	dir := tb.TempDir()
	r := &synthRepo{dir: dir, chunk: filepath.Join(dir, "chunk")}
	if err := os.WriteFile(r.chunk, make([]byte, 164), 0o644); err != nil {
		tb.Fatal(err)
	}
	return r
}

// stage records each object of r as Staged, and returns the log.
func (r *synthRepo) stage(tb testing.TB) *stage.Log {
	l, err := stage.Open(r.dir)
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < len(r.ids); i += 100_000 {
		if err := l.EnsureStaged(r.ids[i:min(i+100_000, len(r.ids))]...); err != nil {
			tb.Fatal(err)
		}
	}
	return l
}

// peakRSS returns the peak resident set size of the process in bytes,
// and resets the peak.
func peakRSS(tb testing.TB) uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		tb.Skip("no /proc/self/status")
	}
	var peak uint64
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			kb, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			peak = kb * 1024
		}
	}
	runtime.GC()
	debug.FreeOSMemory()
	_ = os.WriteFile("/proc/self/clear_refs", []byte("5"), 0)
	return peak
}

// BenchmarkPackMemory1M measures the peak memory of the state log load,
// pack --dry-run and the work of status, for one snapshot of one million
// small files, each one blob of one chunk. Run it on its own:
// go test ./internal/image -run '^$' -bench PackMemory1M -benchtime 1x.
func BenchmarkPackMemory1M(b *testing.B) {
	const dirs, perDir = 1000, 1000
	r := newSynthRepo(b)
	root := make([]format.TreeEntry, dirs)
	for d := range dirs {
		root[d] = synthEntry(format.EntryTypeDirectory, fmt.Sprintf("d%04d", d), r.synthDir(b, fmt.Sprint(d), perDir))
	}
	snapID := r.snapshot(b, r.tree(b, root), fixedClock())
	_ = r.stage(b)
	b.ResetTimer()
	for range b.N {
		peakRSS(b)
		l, err := stage.Open(r.dir)
		if err != nil {
			b.Fatal(err)
		}
		logPeak := peakRSS(b)
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		b.ReportMetric(float64(ms.HeapAlloc)/(1<<20), "log-heap-MiB")
		start := time.Now()
		opts := packOpts(r.dir, snapID, b.TempDir(), 12_219_392, 1, l)
		opts.ObjectPath = r.objectPath
		discs, err := DryRun(opts, nil)
		if err != nil || len(discs) == 0 {
			b.Fatalf("DryRun: %v, %d discs", err, len(discs))
		}
		dryTime := time.Since(start)
		dryPeak := peakRSS(b)
		start = time.Now()
		if _, _, _, err := StagedTotals(r.objectPath, l); err != nil {
			b.Fatal(err)
		}
		groups, _ := PackGroups(r.objectPath, []object.ID{snapID}, l)
		if len(groups) != 1 || groups[0].StagedItems != len(r.ids) {
			b.Fatalf("groups %+v, want one of %d items", groups, len(r.ids))
		}
		statusTime := time.Since(start)
		statusPeak := peakRSS(b)
		b.ReportMetric(float64(logPeak)/(1<<20), "log-peak-MiB")
		b.ReportMetric(float64(dryPeak)/(1<<20), "dryrun-peak-MiB")
		b.ReportMetric(float64(statusPeak)/(1<<20), "status-peak-MiB")
		b.ReportMetric(dryTime.Seconds(), "dryrun-s")
		b.ReportMetric(statusTime.Seconds(), "status-s")
		runtime.KeepAlive(l)
	}
}

// BenchmarkStatus1000Snapshots measures the work of status for 1000
// staged snapshots that share one directory of 1000 files, each with one
// file of its own.
func BenchmarkStatus1000Snapshots(b *testing.B) {
	r := newSynthRepo(b)
	shared := r.synthDir(b, "shared", 1000)
	var snaps []object.ID
	for i := range 1000 {
		own := r.synthDir(b, fmt.Sprintf("own%d", i), 1)
		root := r.tree(b, []format.TreeEntry{
			synthEntry(format.EntryTypeDirectory, "own", own),
			synthEntry(format.EntryTypeDirectory, "shared", shared),
		})
		snaps = append(snaps, r.snapshot(b, root, fixedClock().Add(time.Duration(i)*time.Second)))
	}
	l := r.stage(b)
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		if _, _, _, err := StagedTotals(r.objectPath, l); err != nil {
			b.Fatal(err)
		}
		groups, _ := PackGroups(r.objectPath, snaps, l)
		if len(groups) != 1000 {
			b.Fatalf("%d groups, want 1000", len(groups))
		}
		b.ReportMetric(time.Since(start).Seconds(), "status-s")
	}
}

// mustSnapshotIDs lists every snapshot of the test repository dir.
func mustSnapshotIDs(t *testing.T, dir string) []object.ID {
	t.Helper()
	ids, err := testSnapshotIDs(dir)()
	if err != nil {
		t.Fatal(err)
	}
	return ids
}
