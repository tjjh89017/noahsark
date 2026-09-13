// Command ci-incremental-fixture is CI-only tooling for the disc e2e
// incremental scenario. It builds a source tree of mixed file sizes,
// mutates it in place the way a real second commit would (new files,
// appended bytes, a full rewrite, deletions, a renamed directory), and
// checks a restored tree against a saved whole-file hash manifest.
//
// Usage:
//
//	ci-incremental-fixture gen SRC-DIR TOTAL-BYTES SEED HASHES-OUT PLAN-OUT
//	ci-incremental-fixture mutate SRC-DIR ADD-BYTES SEED PLAN-IN HASHES-IN NEXT-HASHES-OUT
//	ci-incremental-fixture check DIR HASHES-IN
//
// gen writes TOTAL-BYTES of deterministic content under SRC-DIR, a
// whole-file SHA-256 manifest to HASHES-OUT, and a plan of which paths
// mutate will later delete, append to, rewrite and rename to PLAN-OUT.
//
// mutate applies that plan to SRC-DIR: it adds ADD-BYTES of new files,
// appends to a few existing files, rewrites one file, deletes a few
// files, and renames the plan's rotate directory. It derives
// NEXT-HASHES-OUT from HASHES-IN plus only the paths it touched, so it
// never rereads the files it left alone.
//
// check walks DIR, hashes every regular file, and diffs the result
// against HASHES-IN.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	largeCount  = 8
	mediumCount = 150
	smallCount  = 80
	rotateCount = 5
	dirCount    = 5

	largeFraction  = 0.55
	smallFraction  = 0.05
	rotateFraction = 0.05
	// mediumFraction is the remainder: 1 - largeFraction - smallFraction - rotateFraction.

	deleteCount = 3
	appendCount = 3

	appendChunkBytes = 2 << 20 // 2 MiB appended to each chosen file
	rewriteBytes     = 8 << 20 // fixed size for the rewritten file

	rotateDirName = "rotate"
	addedDirName  = "added"
	addedCount    = 40
)

type fileSpec struct {
	relPath string
	size    int64
	seed    int64
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
	case "mutate":
		if len(os.Args) != 8 {
			usage()
		}
		runMutate(os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6], os.Args[7])
	case "check":
		if len(os.Args) != 4 {
			usage()
		}
		runCheck(os.Args[2], os.Args[3])
	default:
		usage()
	}
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, "usage: ci-incremental-fixture gen SRC-DIR TOTAL-BYTES SEED HASHES-OUT PLAN-OUT")
	_, _ = fmt.Fprintln(os.Stderr, "       ci-incremental-fixture mutate SRC-DIR ADD-BYTES SEED PLAN-IN HASHES-IN NEXT-HASHES-OUT")
	_, _ = fmt.Fprintln(os.Stderr, "       ci-incremental-fixture check DIR HASHES-IN")
	os.Exit(2)
}

// runGen builds the base fixture: a plan of large, medium, small and
// rotate-directory files, plus which of the non-rotate files mutate will
// later delete, append to and rewrite. It writes every file once,
// hashing it while it writes, so gen never rereads its own output.
func runGen(srcDir, totalStr, seedStr, hashesOut, planOut string) {
	total, err := strconv.ParseInt(totalStr, 10, 64)
	must(err)
	seed, err := strconv.ParseInt(seedStr, 10, 64)
	must(err)

	plan := buildPlan(total, seed)
	hashes := make(map[string]string, len(plan))
	for _, f := range plan {
		full := filepath.Join(srcDir, f.relPath)
		sum, err := writeRandomFile(full, f.size, f.seed)
		must(err)
		hashes[f.relPath] = sum
	}

	deletePaths, appendPaths, rewritePath := choosePlan(plan, seed)
	must(writePlan(planOut, deletePaths, appendPaths, rewritePath))
	must(writeHashes(hashesOut, hashes))

	fmt.Printf("ci-incremental-fixture: wrote %d bytes in %d files under %s\n", total, len(plan), srcDir)
}

// runMutate applies the plan gen recorded to srcDir: deletes, appends,
// rewrites, renames the rotate directory, then adds ADD-BYTES of new
// files under "added/". It builds NEXT-HASHES-OUT from HASHES-IN plus
// only the paths it touched.
func runMutate(srcDir, addStr, seedStr, planIn, hashesIn, hashesOut string) {
	add, err := strconv.ParseInt(addStr, 10, 64)
	must(err)
	seed, err := strconv.ParseInt(seedStr, 10, 64)
	must(err)

	deletePaths, appendPaths, rewritePath, err := readPlan(planIn)
	must(err)
	hashes, err := readHashes(hashesIn)
	must(err)

	for _, rel := range deletePaths {
		must(os.Remove(filepath.Join(srcDir, rel)))
		delete(hashes, rel)
	}

	for i, rel := range appendPaths {
		full := filepath.Join(srcDir, rel)
		must(appendRandomBytes(full, appendChunkBytes, seed*7+int64(i)))
		sum, err := hashFile(full)
		must(err)
		hashes[rel] = sum
	}

	rewriteFull := filepath.Join(srcDir, rewritePath)
	must(os.Remove(rewriteFull))
	sum, err := writeRandomFile(rewriteFull, rewriteBytes, seed*11)
	must(err)
	hashes[rewritePath] = sum

	renamed := rotateDirName + "-renamed"
	must(os.Rename(filepath.Join(srcDir, rotateDirName), filepath.Join(srcDir, renamed)))
	prefix := rotateDirName + string(filepath.Separator)
	renamedHashes := make(map[string]string, len(hashes))
	for rel, h := range hashes {
		if strings.HasPrefix(rel, prefix) {
			renamedHashes[filepath.Join(renamed, strings.TrimPrefix(rel, prefix))] = h
			continue
		}
		renamedHashes[rel] = h
	}
	hashes = renamedHashes

	added := sizeGroup("added", addedCount, add, seed, 9)
	for _, f := range added {
		rel := filepath.Join(addedDirName, f.relPath)
		full := filepath.Join(srcDir, rel)
		sum, err := writeRandomFile(full, f.size, f.seed)
		must(err)
		hashes[rel] = sum
	}

	must(writeHashes(hashesOut, hashes))
	fmt.Printf("ci-incremental-fixture: mutated %s: %d deleted, %d appended, 1 rewritten, directory %s renamed to %s, %d bytes added\n",
		srcDir, len(deletePaths), len(appendPaths), rotateDirName, renamed, add)
}

// runCheck hashes every regular file under dir and diffs the result
// against the manifest at hashesIn.
func runCheck(dir, hashesIn string) {
	want, err := readHashes(hashesIn)
	must(err)
	got, err := manifestOfDir(dir)
	must(err)

	mismatch := false
	for rel, wantSum := range want {
		gotSum, ok := got[rel]
		if !ok {
			fmt.Fprintf(os.Stderr, "ci-incremental-fixture check: missing: %s\n", rel)
			mismatch = true
			continue
		}
		if gotSum != wantSum {
			fmt.Fprintf(os.Stderr, "ci-incremental-fixture check: mismatch: %s\n", rel)
			mismatch = true
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			fmt.Fprintf(os.Stderr, "ci-incremental-fixture check: unexpected: %s\n", rel)
			mismatch = true
		}
	}
	if mismatch {
		fmt.Fprintln(os.Stderr, "ci-incremental-fixture check: manifest mismatch")
		os.Exit(1)
	}
	fmt.Printf("ci-incremental-fixture check: %s matches (%d files)\n", dir, len(want))
}

// buildPlan lays out large, medium, small and rotate-directory files
// that sum to exactly total bytes. The rotate group's files live under
// rotateDirName; every other file is spread round-robin across dCount
// subdirectories and the root. The plan is fully determined by
// (total, seed).
func buildPlan(total int64, seed int64) []fileSpec {
	r := rand.New(rand.NewSource(seed))

	rotateTotal := int64(float64(total) * rotateFraction)
	largeTotal := int64(float64(total-rotateTotal) * largeFraction)
	smallTotal := int64(float64(total-rotateTotal) * smallFraction)
	mediumTotal := total - rotateTotal - largeTotal - smallTotal

	var plan []fileSpec
	plan = append(plan, sizeGroupR(r, "large", largeCount, largeTotal, seed, 1)...)
	plan = append(plan, sizeGroupR(r, "medium", mediumCount, mediumTotal, seed, 2)...)
	plan = append(plan, sizeGroupR(r, "small", smallCount, smallTotal, seed, 3)...)

	rotate := sizeGroupR(r, "rotate", rotateCount, rotateTotal, seed, 4)
	for i := range rotate {
		rotate[i].relPath = filepath.Join(rotateDirName, rotate[i].relPath)
	}
	plan = append(plan, rotate...)

	fixRemainder(plan, total)
	assignDirs(plan)
	return plan
}

// sizeGroup is sizeGroupR with its own *rand.Rand, for a caller (mutate,
// adding new files) that has no existing generator to share.
func sizeGroup(kind string, n int, groupTotal int64, seed int64, salt int64) []fileSpec {
	return sizeGroupR(rand.New(rand.NewSource(seed^salt)), kind, n, groupTotal, seed, salt)
}

// sizeGroupR builds n files of prefix kind whose sizes are random
// weighted shares of groupTotal bytes, seeded so the shares and every
// file's own content seed are reproducible.
func sizeGroupR(r *rand.Rand, kind string, n int, groupTotal int64, baseSeed int64, salt int64) []fileSpec {
	if n == 0 || groupTotal <= 0 {
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

// fixRemainder adds any rounding remainder to the first medium file, so
// the plan sums to exactly total bytes.
func fixRemainder(plan []fileSpec, total int64) {
	var sum int64
	for _, f := range plan {
		sum += f.size
	}
	remainder := total - sum
	if remainder == 0 {
		return
	}
	for i := range plan {
		if strings.HasPrefix(plan[i].relPath, "medium") {
			plan[i].size += remainder
			return
		}
	}
	if len(plan) > 0 {
		plan[0].size += remainder
	}
}

// assignDirs spreads every non-rotate file across the root and dirCount
// subdirectories, round-robin by sorted path, leaving rotate's own
// files under rotateDirName untouched.
func assignDirs(plan []fileSpec) {
	sort.Slice(plan, func(i, j int) bool { return plan[i].relPath < plan[j].relPath })
	prefix := rotateDirName + string(filepath.Separator)
	slot := 0
	for i := range plan {
		if strings.HasPrefix(plan[i].relPath, prefix) {
			continue
		}
		s := slot % (dirCount + 1)
		slot++
		if s == 0 {
			continue
		}
		plan[i].relPath = filepath.Join(fmt.Sprintf("d%d", s-1), plan[i].relPath)
	}
}

// choosePlan picks, by a seeded shuffle over the plan's non-rotate
// files, disjoint sets for later deletion, appending and one rewrite.
func choosePlan(plan []fileSpec, seed int64) (deletePaths, appendPaths []string, rewritePath string) {
	prefix := rotateDirName + string(filepath.Separator)
	var eligible []string
	for _, f := range plan {
		if !strings.HasPrefix(f.relPath, prefix) {
			eligible = append(eligible, f.relPath)
		}
	}
	sort.Strings(eligible)

	r := rand.New(rand.NewSource(seed ^ 0x5a5a5a5a))
	idx := r.Perm(len(eligible))
	need := deleteCount + appendCount + 1
	if need > len(eligible) {
		panic("ci-incremental-fixture: not enough eligible files for the plan")
	}
	for _, i := range idx[:deleteCount] {
		deletePaths = append(deletePaths, eligible[i])
	}
	for _, i := range idx[deleteCount : deleteCount+appendCount] {
		appendPaths = append(appendPaths, eligible[i])
	}
	rewritePath = eligible[idx[deleteCount+appendCount]]
	sort.Strings(deletePaths)
	sort.Strings(appendPaths)
	return deletePaths, appendPaths, rewritePath
}

// writeRandomFile streams size deterministic pseudo-random bytes to
// path, one fixed-size buffer at a time, hashing as it writes so the
// caller never rereads the file to learn its content id.
func writeRandomFile(path string, size int64, seed int64) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	bw := bufio.NewWriterSize(f, 1<<20)
	sum := sha256.New()
	w := io.MultiWriter(bw, sum)
	r := rand.New(rand.NewSource(seed))
	buf := make([]byte, 1<<20)
	for remaining := size; remaining > 0; {
		chunk := min(remaining, int64(len(buf)))
		r.Read(buf[:chunk])
		if _, err := w.Write(buf[:chunk]); err != nil {
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
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// appendRandomBytes appends size deterministic pseudo-random bytes to
// the file at path, which must already exist.
func appendRandomBytes(path string, size int64, seed int64) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	bw := bufio.NewWriterSize(f, 1<<20)
	r := rand.New(rand.NewSource(seed))
	buf := make([]byte, 1<<20)
	for remaining := size; remaining > 0; {
		chunk := min(remaining, int64(len(buf)))
		r.Read(buf[:chunk])
		if _, err := bw.Write(buf[:chunk]); err != nil {
			return err
		}
		remaining -= chunk
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return f.Close()
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

// manifestOfDir walks dir and hashes every regular file it finds,
// keyed by its path relative to dir.
func manifestOfDir(dir string) (map[string]string, error) {
	out := make(map[string]string)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dir || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		out[rel] = sum
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// writePlan saves the paths mutate must delete, append to and rewrite,
// and the rotate directory's name, as plain tab-separated lines.
func writePlan(path string, deletePaths, appendPaths []string, rewritePath string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	if _, err := fmt.Fprintf(w, "rotate\t%s\n", rotateDirName); err != nil {
		return err
	}
	for _, p := range deletePaths {
		if _, err := fmt.Fprintf(w, "delete\t%s\n", p); err != nil {
			return err
		}
	}
	for _, p := range appendPaths {
		if _, err := fmt.Fprintf(w, "append\t%s\n", p); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "rewrite\t%s\n", rewritePath); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

// readPlan loads what writePlan saved.
func readPlan(path string) (deletePaths, appendPaths []string, rewritePath string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "delete":
			deletePaths = append(deletePaths, parts[1])
		case "append":
			appendPaths = append(appendPaths, parts[1])
		case "rewrite":
			rewritePath = parts[1]
		case "rotate":
			// rotateDirName is a package constant; the plan's own
			// record of it is informative only, kept for a human
			// reading the plan file.
		}
	}
	if rewritePath == "" {
		return nil, nil, "", fmt.Errorf("ci-incremental-fixture: plan %s: no rewrite path recorded", path)
	}
	return deletePaths, appendPaths, rewritePath, nil
}

// writeHashes saves a path-to-SHA-256 manifest, sorted by path, as
// plain tab-separated lines.
func writeHashes(path string, hashes map[string]string) error {
	rels := make([]string, 0, len(hashes))
	for rel := range hashes {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	for _, rel := range rels {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", rel, hashes[rel]); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

// readHashes loads what writeHashes saved.
func readHashes(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}

func must(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-incremental-fixture:", err)
		os.Exit(1)
	}
}
