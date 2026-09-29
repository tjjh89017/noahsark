package restore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/image"
)

// unreadableBlocks names blocks of files that a test read fails on: the
// file path and the block numbers inside the file.
type unreadableBlocks map[string][]int64

// readAtFailing gives a ReadAt that fails with EIO on each read that
// touches a block of bad.
func readAtFailing(bad unreadableBlocks) func(*os.File, []byte, int64) (int, error) {
	return func(f *os.File, p []byte, off int64) (int, error) {
		for _, b := range bad[f.Name()] {
			start := b * fec.BlockSize
			if off < start+fec.BlockSize && start < off+int64(len(p)) {
				return 0, syscall.EIO
			}
		}
		return f.ReadAt(p, off)
	}
}

// dataBlockOf gives the file and the block number inside the file of the
// data block at (column, stripe) of treeDir.
func dataBlockOf(t *testing.T, treeDir string, column, stripe uint64) (string, int64) {
	t.Helper()
	paths, sizes, layout := streamLayout(t, treeDir)
	idx, off, err := layout.Locate(column*layout.StripeCount() + stripe)
	if err != nil {
		t.Fatal(err)
	}
	if off >= sizes[idx] {
		t.Fatalf("column %d stripe %d falls in padding", column, stripe)
	}
	return paths[idx], int64(off) / fec.BlockSize
}

func checksumPath(t *testing.T, treeDir string) string {
	t.Helper()
	base, err := image.FindNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(runDir, "checksum.bin")
}

// healUnreadable heals treeDir into a new directory with the reads of bad
// failing, and returns the result, the error and the new directory.
func healUnreadable(t *testing.T, treeDir string, bad unreadableBlocks) (*HealResult, string, error) {
	t.Helper()
	outDir := filepath.Join(t.TempDir(), "healed")
	res, err := HealWithOptions(treeDir, outDir, HealOptions{ReadAt: readAtFailing(bad)})
	return res, outDir, err
}

// TestHealUnreadableBlocks makes blocks of stripe 0 unreadable and heals
// the tree into a new directory. Each case holds at most m erasures. The
// healed tree must pass the full check, and the report must name each
// block that Heal wrote again.
func TestHealUnreadableBlocks(t *testing.T) {
	cases := []struct {
		name   string
		data   []uint64
		parity []int
	}{
		{"one data block", []uint64{5}, nil},
		{"parity blocks", nil, []int{0, 8, 22}},
		{"data and parity blocks", []uint64{3, 9}, []int{0, 1}},
		{"m data blocks, the limit", colRange(1, fec.M), nil},
		{"m data and parity blocks, the limit", colRange(1, fec.M-2), []int{0, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
			bad := unreadableBlocks{}
			for _, col := range tc.data {
				path, b := dataBlockOf(t, treeDir, col, 0)
				bad[path] = append(bad[path], b)
			}
			for _, j := range tc.parity {
				p := parityPath(t, treeDir, j)
				bad[p] = append(bad[p], 0)
			}

			res, outDir, err := healUnreadable(t, treeDir, bad)
			if err != nil {
				t.Fatalf("Heal: %v", err)
			}
			if len(res.Stripes) != 1 || res.Stripes[0].Stripe != 0 {
				t.Fatalf("reports %+v, want one report for stripe 0", res.Stripes)
			}
			var wantData []int
			for _, col := range tc.data {
				wantData = append(wantData, int(col))
			}
			if !slices.Equal(res.Stripes[0].DataColumns, wantData) {
				t.Fatalf("data columns %v, want %v", res.Stripes[0].DataColumns, wantData)
			}
			if !slices.Equal(res.Stripes[0].ParityColumns, tc.parity) {
				t.Fatalf("parity columns %v, want %v", res.Stripes[0].ParityColumns, tc.parity)
			}
			if _, err := image.Read(outDir); err != nil {
				t.Fatalf("the healed tree fails the full check: %v", err)
			}
		})
	}
}

func colRange(from, to uint64) []uint64 {
	var cols []uint64
	for c := from; c <= to; c++ {
		cols = append(cols, c)
	}
	return cols
}

// TestHealRefusesOneUnreadableBlockAboveTheLimit makes m data blocks and
// one parity block of stripe 0 unreadable. Heal must name the stripe and
// write nothing.
func TestHealRefusesOneUnreadableBlockAboveTheLimit(t *testing.T) {
	_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
	bad := unreadableBlocks{}
	for _, col := range colRange(1, fec.M) {
		path, b := dataBlockOf(t, treeDir, col, 0)
		bad[path] = append(bad[path], b)
	}
	p := parityPath(t, treeDir, 0)
	bad[p] = append(bad[p], 0)

	res, _, err := healUnreadable(t, treeDir, bad)
	if err == nil || !strings.Contains(err.Error(), "stripe 0 ") {
		t.Fatalf("Heal: %v, want an error that names stripe 0", err)
	}
	if len(res.Files) != 0 {
		t.Fatalf("Heal wrote %v for a stripe that it cannot decode", res.Files)
	}
}

// smallFileColumn gives a data column of stripe 0, after column 0, whose
// file holds at most maxBlocks blocks of stripe 0. A check through the
// content id makes each block of a damaged file an erasure, thus the file
// must be small.
func smallFileColumn(t *testing.T, treeDir string, maxBlocks int) uint64 {
	t.Helper()
	_, sizes, layout := streamLayout(t, treeDir)
	perFile := map[int][]uint64{}
	for c := range uint64(fec.K) {
		idx, off, err := layout.Locate(c * layout.StripeCount())
		if err != nil || off >= sizes[idx] {
			continue
		}
		perFile[idx] = append(perFile[idx], c)
	}
	for c := uint64(1); c < fec.K; c++ {
		idx, off, err := layout.Locate(c * layout.StripeCount())
		if err != nil || off >= sizes[idx] {
			continue
		}
		if len(perFile[idx]) <= maxBlocks {
			return c
		}
	}
	t.Fatal("the fixture has no small file in stripe 0")
	return 0
}

// TestHealWithAnUnreadableChecksumBlock makes the checksum block of
// stripe 0 unreadable. Heal must then check the files of the stripe
// through their content ids and file hashes, repair a damaged block, and
// write the checksum block again.
func TestHealWithAnUnreadableChecksumBlock(t *testing.T) {
	for _, damage := range []bool{false, true} {
		name := "no other damage"
		if damage {
			name = "a damaged data block"
		}
		t.Run(name, func(t *testing.T) {
			_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
			var wantData []int
			if damage {
				col := smallFileColumn(t, treeDir, 4)
				paths, sizes, layout := streamLayout(t, treeDir)
				corruptDataBlockAt(t, paths, sizes, layout, col, 0)
				wantData = []int{int(col)}
			}
			res, outDir, err := healUnreadable(t, treeDir, unreadableBlocks{checksumPath(t, treeDir): {0}})
			if err != nil {
				t.Fatalf("Heal: %v", err)
			}
			if len(res.Stripes) != 1 || res.Stripes[0].Stripe != 0 || !res.Stripes[0].Checksum {
				t.Fatalf("reports %+v, want one report for stripe 0 with the checksum block", res.Stripes)
			}
			if !slices.Equal(res.Stripes[0].DataColumns, wantData) {
				t.Fatalf("data columns %v, want %v", res.Stripes[0].DataColumns, wantData)
			}
			if _, err := image.Read(outDir); err != nil {
				t.Fatalf("the healed tree fails the full check: %v", err)
			}
		})
	}
}

// TestHealCopyFixesTheLengthOfAColumnFile cuts the last block from one
// parity file and adds a byte to another. A short read is an erasure, and
// the healed tree holds each file at its length.
func TestHealCopyFixesTheLengthOfAColumnFile(t *testing.T) {
	_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
	short := parityPath(t, treeDir, 1)
	fi, err := os.Stat(short)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(short, fi.Size()-fec.BlockSize); err != nil {
		t.Fatal(err)
	}
	long, err := os.OpenFile(parityPath(t, treeDir, 2), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := long.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := long.Close(); err != nil {
		t.Fatal(err)
	}

	res, outDir, err := healUnreadable(t, treeDir, nil)
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("Heal wrote %v, want the two parity files", res.Files)
	}
	if _, err := image.Read(outDir); err != nil {
		t.Fatalf("the healed tree fails the full check: %v", err)
	}
}

// TestHealUsesRun2WhenRunIsDamaged damages RUN.bin. Heal must take the run
// header from RUN2.bin and write RUN.bin again from it.
func TestHealUsesRun2WhenRunIsDamaged(t *testing.T) {
	_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
	runPath := filepath.Join(filepath.Dir(checksumPath(t, treeDir)), "RUN.bin")
	flipByte(t, runPath, 64)

	res, outDir, err := healUnreadable(t, treeDir, nil)
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	want := filepath.Join("runs", "0000000001", "RUN.bin")
	if !slices.Equal(res.Files, []string{want}) {
		t.Fatalf("Heal wrote %v, want %s", res.Files, want)
	}
	rr, err := image.Read(outDir)
	if err != nil {
		t.Fatalf("the healed tree fails the full check: %v", err)
	}
	if rr.RunCopies != 2 {
		t.Fatalf("RunCopies = %d, want 2", rr.RunCopies)
	}
}
