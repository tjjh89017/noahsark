// Command ci-fixture is CI-only tooling, not a NoahsArk command surface.
// It commits a small deterministic source tree, builds one run's
// NOAHSARK tree from it, and builds a UDF image from that tree with
// mkudffs, so the CI action has a real disc image to loop-mount.
//
// Usage: ci-fixture WORKDIR
//
// It prints three lines to stdout: the tree directory, the image path,
// and the source directory the fixture snapshot was committed from.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

func fixedClock() time.Time {
	return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ci-fixture WORKDIR")
		os.Exit(2)
	}
	workDir := os.Args[1]
	srcDir := filepath.Join(workDir, "src")
	stagingDir := filepath.Join(workDir, "staging")
	treeDir := filepath.Join(workDir, "tree")
	imagePath := filepath.Join(workDir, "run.img")

	must(os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755))
	must(os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of a, for the CI fixture disc"), 0o644))
	must(os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("content of b, also for the CI fixture disc, a bit longer"), 0o644))

	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	must(err)

	const targetSectors = 1 << 18 // 512 MiB, comfortably above this fixture
	opts := image.BuildOptions{
		StagingDir:              stagingDir,
		Snapshots:               []image.SnapshotRef{{Name: "LATEST", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors:   targetSectors,
		PhysicalCapacitySectors: targetSectors,
		OutputDir:               treeDir,
		RepoUUID:                [16]byte{0xaa, 0xbb, 0xcc, 0xdd},
		DiscUUID:                [16]byte{0x11, 0x22, 0x33, 0x44},
		Label:                   "ci-fixture",
		Now:                     fixedClock,
	}
	_, err = image.Build(opts)
	must(err)

	must(image.MakeImage(treeDir, imagePath, targetSectors))

	fmt.Println(treeDir)
	fmt.Println(imagePath)
	fmt.Println(srcDir)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci-fixture:", err)
		os.Exit(1)
	}
}
