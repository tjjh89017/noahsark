package image

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// buildSmallTree builds the small fixture into a new directory
// and returns the disc root and its run directory.
func buildSmallTree(t *testing.T) (root, runDir string) {
	t.Helper()
	stagingDir, snapID := stageFixture(t)
	root = t.TempDir()
	if _, err := Build(testOpts(t, stagingDir, snapID, root)); err != nil {
		t.Fatal(err)
	}
	runDir, err := NewestRunDir(filepath.Join(root, "NOAHSARK", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return root, runDir
}

// flipRunSeqByte flips one byte of run_seq in the run header copy at
// path, a byte that header_crc32c covers.
func flipRunSeqByte(t *testing.T, path string) {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	buf[64] ^= 0xff
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// damageNames returns the File field of each damage of rr.
func damageNames(rr *ReadResult) []string {
	var names []string
	for _, d := range rr.Damaged {
		names = append(names, d.File)
	}
	return names
}

// TestReadFallsBackToRun2 damages RUN.bin in two ways. A read that stops
// at the first damage must report it. A keep-going read must take the run
// header from RUN2.bin and list RUN.bin as damaged.
func TestReadFallsBackToRun2(t *testing.T) {
	cases := []struct {
		name   string
		damage func(t *testing.T, path string)
	}{
		{"a bad byte", flipRunSeqByte},
		{"a missing file", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, runDir := buildSmallTree(t)
			good, err := Read(root)
			if err != nil {
				t.Fatal(err)
			}
			tc.damage(t, filepath.Join(runDir, "RUN.bin"))

			if _, err := Read(root); err == nil || !strings.Contains(err.Error(), "RUN.bin") {
				t.Fatalf("Read: %v, want an error that names RUN.bin", err)
			}
			rr, err := ReadWithOptions(root, ReadOptions{KeepGoing: true})
			if err != nil {
				t.Fatalf("keep-going Read: %v, want the run header of RUN2.bin", err)
			}
			if got := damageNames(rr); len(got) != 1 || got[0] != "RUN.bin" {
				t.Fatalf("Damaged = %v, want RUN.bin only", got)
			}
			if rr.RunCopies != 1 {
				t.Fatalf("RunCopies = %d, want 1", rr.RunCopies)
			}
			if rr.Run != good.Run {
				t.Fatal("the run header of RUN2.bin differs from the run header of the good tree")
			}
			if rr.ObjectsVerified != good.ObjectsVerified {
				t.Fatalf("ObjectsVerified = %d, want %d", rr.ObjectsVerified, good.ObjectsVerified)
			}
		})
	}
}

// TestReadRefusesBothRunCopiesDamaged damages both run header copies. The
// read must stop, also with the keep-going option, and name both files.
func TestReadRefusesBothRunCopiesDamaged(t *testing.T) {
	root, runDir := buildSmallTree(t)
	flipRunSeqByte(t, filepath.Join(runDir, "RUN.bin"))
	flipRunSeqByte(t, filepath.Join(runDir, "RUN2.bin"))

	_, err := ReadWithOptions(root, ReadOptions{KeepGoing: true})
	if err == nil || !strings.Contains(err.Error(), "RUN.bin") || !strings.Contains(err.Error(), "RUN2.bin") {
		t.Fatalf("keep-going Read: %v, want an error that names both copies", err)
	}
}

// TestReadRefusesAnIndexOfAnotherRun writes an INDEX whose run_seq
// differs from the run header, with index_bytes and index_hash of both
// header copies set to match it.
func TestReadRefusesAnIndexOfAnotherRun(t *testing.T) {
	root, runDir := buildSmallTree(t)
	indexPath := filepath.Join(runDir, "INDEX.bin")
	buf, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var idx format.Index
	if _, err := idx.Decode(buf); err != nil {
		t.Fatal(err)
	}
	idx.RunSeq++
	out := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(out); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, out, 0o644); err != nil {
		t.Fatal(err)
	}

	runBuf, err := os.ReadFile(filepath.Join(runDir, "RUN.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var run format.Run
	if err := run.Decode(runBuf); err != nil {
		t.Fatal(err)
	}
	run.IndexBytes = uint64(len(out))
	run.IndexHash = sha256sum(out)
	if err := run.Encode(runBuf); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"RUN.bin", "RUN2.bin"} {
		if err := os.WriteFile(filepath.Join(runDir, name), runBuf, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err = ReadWithOptions(root, ReadOptions{KeepGoing: true})
	if err == nil || !strings.Contains(err.Error(), "run_seq") {
		t.Fatalf("keep-going Read: %v, want an error that names run_seq", err)
	}
}

// TestReadReadsARunWithAnUnknownFECScheme sets fec_scheme 1 in both run
// header copies. The read must take every object and say that it cannot
// use the scheme, naming the value.
func TestReadReadsARunWithAnUnknownFECScheme(t *testing.T) {
	root, runDir := buildSmallTree(t)
	for _, name := range []string{"RUN.bin", "RUN2.bin"} {
		path := filepath.Join(runDir, name)
		buf, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		buf[84] = 1
		binary.LittleEndian.PutUint32(buf[504:508], crc32.Checksum(buf[0:504], crc32.MakeTable(crc32.Castagnoli)))
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rr, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rr.ObjectsVerified != len(rr.Index.Objects) {
		t.Fatalf("Read verified %d of %d objects", rr.ObjectsVerified, len(rr.Index.Objects))
	}
	if len(rr.Notices) != 1 || !strings.Contains(rr.Notices[0], "fec_scheme 1") {
		t.Fatalf("notices %q: want one notice that names fec_scheme 1", rr.Notices)
	}
}

// TestReadRefusesASymlinkInTheTree puts a symlink in the disc tree. The
// target outside the tree holds the right bytes, thus only the refusal
// of the symlink can make the read fail.
func TestReadRefusesASymlinkInTheTree(t *testing.T) {
	root, _ := buildSmallTree(t)
	target := filepath.Join(t.TempDir(), "README.txt")
	readme := filepath.Join(root, "NOAHSARK", "README.txt")
	buf, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(readme); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, readme); err != nil {
		t.Fatal(err)
	}

	_, err = ReadWithOptions(root, ReadOptions{KeepGoing: true})
	if err == nil || !strings.Contains(err.Error(), "README.txt") {
		t.Fatalf("keep-going Read: %v, want a refusal that names README.txt", err)
	}
}

// TestReadAcceptsASymlinkRoot gives the reader a disc root through a
// symlink, as the operator can do. Only an entry inside the tree is
// refused.
func TestReadAcceptsASymlinkRoot(t *testing.T) {
	root, _ := buildSmallTree(t)
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); err != nil {
		t.Fatalf("Read through a symlink root: %v", err)
	}
}
