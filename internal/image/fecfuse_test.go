package image

import (
	"bytes"
	"fmt"
	mrand "math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestBuildFECToDiskDigestsMatchHashing checks that buildFECToDisk gives
// byte-identical checksum.bin and parity files whether it hashes each
// stripe's blocks itself (digests nil, the pre-fuse behaviour) or is
// handed digests already computed while the stream's bytes were placed
// (the fused pass's own path). This is the core correctness risk the
// fuse introduces: the checksum column must not depend on which pass
// computed its digests.
func TestBuildFECToDiskDigestsMatchHashing(t *testing.T) {
	rng := mrand.New(mrand.NewSource(7))
	sizes := []uint64{0, 100, fec.BlockSize, fec.BlockSize*2 + 500, 3*fec.BlockSize + 1, 600_000}
	var sources []streamSource
	var streamSizes []uint64
	for _, sz := range sizes {
		data := make([]byte, sz)
		rng.Read(data)
		sources = append(sources, streamSource{data: data, size: sz})
		streamSizes = append(streamSizes, sz)
	}

	layout, err := fec.NewStreamLayout(streamSizes, fec.K)
	if err != nil {
		t.Fatal(err)
	}

	// Digests computed the way the fused write pass computes them: fed
	// in stream order, one file at a time, padded to a block boundary
	// per file, then padded again out to fec.K*L to cover the last
	// column's own padding.
	digester := newBlockDigester(uint64(fec.K) * layout.StripeCount())
	for _, src := range sources {
		if _, err := digester.Write(src.data); err != nil {
			t.Fatal(err)
		}
		digester.FinishFile()
	}
	digester.Wait()
	digester.PadRemaining()

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	buildOne := func(dir string, digests [][8]byte) {
		t.Helper()
		checksumPath := filepath.Join(dir, "checksum.bin")
		parityPaths := make([]string, fec.M)
		for j := range fec.M {
			parityPaths[j] = filepath.Join(dir, fmt.Sprintf("p%04d.bin", j))
		}
		if err := buildFECToDisk(sources, layout, checksumPath, parityPaths, digests, nil); err != nil {
			t.Fatal(err)
		}
	}
	buildOne(dir1, nil)
	buildOne(dir2, digester.digests)

	compareTrees(t, dir1, dir2)
}

// stageMixedFixture commits two snapshots of a source tree that mixes
// an empty file, a file under one FEC block, a file exactly a multiple
// of the block size, a multi-chunk pseudo-random file, and many fanout
// directories, and returns the staging directory plus both snapshot
// refs in commit order.
func stageMixedFixture(t *testing.T) (string, []SnapshotRef) {
	t.Helper()
	stagingDir := t.TempDir()
	w := object.NewWriter(stagingDir)
	w.Now = fixedClock

	build := func(seed int64, extra bool) object.ID {
		srcDir := t.TempDir()
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(os.WriteFile(filepath.Join(srcDir, "empty.bin"), nil, 0o644))
		must(os.WriteFile(filepath.Join(srcDir, "under_block.bin"), []byte("short content, well under one FEC block"), 0o644))
		exact := make([]byte, 3*fec.BlockSize)
		mrand.New(mrand.NewSource(seed)).Read(exact)
		must(os.WriteFile(filepath.Join(srcDir, "exact_multiple.bin"), exact, 0o644))
		multi := make([]byte, 3<<20)
		mrand.New(mrand.NewSource(seed + 1)).Read(multi)
		must(os.WriteFile(filepath.Join(srcDir, "multi_chunk.bin"), multi, 0o644))

		fanoutRoot := filepath.Join(srcDir, "fanout")
		for i := range 40 {
			dir := filepath.Join(fanoutRoot, fmt.Sprintf("d%d", i))
			must(os.MkdirAll(dir, 0o755))
			must(os.WriteFile(filepath.Join(dir, "f.txt"), fmt.Appendf(nil, "fanout file %d seed %d", i, seed), 0o644))
		}
		if extra {
			must(os.WriteFile(filepath.Join(srcDir, "second_snapshot_only.bin"), []byte("only in the second snapshot"), 0o644))
		}

		snapID, _, err := w.Commit(srcDir)
		must(err)
		return snapID
	}

	first := build(1, false)
	second := build(2, true)

	return stagingDir, []SnapshotRef{
		{Name: "FIRST", ID: first, Time: fixedClock()},
		{Name: "LATEST", ID: second, Time: fixedClock().Add(time.Hour)},
	}
}

// TestBuildMixedFixtureFusedIsDeterministicAndVerifies runs Build twice
// over the mixed fixture with FEC on, checks the two runs are
// byte-identical (the fused digest and parity pass gives the same
// answer every time over the same input), and that Read verifies each
// one, including its checksum column and parity.
func TestBuildMixedFixtureFusedIsDeterministicAndVerifies(t *testing.T) {
	stagingDir, snaps := stageMixedFixture(t)

	opts := func(outDir string) BuildOptions {
		return BuildOptions{
			StagingDir:              stagingDir,
			Snapshots:               snaps,
			TargetCapacitySectors:   1 << 22,
			PhysicalCapacitySectors: 1 << 22,
			OutputDir:               outDir,
			RepoUUID:                [16]byte{1, 2, 3, 4},
			DiscUUID:                [16]byte{5, 6, 7, 8},
			Label:                   "mixed-fixture",
			FECEnabled:              true,
			Now:                     fixedClock,
		}
	}

	outDir1 := t.TempDir()
	if _, err := Build(opts(outDir1)); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(outDir1); err != nil {
		t.Fatalf("Read outDir1: %v", err)
	}

	outDir2 := t.TempDir()
	if _, err := Build(opts(outDir2)); err != nil {
		t.Fatal(err)
	}

	compareTrees(t, outDir1, outDir2)
}

// TestBuildMixedFixtureFECOffMatchesFECOn checks that turning FEC on
// does not change any byte of a row that both runs share: every file
// outside checksum.bin and the parity directory must be identical.
func TestBuildMixedFixtureFECOffMatchesFECOn(t *testing.T) {
	stagingDir, snaps := stageMixedFixture(t)
	base := BuildOptions{
		StagingDir:              stagingDir,
		Snapshots:               snaps,
		TargetCapacitySectors:   1 << 22,
		PhysicalCapacitySectors: 1 << 22,
		RepoUUID:                [16]byte{1, 2, 3, 4},
		DiscUUID:                [16]byte{5, 6, 7, 8},
		Label:                   "mixed-fixture",
		Now:                     fixedClock,
	}

	offDir := t.TempDir()
	offOpts := base
	offOpts.OutputDir = offDir
	offOpts.FECEnabled = false
	if _, err := Build(offOpts); err != nil {
		t.Fatal(err)
	}

	onDir := t.TempDir()
	onOpts := base
	onOpts.OutputDir = onDir
	onOpts.FECEnabled = true
	if _, err := Build(onOpts); err != nil {
		t.Fatal(err)
	}

	err := filepath.WalkDir(offDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(offDir, path)
		if err != nil {
			return err
		}
		if rel == filepath.Join("NOAHSARK", "runs", "0000000001", "RUN.bin") ||
			rel == filepath.Join("NOAHSARK", "runs", "0000000001", "RUN2.bin") ||
			rel == filepath.Join("NOAHSARK", "runs", "0000000001", "INDEX.bin") {
			// RUN carries fec_scheme and the checksum/parity geometry;
			// INDEX carries RUN2's row and the checksum/parity Files
			// rows: both legitimately differ between FEC off and on.
			return nil
		}
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(onDir, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
			return nil
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("%s: differs between FEC off and FEC on", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// timingFixtureBytes is the streamed pseudo-random content size the
// pack timing test packs, once with FEC off and once with FEC on.
const timingFixtureBytes = 512 << 20

// mbPerSec returns n bytes over d as megabytes per second.
func mbPerSec(n int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / (1 << 20) / d.Seconds()
}

// TestPackTiming512MiB packs the same size of streamed content once
// with FEC off and once with FEC on, and logs both times: the number
// the media/bd25 timing line is read from.
func TestPackTiming512MiB(t *testing.T) {
	if testing.Short() {
		t.Skip("timing benchmark, skipped under -short")
	}
	capacitySectors := sectorsFor(900 << 20)

	run := func(fecEnabled bool) time.Duration {
		srcDir := t.TempDir()
		writeStreamedRandomFile(t, filepath.Join(srcDir, "big.bin"), timingFixtureBytes)
		stagingDir := t.TempDir()
		w := object.NewWriter(stagingDir)
		w.Now = fixedClock
		snapID, _, err := w.Commit(srcDir)
		if err != nil {
			t.Fatal(err)
		}
		l, err := stage.Open(stagingDir)
		if err != nil {
			t.Fatal(err)
		}
		markStagedFromCommit(t, stagingDir, snapID, l)

		outDir := t.TempDir()
		opts := packOpts(stagingDir, snapID, outDir, capacitySectors, 1, l)
		opts.FECEnabled = fecEnabled

		start := time.Now()
		if _, err := Pack(opts); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}

	offTime := run(false)
	onTime := run(true)
	t.Logf("pack 512MiB FEC off: %s (%.1f MB/s)", offTime, mbPerSec(timingFixtureBytes, offTime))
	t.Logf("pack 512MiB FEC on:  %s (%.1f MB/s)", onTime, mbPerSec(timingFixtureBytes, onTime))
}
