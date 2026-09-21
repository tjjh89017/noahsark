// Command ci-fixture is CI-only tooling, not a NoahsArk command surface.
// It commits a small deterministic source tree, builds one run's
// NOAHSARK tree from it, and builds a UDF image from that tree with
// mkudffs, so an e2e scenario has a real disc image to loop-mount.
//
// Usage: ci-fixture [-fec] WORKDIR [TARGET-SECTORS] [CONTENT-BYTES]
//
// -fec builds the run with Reed-Solomon FEC (fec_scheme 1). Without it
// the run carries no FEC (fec_scheme 0), the default. A scenario that
// corrupts and heals a disc needs -fec; there is nothing to heal
// otherwise.
//
// TARGET-SECTORS defaults to 512 MiB, comfortably above the small
// fixture tree; a scenario testing one media preset passes that
// preset's real sector count so the empty image it builds is the
// real, sparse size for that preset.
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
	"bufio"
	"flag"
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
	fs := flag.NewFlagSet("ci-fixture", flag.ExitOnError)
	fec := fs.Bool("fec", false, "build the run with Reed-Solomon FEC (fec_scheme 1)")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-fixture [-fec] WORKDIR [TARGET-SECTORS] [CONTENT-BYTES]")
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	args := fs.Args()
	if len(args) < 1 || len(args) > 3 {
		fs.Usage()
		os.Exit(2)
	}
	workDir := args[0]
	srcDir := filepath.Join(workDir, "src")
	stagingDir := filepath.Join(workDir, "staging")
	treeDir := filepath.Join(workDir, "tree")
	imagePath := filepath.Join(workDir, "run.img")

	const defaultSectors = 1 << 18 // 512 MiB
	targetSectors := uint64(defaultSectors)
	if len(args) >= 2 {
		targetSectors = mustSectors(args[1])
	}
	var contentBytes int
	if len(args) == 3 {
		n, err := strconv.Atoi(args[2])
		if err != nil || n < 0 {
			_, _ = fmt.Fprintln(os.Stderr, "ci-fixture: bad content byte count:", args[2])
			os.Exit(2)
		}
		contentBytes = n
	}

	must(os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755))
	must(os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of a, for the CI fixture disc"), 0o644))
	must(os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("content of b, also for the CI fixture disc, a bit longer"), 0o644))
	if contentBytes > 0 {
		must(writeRandomFile(filepath.Join(srcDir, "sub", "big.bin"), contentBytes))
	}

	w := object.NewWriter(stagingDir)
	w.Now = fixedClock
	snapID, _, err := w.Commit(srcDir)
	must(err)

	opts := image.BuildOptions{
		StagingDir:            stagingDir,
		Snapshots:             []image.SnapshotRef{{Name: "2026-09-13", ID: snapID, Time: fixedClock()}},
		TargetCapacitySectors: targetSectors,
		OutputDir:             treeDir,
		RepoUUID:              [16]byte{0xaa, 0xbb, 0xcc, 0xdd},
		DiscUUID:              [16]byte{0x11, 0x22, 0x33, 0x44},
		Label:                 "ci-fixture",
		FECEnabled:            *fec,
		Now:                   fixedClock,
	}
	_, err = image.Build(opts)
	must(err)

	must(image.MakeImage(treeDir, imagePath, targetSectors, nil))

	fmt.Println(treeDir)
	fmt.Println(imagePath)
	fmt.Println(srcDir)
}

// writeRandomFile streams n deterministic pseudo-random bytes to path, one
// fixed-size buffer at a time, so a large fixture never sits in memory
// whole.
func writeRandomFile(path string, n int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := rand.New(rand.NewSource(1))
	const bufSize = 1 << 20
	buf := make([]byte, bufSize)
	w := bufio.NewWriter(f)
	for remaining := n; remaining > 0; {
		chunk := min(remaining, bufSize)
		r.Read(buf[:chunk])
		if _, err := w.Write(buf[:chunk]); err != nil {
			return err
		}
		remaining -= chunk
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
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
