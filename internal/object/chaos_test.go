package object

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chaosDirCount and chaosFileCount size the fixture TestChaosDuringCommit
// walks while a mutation goroutine runs against it.
const (
	chaosDirCount  = 8
	chaosFileCount = 240
	// chaosMaxFileSize caps how large the append case may grow one file,
	// so a tight chaos loop across many Commit calls cannot fill the disk.
	chaosMaxFileSize = 64 << 10
	// chaosMaxNewFiles bounds how many distinct paths the "create a new
	// file" case ever creates: it cycles through a fixed set of names
	// instead of minting one per loop iteration, so a tight loop over
	// many seconds cannot grow the fixture without bound.
	chaosMaxNewFiles = 200
)

// buildChaosFixture writes chaosDirCount subdirectories, each holding a
// share of chaosFileCount regular files, and returns every file path.
func buildChaosFixture(t *testing.T, src string) []string {
	t.Helper()
	var paths []string
	for d := 0; d < chaosDirCount; d++ {
		mustMkdir(t, filepath.Join(src, fmt.Sprintf("d%d", d)))
	}
	for i := 0; i < chaosFileCount; i++ {
		dir := filepath.Join(src, fmt.Sprintf("d%d", i%chaosDirCount))
		p := filepath.Join(dir, fmt.Sprintf("f%04d.dat", i))
		mustWriteBytes(t, p, []byte(strings.Repeat("x", 200+i%64)))
		paths = append(paths, p)
	}
	return paths
}

// runChaos performs one random filesystem mutation against src, using
// rng and the fixture's known directories and paths. It never fails the
// test: every operation is best-effort, since its target may already
// have been changed or removed by an earlier chaos step or by a commit
// in progress reading it.
func runChaos(rng *rand.Rand, src string, dirs, paths []string) {
	pick := func() string { return paths[rng.Intn(len(paths))] }

	switch rng.Intn(8) {
	case 0: // append to a file, capped so a tight loop cannot fill the disk
		p := pick()
		if info, err := os.Stat(p); err == nil && info.Size() < chaosMaxFileSize {
			f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o644)
			if err == nil {
				_, _ = f.Write([]byte("appended"))
				_ = f.Close()
			}
		}
	case 1: // truncate a file
		_ = os.Truncate(pick(), int64(rng.Intn(64)))
	case 2: // overwrite bytes in place
		f, err := os.OpenFile(pick(), os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteAt([]byte("overwritten"), int64(rng.Intn(64)))
			_ = f.Close()
		}
	case 3: // delete a file
		_ = os.Remove(pick())
	case 4: // rename a file
		p := pick()
		_ = os.Rename(p, p+".ren")
	case 5: // create a new file, cycling a fixed name set so the fixture
		// cannot grow without bound over a long tight loop
		idx := rng.Intn(chaosMaxNewFiles)
		dir := dirs[idx%len(dirs)]
		p := filepath.Join(dir, fmt.Sprintf("new%03d.dat", idx))
		_ = os.WriteFile(p, []byte("new file content"), 0o644)
	case 6: // create and remove a directory
		dir := filepath.Join(src, fmt.Sprintf("tmp%d", rng.Int()))
		if os.Mkdir(dir, 0o755) == nil {
			_ = os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644)
			_ = os.RemoveAll(dir)
		}
	case 7: // replace a file with a symlink
		p := pick()
		if os.Remove(p) == nil {
			_ = os.Symlink(pick(), p)
		}
	}
}

// TestChaosDuringCommit is the goal-of-record robustness test: Commit
// must never crash the process, no matter what happens to the source
// tree underneath it. A goroutine runs random mutations against a
// several-hundred-file fixture, with a fixed seed, while Commit runs
// repeatedly against it. Every iteration checks that Commit returns
// exactly one of a snapshot or an error, that no writeObjectFile temp
// file is left behind, and that every object present still decodes and
// still hashes to its own file name. Run with -race.
func TestChaosDuringCommit(t *testing.T) {
	src := t.TempDir()
	paths := buildChaosFixture(t, src)
	var dirs []string
	for d := 0; d < chaosDirCount; d++ {
		dirs = append(dirs, filepath.Join(src, fmt.Sprintf("d%d", d)))
	}

	staging := t.TempDir()
	w := NewWriter(staging)
	w.Now = fixedClock

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		rng := rand.New(rand.NewSource(20260913))
		for {
			select {
			case <-stop:
				return
			default:
			}
			runChaos(rng, src, dirs, paths)
		}
	}()

	const iterations = 6
	for i := 0; i < iterations; i++ {
		snapID, _, err := w.Commit(src)
		gotSnapshot := snapID != (ID{})
		if err == nil && !gotSnapshot {
			t.Fatalf("iteration %d: Commit returned neither a snapshot nor an error", i)
		}
		if err != nil && gotSnapshot {
			t.Fatalf("iteration %d: Commit returned both a snapshot and an error: %v", i, err)
		}
		if err != nil {
			t.Logf("iteration %d: Commit returned an error, acceptable under chaos: %v", i, err)
		}

		checkNoTempFiles(t, staging)
		verifyAllObjectsValid(t, staging)
	}

	close(stop)
	<-done
	checkNoTempFiles(t, staging)
}
