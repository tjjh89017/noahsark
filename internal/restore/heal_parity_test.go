package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/image"
)

// parityRange returns the parity columns from, from+1, ..., to.
func parityRange(from, to int) []int {
	var cols []int
	for j := from; j <= to; j++ {
		cols = append(cols, j)
	}
	return cols
}

// TestHealRepairsDamagedParity damages data and parity blocks of stripe 0
// and heals the tree into a new directory. Each case damages at most m
// blocks. The healed tree must pass the full check, and the report must
// name each damaged block.
func TestHealRepairsDamagedParity(t *testing.T) {
	cases := []struct {
		name   string
		data   []uint64
		parity []int
	}{
		{"parity only", nil, []int{0, 1}},
		{"every parity block", nil, parityRange(0, fec.M-1)},
		{"one data block and parity 1 to 22", []uint64{2}, parityRange(1, fec.M-1)},
		{"two data blocks and parity 2 to 22", []uint64{2, 3}, parityRange(2, fec.M-1)},
		{"one data block, parity 0 and parity 2 to 22", []uint64{2}, append([]int{0}, parityRange(2, fec.M-1)...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if n := len(tc.data) + len(tc.parity); n > fec.M {
				t.Fatalf("the case damages %d blocks, more than m", n)
			}
			srcDir := buildFixtureSrc(t)
			_, treeDir, _ := buildFixtureTree(t, srcDir)
			paths, sizes, layout := streamLayout(t, treeDir)
			for _, col := range tc.data {
				corruptDataBlockAt(t, paths, sizes, layout, col, 0)
			}
			for _, j := range tc.parity {
				corruptParityBlock(t, treeDir, j, 0)
			}
			if _, err := image.Read(treeDir); err == nil {
				t.Fatal("the damaged tree passes the full check")
			}

			outDir := filepath.Join(t.TempDir(), "healed")
			reports, err := Heal(treeDir, outDir)
			if err != nil {
				t.Fatalf("Heal: %v", err)
			}
			if len(reports) != 1 || reports[0].Stripe != 0 {
				t.Fatalf("reports %+v, want one report for stripe 0", reports)
			}
			var wantData []int
			for _, col := range tc.data {
				wantData = append(wantData, int(col))
			}
			if !slices.Equal(reports[0].DataColumns, wantData) {
				t.Fatalf("data columns %v, want %v", reports[0].DataColumns, wantData)
			}
			if !slices.Equal(reports[0].ParityColumns, tc.parity) {
				t.Fatalf("parity columns %v, want %v", reports[0].ParityColumns, tc.parity)
			}
			if _, err := image.Read(outDir); err != nil {
				t.Fatalf("the healed tree fails the full check: %v", err)
			}
		})
	}
}

// TestHealRefusesOneBlockAboveTheLimit damages one data block and every
// parity block of stripe 0: m+1 blocks. Heal must name the stripe and
// write no block of it.
func TestHealRefusesOneBlockAboveTheLimit(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, _ := buildFixtureTree(t, srcDir)
	paths, sizes, layout := streamLayout(t, treeDir)
	corruptDataBlockAt(t, paths, sizes, layout, 2, 0)
	for j := range fec.M {
		corruptParityBlock(t, treeDir, j, 0)
	}

	outDir := filepath.Join(t.TempDir(), "healed")
	_, err := Heal(treeDir, outDir)
	if err == nil || !strings.Contains(err.Error(), "stripe 0 ") {
		t.Fatalf("Heal: %v, want an error that names stripe 0", err)
	}

	outPaths, _, _ := streamLayout(t, outDir)
	for i := range paths {
		assertSameFile(t, paths[i], outPaths[i])
	}
	for j := range fec.M {
		assertSameFile(t, parityPath(t, treeDir, j), parityPath(t, outDir, j))
	}
}

func parityPath(t *testing.T, treeDir string, j int) string {
	t.Helper()
	base, err := image.FindNoahsark(treeDir, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(runDir, "parity", parityFileName(j))
}

func assertSameFile(t *testing.T, want, got string) {
	t.Helper()
	w, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	g, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w, g) {
		t.Fatalf("%s differs from the damaged %s: Heal wrote into a stripe it cannot decode", got, want)
	}
}
