package image

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// dvdrCapacitySectors is a real dvd+r capacity, used to check the
// overhead estimate against realistic numbers instead of the small
// forced capacities the rest of this package's tests use for speed.
const dvdrCapacitySectors = 2_295_104

// TestOverheadMarginAtDVDRCapacity guards the estimate's own safety
// margin: the fixed and proportional margin terms, over and above the
// itemised bitmap, per-file and per-directory costs, must leave at
// least 8 MiB of slack at dvd+r capacity. A regression that shrinks the
// margin back toward a thin buffer, the kind that failed against a real
// mkudffs image before this model, fails this test.
func TestOverheadMarginAtDVDRCapacity(t *testing.T) {
	proportional := uint64(dvdrCapacitySectors) * SectorSize * filesystemMarginNumerator / filesystemMarginDenominator
	slack := uint64(filesystemMarginBytes) + proportional
	const wantMinSlack = 8 << 20
	if slack < wantMinSlack {
		t.Fatalf("margin at dvd+r capacity with 1000 files is %d bytes, want at least %d bytes", slack, wantMinSlack)
	}
	t.Logf("dvd+r margin: %d bytes (%.2f MiB)", slack, float64(slack)/(1<<20))
}

// TestEstimateNeverExceedsCapacityAtDVDR checks that EstimateFilesystemOverhead
// itself never claims more than dvd+r capacity for a realistic file
// count, so DataBudgetBlocks always has something left for data.
func TestEstimateNeverExceedsCapacityAtDVDR(t *testing.T) {
	overhead := EstimateFilesystemOverhead(1000, dvdrCapacitySectors)
	limit := uint64(dvdrCapacitySectors) * SectorSize
	if overhead >= limit {
		t.Fatalf("overhead %d bytes at dvd+r capacity leaves no room for data (limit %d bytes)", overhead, limit)
	}
}

// realUDFOverheadEnvVar, when set, runs the measurement tests below
// against a real mkudffs image. They need root for the loop mount and
// mkudffs itself, so they run only under CI.
const realUDFOverheadEnvVar = "NOAHSARK_CI"

// udfCapacityCase names one capacity this measurement covers: a real
// dvd+r, and two round byte sizes for comparison at other media scales.
type udfCapacityCase struct {
	name    string
	sectors uint64
}

var udfCapacityCases = []udfCapacityCase{
	{"dvd+r", dvdrCapacitySectors},
	{"4GiB", (4 << 30) / SectorSize},
	{"1GiB", (1 << 30) / SectorSize},
}

// fanoutDirFor mirrors object.ID.FanoutByte: a two-hex-digit directory
// name, spreading files over objectFanoutDirs directories the way the
// real objects/<ab> tree does.
func fanoutDirFor(i int) string {
	return fmt.Sprintf("%02x", i%objectFanoutDirs)
}

// TestRealUDFOverheadMeasurement builds a real mkudffs UDF 2.01 image at
// each of dvd+r, 4 GiB and 1 GiB, loop-mounts it, and measures the real
// usable bytes: df of the empty mount, then the per-file cost of adding
// files in batches of 1000, 5000 and 20000, spread over a 256-directory
// fanout the way the real objects tree is. It needs root and mkudffs, so
// it runs only under NOAHSARK_CI; otherwise it skips.
func TestRealUDFOverheadMeasurement(t *testing.T) {
	if os.Getenv(realUDFOverheadEnvVar) == "" {
		t.Skip("NOAHSARK_CI not set; skipping the real mkudffs overhead measurement")
	}
	if _, err := exec.LookPath("mkudffs"); err != nil {
		t.Skip("mkudffs not installed")
	}
	if _, err := CheckTools(); err != nil {
		t.Skipf("udftools too old: %v", err)
	}
	if os.Geteuid() != 0 {
		t.Skip("measurement needs root for the loop mount")
	}

	for _, c := range udfCapacityCases {
		t.Run(c.name, func(t *testing.T) {
			measureRealUDFOverhead(t, c.sectors)
		})
	}
}

// buildEmptyUDFImage makes an empty UDF 2.01 image at imagePath, sized
// sectors, the same way MakeImage does but without the NOAHSARK-tree
// populate step: this measurement mounts and writes its own fanout of
// probe files instead.
func buildEmptyUDFImage(imagePath string, sectors uint64) error {
	size := int64(sectors) * SectorSize
	f, err := os.Create(imagePath)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	cmd := exec.Command("mkudffs",
		"--utf8",
		"--media-type=hd",
		"--blocksize=2048",
		"--udfrev=2.01",
		"--uid=0",
		"--gid=0",
		"--mode=0555",
		"--bootarea=erase",
		imagePath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkudffs: %w: %s", err, out)
	}
	return nil
}

// measureRealUDFOverhead builds and mounts one image, then prints df's
// available bytes empty and after each file batch.
func measureRealUDFOverhead(t *testing.T, sectors uint64) {
	t.Helper()
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "measure.img")
	if err := buildEmptyUDFImage(imagePath, sectors); err != nil {
		t.Fatalf("mkudffs: %v", err)
	}

	mnt := t.TempDir()
	mount := exec.Command("sudo", "mount", "-o", "loop", "-t", "udf", imagePath, mnt)
	if out, err := mount.CombinedOutput(); err != nil {
		t.Fatalf("mount: %v: %s", err, out)
	}
	defer func() {
		_ = exec.Command("sudo", "umount", mnt).Run()
	}()

	avail0, err := dfAvailBytes(mnt)
	if err != nil {
		t.Fatalf("df: %v", err)
	}
	t.Logf("sectors=%d empty mount available bytes=%d", sectors, avail0)

	prevAvail := avail0
	prevCount := 0
	i := 0
	for _, target := range []int{1000, 5000, 20000} {
		for ; i < target; i++ {
			d := filepath.Join(mnt, "fanout", fanoutDirFor(i))
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			f := filepath.Join(d, fmt.Sprintf("f%d", i))
			if err := os.WriteFile(f, make([]byte, SectorSize), 0o644); err != nil {
				t.Fatalf("write %s: %v", f, err)
			}
		}
		_ = exec.Command("sync").Run()
		avail, err := dfAvailBytes(mnt)
		if err != nil {
			t.Fatalf("df: %v", err)
		}
		used := prevAvail - avail
		added := i - prevCount
		t.Logf("sectors=%d after %d files: available=%d used-since-last=%d files-added=%d bytes/file=%.2f",
			sectors, i, avail, used, added, float64(used)/float64(added))
		prevAvail = avail
		prevCount = i
	}
}

// dfAvailBytes runs df on mnt and returns the available byte count from
// its second line.
func dfAvailBytes(mnt string) (uint64, error) {
	out, err := exec.Command("df", "-B1", "--output=avail", mnt).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output: %q", out)
	}
	return strconv.ParseUint(strings.TrimSpace(lines[1]), 10, 64)
}
