package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dryRunDiscRe matches one predicted disc line of pack --dry-run.
var dryRunDiscRe = regexp.MustCompile(`^disc (\d+) "([^"]*)": (\d+) objects, (\d+) bytes$`)

// packedDiscRe matches the first line of a real pack.
var packedDiscRe = regexp.MustCompile(`^packed disc (\d+) "([^"]*)": (\d+) objects, (\d+) bytes$`)

// TestPackDefaultLabelUsesTheNewestPendingRef checks that a pack with
// two pending refs labels the disc with the newest one, and that a
// later disc, which carries no pending ref at all, still carries a ref
// name instead of a bare disc number.
func TestPackDefaultLabelUsesTheNewestPendingRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	// Two commits in the same second: the tie must go to the later
	// date, not to the name that sorts first.
	writeSized(t, filepath.Join(src, "one.bin"), 400_000, 1)
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-14"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	writeSized(t, filepath.Join(src, "two.bin"), 400_000, 2)
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-21"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=6MB", "--out="+filepath.Join(work, "d0"))
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if !strings.Contains(out, `packed disc 0 "2026-09-21 disc 0"`) {
		t.Fatalf("pack output %q, want the newest pending ref in the label", out)
	}

	// A second disc: both refs are carried already, so nothing is
	// pending. The label still names the newest ref of the repository.
	writeSized(t, filepath.Join(src, "three.bin"), 400_000, 3)
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=2026-09-21"); code != 0 {
		t.Fatalf("commit again: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=6MB", "--out="+filepath.Join(work, "d1")); code != 0 {
		t.Fatalf("pack again: exit %d: %s", code, out)
	} else if !strings.Contains(out, `packed disc 1 "2026-09-21 disc 1"`) {
		t.Fatalf("pack output %q, want a later disc to keep a ref name in its label", out)
	}

	if code, out := runCmd(t, "pack", "-h"); code != 0 && !strings.Contains(out, "newest ref name and the disc number") {
		t.Fatalf("pack -h output %q, want the help to name the same rule", out)
	}
}

// TestPackDryRunPredictsTheRealPacks checks that pack --dry-run and a
// loop of real packs agree exactly: the same disc numbers, labels,
// object counts and byte counts, at a capacity that needs several
// discs.
func TestPackDryRunPredictsTheRealPacks(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := filepath.Join(work, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		writeSized(t, filepath.Join(src, fmt.Sprintf("f%d.bin", i)), 900_000, byte(i+1))
	}
	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, "--ref=r1"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=7MB", "--dry-run")
	if code != 0 {
		t.Fatalf("pack --dry-run: exit %d: %s", code, out)
	}
	var predicted []string
	for line := range strings.SplitSeq(out, "\n") {
		if dryRunDiscRe.MatchString(line) {
			predicted = append(predicted, line)
		}
	}
	if len(predicted) < 2 {
		t.Fatalf("pack --dry-run predicted %d disc(s), want at least 2: %s", len(predicted), out)
	}

	for i := range predicted {
		code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=7MB", "--out="+filepath.Join(work, fmt.Sprintf("tree%d", i)))
		if code != 0 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		m := packedDiscRe.FindStringSubmatch(strings.SplitN(out, "\n", 2)[0])
		if m == nil {
			t.Fatalf("pack %d: first line of %q is not a packed-disc line", i, out)
		}
		got := fmt.Sprintf("disc %s %q: %s objects, %s bytes", m[1], m[2], m[3], m[4])
		if got != predicted[i] {
			t.Fatalf("real pack wrote %q, pack --dry-run predicted %q", got, predicted[i])
		}
	}
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=7MB", "--out="+filepath.Join(work, "extra")); code == 0 {
		t.Fatalf("pack after the predicted discs: exit 0, want a refusal: %s", out)
	}
}

// TestBadPackCapacityStopsPackOnly checks that a pack.capacity value
// the config cannot parse stops pack and pack --dry-run, and stops no
// other command.
func TestBadPackCapacityStopsPackOnly(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	appendConfig(t, repo, "pack.capacity = 7500000\n")

	for _, args := range [][]string{
		{"pack", "--repo=" + repo},
		{"pack", "--repo=" + repo, "--dry-run"},
	} {
		code, out := runCmd(t, args...)
		if code != 2 || !strings.Contains(out, "pack.capacity") {
			t.Fatalf("%v: exit %d: %s, want exit 2 naming pack.capacity", args, code, out)
		}
	}
	for _, args := range [][]string{
		{"status", "--repo=" + repo},
		{"gc", "--repo=" + repo, "--dry-run"},
	} {
		if code, out := runCmd(t, args...); code != 0 {
			t.Fatalf("%v: exit %d: %s, want a command that never reads pack.capacity to run", args, code, out)
		}
	}
}

// TestUnknownConfigKeyStopsEveryCommand checks that the loader still
// refuses a key it does not know, whichever command reads the config.
func TestUnknownConfigKeyStopsEveryCommand(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfig(t, repo, "nonsense.key = 1\n")
	if code, out := runCmd(t, "status", "--repo="+repo); code != 2 || !strings.Contains(out, "unknown key") {
		t.Fatalf("status: exit %d: %s, want exit 2 for an unknown key", code, out)
	}
}

// TestCommitPrintsExcludedOnlyWhenSomethingWasExcluded checks that the
// excluded line is absent on a commit that excluded nothing.
func TestCommitPrintsExcludedOnlyWhenSomethingWasExcluded(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if strings.Contains(out, "excluded:") {
		t.Fatalf("commit output %q prints an excluded line with nothing excluded", out)
	}

	code, out = runCmd(t, "commit", "--repo="+repo, "--exclude=*.txt", src)
	if code != 0 {
		t.Fatalf("commit with an exclude: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "excluded:") {
		t.Fatalf("commit output %q, want the excluded line when a path was excluded", out)
	}
}

// writeSized writes a file of n bytes whose content depends on seed, so
// two files of the same size never dedup against each other.
func writeSized(t *testing.T, path string, n int, seed byte) {
	t.Helper()
	buf := make([]byte, n)
	x := uint32(seed)*2654435761 + 1
	for i := range buf {
		x = x*1664525 + 1013904223
		buf[i] = byte(x >> 24)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendConfig adds text to a repository's config file.
func appendConfig(t *testing.T, repo, text string) {
	t.Helper()
	f, err := os.OpenFile(configPath(repo), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyHealReportsBlocks checks that verify --heal reports in the
// operator's words: repaired blocks, with no talk of stripes.
func TestVerifyHealReportsBlocks(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo, "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	tree := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--fec", "--out="+tree); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "verify", "--repo="+repo, "--heal", tree)
	if code != 0 {
		t.Fatalf("verify --heal: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "heal: repaired 0 block(s)") {
		t.Fatalf("verify --heal output %q, want the repaired-blocks line", out)
	}
	if strings.Contains(out, "stripe") {
		t.Fatalf("verify --heal output %q still uses the word stripe", out)
	}
}
