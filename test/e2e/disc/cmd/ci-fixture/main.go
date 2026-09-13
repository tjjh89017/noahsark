// Command ci-fixture is CI-only tooling, not a NoahsArk command surface.
// It commits a small deterministic source tree, builds one run's
// NOAHSARK tree from it, and builds a UDF image from that tree with
// mkudffs, so an e2e scenario has a real disc image to loop-mount.
//
// Usage: ci-fixture WORKDIR [TARGET-SECTORS] [PHYSICAL-SECTORS] [CONTENT-BYTES]
//
// TARGET-SECTORS and PHYSICAL-SECTORS default to 512 MiB, comfortably
// above the small fixture tree; a scenario testing one media preset
// passes that preset's real sector counts so the empty image it builds
// is the real, sparse size for that preset.
//
// CONTENT-BYTES adds one more deterministic pseudo-random file of that
// size, on top of the two small fixed files always written. A scenario
// that needs its real data to span more than one FEC stripe (one stripe
// holds 231*2048 bytes) passes a size past that, e.g. corrupting a
// second stripe and proving a different one stayed untouched needs real
// data there to check.
//
// It prints three lines to stdout: the tree directory, the image path,
// and the source directory the fixture snapshot was committed from.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

func fixedClock() time.Time {
	return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
}

func main() {
	if len(os.Args) < 2 || len(os.Args) > 5 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-fixture WORKDIR [TARGET-SECTORS] [PHYSICAL-SECTORS] [CONTENT-BYTES]")
		os.Exit(2)
	}
	workDir := os.Args[1]
	srcDir := filepath.Join(workDir, "src")
	stagingDir := filepath.Join(workDir, "staging")
	treeDir := filepath.Join(workDir, "tree")
	imagePath := filepath.Join(workDir, "run.img")

	const defaultSectors = 1 << 18 // 512 MiB
	targetSectors := uint64(defaultSectors)
	if len(os.Args) >= 3 {
		targetSectors = mustSectors(os.Args[2])
	}
	physicalSectors := targetSectors
	if len(os.Args) >= 4 {
		physicalSectors = mustSectors(os.Args[3])
	}
	var contentBytes int
	if len(os.Args) == 5 {
		n, err := strconv.Atoi(os.Args[4])
		if err != nil || n < 0 {
			_, _ = fmt.Fprintln(os.Stderr, "ci-fixture: bad content byte count:", os.Args[4])
			os.Exit(2)
		}
		contentBytes = n
	}

	must(os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755))
	must(os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of a, for the CI fixture disc"), 0o644))
	must(os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("content of b, also for the CI fixture disc, a bit longer"), 0o644))
	if contentBytes > 0 {
		big := make([]byte, contentBytes)
		rand.New(rand.NewSource(1)).Read(big)
		must(os.WriteFile(filepath.Join(srcDir, "sub", "big.bin"), big, 0o644))
	}

	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	must(err)

	opts := image.BuildOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "LATEST", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors:   targetSectors,
		PhysicalCapacitySectors: physicalSectors,
		OutputDir:               treeDir,
		RepoUUID:                [16]byte{0xaa, 0xbb, 0xcc, 0xdd},
		DiscUUID:                [16]byte{0x11, 0x22, 0x33, 0x44},
		Label:                   "ci-fixture",
		Now:                     fixedClock,
	}
	_, err = image.Build(opts)
	must(err)

	must(image.MakeImage(treeDir, imagePath, physicalSectors))

	fmt.Println(treeDir)
	fmt.Println(imagePath)
	fmt.Println(srcDir)
}

func mustSectors(s string) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-fixture: bad sector count:", s)
		os.Exit(2)
	}
	return n
}

func must(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-fixture:", err)
		os.Exit(1)
	}
}
