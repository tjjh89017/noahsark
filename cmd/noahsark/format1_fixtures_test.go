package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// format1Dir holds the frozen disc-root fixtures of format major 1.
const format1Dir = "testdata/format1"

// format1Fixtures names each fixture, its disc roots in disc order, and
// the refs that each has an expected-REF.txt for.
var format1Fixtures = []struct {
	name  string
	discs []string
	refs  []string
}{
	{"one-file", []string{"disc"}, []string{"ONE"}},
	{"two-files", []string{"disc"}, []string{"TWO"}},
	{"two-discs", []string{"disc0", "disc1"}, []string{"first", "second"}},
}

// verifyOKUUIDRe takes the disc uuid from the ok line of a verify with
// no repository.
var verifyOKUUIDRe = regexp.MustCompile(`(?m)^disc ([0-9a-f-]{36}) ".*": \d+ items, ok$`)

// insertUUIDRe takes the disc uuid from the stop line of a restore that
// needs another disc.
var insertUUIDRe = regexp.MustCompile(`(?m)^restore: insert disc .*\(([0-9a-f-]{36})\) into .* and run restore again$`)

// TestFormat1Fixtures reads each frozen format-1 fixture as the discs of
// a lost repository: verify with no repository, recover of each disc
// into a new repository, a counted verify, and a restore of each ref.
// The restored tree must equal the expected-REF.txt of the fixture.
func TestFormat1Fixtures(t *testing.T) {
	for _, fx := range format1Fixtures {
		t.Run(fx.name, func(t *testing.T) {
			work := t.TempDir()
			roots := map[string]string{}
			var order []string
			for _, d := range fx.discs {
				root := filepath.Join(work, d)
				copyTree(t, filepath.Join(format1Dir, fx.name, d), root)
				code, out := runCmd(t, "verify", root)
				m := verifyOKUUIDRe.FindStringSubmatch(out)
				if code != 0 || m == nil {
					t.Fatalf("verify %s with no repository: exit %d, want 0 and an ok line: %s", d, code, out)
				}
				roots[m[1]] = root
				order = append(order, root)
			}

			repo := filepath.Join(work, "repo")
			for _, root := range order {
				if code, out := recoverDisc(t, repo, filepath.Join(work, "src"), root); code != 0 {
					t.Fatalf("recover %s: exit %d: %s", root, code, out)
				}
			}
			for _, root := range order {
				if code, out := runCmd(t, "--repo="+repo, "verify", root); code != 0 || !strings.Contains(out, "items, ok\n") {
					t.Fatalf("counted verify %s: exit %d, want 0 and an ok line: %s", root, code, out)
				}
			}

			for _, ref := range fx.refs {
				dest := filepath.Join(work, "restored-"+ref)
				format1Restore(t, repo, ref, dest, roots, order[0])
				want, err := os.ReadFile(filepath.Join(format1Dir, fx.name, "expected-"+ref+".txt"))
				if err != nil {
					t.Fatal(err)
				}
				if got := expectedLines(t, dest); got != string(want) {
					t.Fatalf("restore of %s gives:\n%s\nwant:\n%s", ref, got, want)
				}
			}
		})
	}
}

// format1Restore restores ref into dest, with the disc at a mount point
// that first holds first. When restore stops and asks for a disc, it
// puts the disc root of that uuid at the mount point and runs restore
// again, until restore exits 0.
func format1Restore(t *testing.T, repo, ref, dest string, roots map[string]string, first string) {
	t.Helper()
	mountDir := filepath.Join(t.TempDir(), "mnt")
	mountDisc(t, mountDir, first)
	for range len(roots) + 1 {
		code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+mountDir, ref, dest)
		if code == 0 {
			return
		}
		m := insertUUIDRe.FindStringSubmatch(out)
		if m == nil || roots[m[1]] == "" {
			t.Fatalf("restore %s: exit %d, and it asks for no known disc: %s", ref, code, out)
		}
		mountDisc(t, mountDir, roots[m[1]])
	}
	t.Fatalf("restore %s asked for a disc more than %d times", ref, len(roots))
}

// expectedLines lists the tree at dir in the form of expected-REF.txt:
// one line for each entry below dir, sorted by bytes.
func expectedLines(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("%s\tl\t%s", rel, target))
		case fi.IsDir():
			lines = append(lines, fmt.Sprintf("%s\td\t%o\t%d", rel, fi.Mode().Perm(), fi.ModTime().Unix()))
		default:
			sum, err := fileSHA256(path)
			if err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("%s\tf\t%o\t%d\t%d\t%s", rel, fi.Mode().Perm(), fi.ModTime().Unix(), fi.Size(), sum))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TestFormat1FixturesUnchanged fails when a file of the frozen format-1
// fixtures changes, goes away, or is added. SHA256SUMS lists each file.
// This list never changes after the first tag: a later tool must read
// the discs that the first tag wrote.
func TestFormat1FixturesUnchanged(t *testing.T) {
	f, err := os.Open(filepath.Join(format1Dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	listed := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, path, ok := strings.Cut(sc.Text(), "  ")
		if !ok || len(sum) != 64 {
			t.Fatalf("SHA256SUMS: bad line %q", sc.Text())
		}
		listed[path] = sum
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(listed) == 0 {
		t.Fatal("SHA256SUMS lists no file")
	}

	seen := map[string]bool{}
	err = filepath.WalkDir(format1Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(format1Dir, path)
		if err != nil {
			return err
		}
		if rel == "SHA256SUMS" || rel == "make.sh" {
			return nil
		}
		seen[rel] = true
		want, ok := listed[rel]
		if !ok {
			t.Errorf("%s: SHA256SUMS does not list it", rel)
			return nil
		}
		if got, err := fileSHA256(path); err != nil || got != want {
			t.Errorf("%s: SHA-256 %s, SHA256SUMS lists %s; a frozen fixture changed", rel, got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path := range listed {
		if !seen[path] {
			t.Errorf("%s: SHA256SUMS lists it, and it does not exist", path)
		}
	}
}
