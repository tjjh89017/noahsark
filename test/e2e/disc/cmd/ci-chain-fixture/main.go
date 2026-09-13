// Command ci-chain-fixture is CI-only tooling for the disc e2e chain
// scenario. It streams a large, deterministic, incompressible source
// tree to disk and never holds a whole file in memory: a few large
// multi-file, many medium files, a few tiny files, a handful of
// subdirectories, and a few symlinks. It also checks a restored tree
// against the manifests it wrote.
//
// Usage:
//
//	ci-chain-fixture gen SRC-DIR TOTAL-BYTES SEED SAMPLE-OUT FULL-OUT
//	ci-chain-fixture check DIR SAMPLE-IN FULL-IN
//
// gen prints, and also saves, a full manifest (every regular file's
// path and size, every symlink's path and target, one per line) and a
// sample manifest (a fixed-seed subset of regular files with a
// whole-file SHA-256). check recomputes the same full manifest for DIR
// and diffs it against FULL-IN, then rehashes only the sample files
// named in SAMPLE-IN and compares.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const (
	largeCount    = 3
	mediumCount   = 300
	tinyCount     = 100
	symlinkCount  = 6
	dirCount      = 6
	sampleCount   = 25
	largeFraction = 0.45
	tinyFraction  = 0.05
	// mediumFraction is the remainder: 1 - largeFraction - tinyFraction.
)

type fileSpec struct {
	relPath string
	size    int64
	seed    int64
	sampled bool
}

type symlinkSpec struct {
	relPath string
	target  string
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "gen":
		if len(os.Args) != 7 {
			usage()
		}
		runGen(os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6])
	case "check":
		if len(os.Args) != 5 {
			usage()
		}
		runCheck(os.Args[2], os.Args[3], os.Args[4])
	default:
		usage()
	}
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, "usage: ci-chain-fixture gen SRC-DIR TOTAL-BYTES SEED SAMPLE-OUT FULL-OUT")
	_, _ = fmt.Fprintln(os.Stderr, "       ci-chain-fixture check DIR SAMPLE-IN FULL-IN")
	os.Exit(2)
}

func runGen(srcDir, totalStr, seedStr, sampleOut, fullOut string) {
	total, err := strconv.ParseInt(totalStr, 10, 64)
	must(err)
	seed, err := strconv.ParseInt(seedStr, 10, 64)
	must(err)

	start := time.Now()
	plan := buildPlan(total, seed)
	symlinks := buildSymlinks(plan, seed)

	must(os.MkdirAll(srcDir, 0o755))
	for i := range dirCount {
		must(os.MkdirAll(filepath.Join(srcDir, dirName(i)), 0o755))
	}

	var sampleLines []string
	var written int64
	for _, f := range plan {
		full := filepath.Join(srcDir, f.relPath)
		sum, err := writeRandomFile(full, f.size, f.seed, f.sampled)
		must(err)
		written += f.size
		if f.sampled {
			sampleLines = append(sampleLines, f.relPath+"\t"+sum)
		}
	}
	for _, s := range symlinks {
		full := filepath.Join(srcDir, s.relPath)
		must(os.Symlink(s.target, full))
	}

	full := buildFullManifest(plan, symlinks)
	sort.Strings(sampleLines)

	must(writeLines(sampleOut, sampleLines))
	must(writeLines(fullOut, full))

	elapsed := time.Since(start)
	mbps := float64(written) / elapsed.Seconds() / (1 << 20)
	fmt.Printf("ci-chain-fixture: wrote %d bytes in %d files (%d symlinks) under %s in %s (%.1f MiB/s)\n",
		written, len(plan), len(symlinks), srcDir, elapsed.Round(time.Second), mbps)
	fmt.Println("--- sample manifest ---")
	for _, l := range sampleLines {
		fmt.Println(l)
	}
	fmt.Println("--- full manifest ---")
	for _, l := range full {
		fmt.Println(l)
	}
}

func runCheck(dir, sampleIn, fullIn string) {
	wantFull, err := readLines(fullIn)
	must(err)
	wantSample, err := readLines(sampleIn)
	must(err)

	gotFull, err := manifestOfDir(dir)
	must(err)

	if !equalLines(wantFull, gotFull) {
		printDiff(wantFull, gotFull)
		fmt.Fprintln(os.Stderr, "ci-chain-fixture check: full manifest mismatch")
		os.Exit(1)
	}

	mismatch := false
	for _, line := range wantSample {
		parts := bytes.SplitN([]byte(line), []byte("\t"), 2)
		if len(parts) != 2 {
			continue
		}
		rel, want := string(parts[0]), string(parts[1])
		got, err := hashFile(filepath.Join(dir, rel))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ci-chain-fixture check: %s: %v\n", rel, err)
			mismatch = true
			continue
		}
		if got != want {
			fmt.Fprintf(os.Stderr, "ci-chain-fixture check: %s: got %s want %s\n", rel, got, want)
			mismatch = true
		}
	}
	if mismatch {
		fmt.Fprintln(os.Stderr, "ci-chain-fixture check: sample hash mismatch")
		os.Exit(1)
	}
	fmt.Printf("ci-chain-fixture check: %s matches (%d files, %d sampled)\n", dir, len(wantFull), len(wantSample))
}

// buildPlan lays out largeCount+mediumCount+tinyCount regular files that
// sum to exactly total bytes: a few large files, many medium files, and
// a few tiny files, spread across the root and dirCount subdirectories.
// The plan and every file's per-file seed are fully determined by seed,
// so the same (total, seed) pair always produces the same tree.
func buildPlan(total int64, seed int64) []fileSpec {
	r := rand.New(rand.NewSource(seed))

	largeTotal := int64(float64(total) * largeFraction)
	tinyTotal := int64(float64(total) * tinyFraction)
	mediumTotal := total - largeTotal - tinyTotal

	var plan []fileSpec
	plan = append(plan, sizeGroup(r, "large", largeCount, largeTotal, seed, 1)...)
	plan = append(plan, sizeGroup(r, "medium", mediumCount, mediumTotal, seed, 2)...)
	plan = append(plan, sizeGroup(r, "tiny", tinyCount, tinyTotal, seed, 3)...)

	// Fix any rounding remainder on the first medium file: medium files
	// are large enough that a few stray bytes never make one negative.
	var sum int64
	for _, f := range plan {
		sum += f.size
	}
	remainder := total - sum
	for i := range plan {
		if len(plan[i].relPath) >= 6 && plan[i].relPath[:6] == "medium" {
			plan[i].size += remainder
			break
		}
	}

	assignDirs(plan)
	assignSamples(plan, seed)
	sort.Slice(plan, func(i, j int) bool { return plan[i].relPath < plan[j].relPath })
	return plan
}

// sizeGroup builds n files of prefix "kind" whose sizes are random
// weighted shares of groupTotal bytes, seeded so the shares (and every
// file's own content seed) are reproducible.
func sizeGroup(r *rand.Rand, kind string, n int, groupTotal int64, baseSeed int64, salt int64) []fileSpec {
	if n == 0 {
		return nil
	}
	weights := make([]float64, n)
	var wsum float64
	for i := range weights {
		weights[i] = r.Float64() + 0.2
		wsum += weights[i]
	}
	out := make([]fileSpec, n)
	var used int64
	for i := range out {
		size := int64(groupTotal * int64(weights[i]*1e6) / int64(wsum*1e6))
		if i == n-1 {
			size = groupTotal - used
		}
		if size < 1 {
			size = 1
		}
		used += size
		out[i] = fileSpec{
			relPath: fmt.Sprintf("%s-%04d.bin", kind, i),
			size:    size,
			seed:    baseSeed*1_000_003 + salt*100_003 + int64(i),
		}
	}
	return out
}

// assignDirs spreads every planned file across the root and dirCount
// subdirectories, in a fixed round-robin by the file's position in the
// (already-sorted-by-name) plan, so the split is deterministic.
func assignDirs(plan []fileSpec) {
	sort.Slice(plan, func(i, j int) bool { return plan[i].relPath < plan[j].relPath })
	for i := range plan {
		slot := i % (dirCount + 1)
		if slot == 0 {
			continue
		}
		plan[i].relPath = filepath.Join(dirName(slot-1), plan[i].relPath)
	}
}

// assignSamples marks sampleCount files, chosen by a seeded shuffle, as
// files whose whole content gets hashed while it is written.
func assignSamples(plan []fileSpec, seed int64) {
	r := rand.New(rand.NewSource(seed ^ 0x5a5a5a5a))
	idx := r.Perm(len(plan))
	n := min(sampleCount, len(plan))
	for _, i := range idx[:n] {
		plan[i].sampled = true
	}
}

// buildSymlinks adds symlinkCount symlinks, each pointing, with a
// relative target, at one of the regular files the plan already lists.
func buildSymlinks(plan []fileSpec, seed int64) []symlinkSpec {
	if len(plan) == 0 {
		return nil
	}
	r := rand.New(rand.NewSource(seed ^ 0x1234))
	out := make([]symlinkSpec, 0, symlinkCount)
	for i := range symlinkCount {
		targetFile := plan[r.Intn(len(plan))]
		linkRel := fmt.Sprintf("link-%02d", i)
		targetDir := filepath.Dir(linkRel)
		rel, _ := filepath.Rel(targetDir, targetFile.relPath)
		out = append(out, symlinkSpec{relPath: linkRel, target: rel})
	}
	return out
}

func dirName(i int) string { return fmt.Sprintf("dir%d", i) }

// writeRandomFile streams size deterministic pseudo-random bytes to
// path, one fixed-size buffer at a time, so a large file never sits in
// memory whole. When hash is true it also returns the file's SHA-256,
// computed from the same stream, never a second read.
func writeRandomFile(path string, size int64, seed int64, hash bool) (string, error) {
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	bw := bufio.NewWriterSize(f, 1<<20)
	var h io.Writer = bw
	sum := sha256.New()
	if hash {
		h = io.MultiWriter(bw, sum)
	}

	r := rand.New(rand.NewSource(seed))
	const bufSize = 1 << 20
	buf := make([]byte, bufSize)
	for remaining := size; remaining > 0; {
		chunk := min(remaining, int64(bufSize))
		r.Read(buf[:chunk])
		if _, err := h.Write(buf[:chunk]); err != nil {
			return "", err
		}
		remaining -= chunk
	}
	if err := bw.Flush(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if !hash {
		return "", nil
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// buildFullManifest lists every planned regular file's path and size,
// and every symlink's path and target, sorted by path.
func buildFullManifest(plan []fileSpec, symlinks []symlinkSpec) []string {
	lines := make([]string, 0, len(plan)+len(symlinks))
	for _, f := range plan {
		lines = append(lines, fmt.Sprintf("f\t%s\t%d", f.relPath, f.size))
	}
	for _, s := range symlinks {
		lines = append(lines, fmt.Sprintf("l\t%s\t%s", s.relPath, s.target))
	}
	sort.Strings(lines)
	return lines
}

// manifestOfDir walks dir and builds the same "f/l path size-or-target"
// manifest buildFullManifest writes, so a restored tree can be diffed
// against the original by path and size alone, without rehashing every
// file.
func manifestOfDir(dir string) ([]string, error) {
	var lines []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("l\t%s\t%s", rel, target))
		case info.IsDir():
			// directories are not listed in the manifest; only their
			// files and symlinks are.
		default:
			lines = append(lines, fmt.Sprintf("f\t%s\t%d", rel, info.Size()))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(lines)
	return lines, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func writeLines(path string, lines []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	for _, l := range lines {
		if _, err := w.WriteString(l + "\n"); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	for l := range bytes.SplitSeq(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		lines = append(lines, string(l))
	}
	sort.Strings(lines)
	return lines, nil
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func printDiff(want, got []string) {
	w := make(map[string]bool, len(want))
	for _, l := range want {
		w[l] = true
	}
	g := make(map[string]bool, len(got))
	for _, l := range got {
		g[l] = true
	}
	for _, l := range want {
		if !g[l] {
			fmt.Fprintln(os.Stderr, "- "+l)
		}
	}
	for _, l := range got {
		if !w[l] {
			fmt.Fprintln(os.Stderr, "+ "+l)
		}
	}
}

func must(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-chain-fixture:", err)
		os.Exit(1)
	}
}
