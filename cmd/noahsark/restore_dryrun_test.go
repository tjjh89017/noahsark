package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
)

// runRestoreDryRun runs "restore --dry-run" against repo for snapID and
// paths, with a throwaway --disc and DEST, and returns its exit code and
// output.
func runRestoreDryRun(t *testing.T, repo, snapID string, paths ...string) (int, string) {
	t.Helper()
	args := []string{"--repo=" + repo, "restore", "--dry-run", "--disc=" + t.TempDir(), snapID}
	args = append(args, paths...)
	args = append(args, filepath.Join(t.TempDir(), "out"))
	return runCmd(t, args...)
}

// totalsRe matches the totals line of a plan.
var totalsRe = regexp.MustCompile(`(?m)^totals: (\d+) discs, (\d+) items, (\d+) bytes$`)

// planTotals returns the numbers of the totals line of a plan. It fails
// when the totals line is not the last line of out.
func planTotals(t *testing.T, out string) (discs, items, bytes int) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	m := totalsRe.FindStringSubmatch(lines[len(lines)-1])
	if m == nil {
		t.Fatalf("the last line of %q is not the totals line", out)
	}
	_, _ = fmt.Sscanf(m[1]+" "+m[2]+" "+m[3], "%d %d %d", &discs, &items, &bytes)
	return discs, items, bytes
}

// TestRestoreDryRunSingleDisc is row 88 for one disc: the plan names the
// disc and the totals line, with the fixed plural form, and writes
// nothing.
func TestRestoreDryRunSingleDisc(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)
	emptyStagingChunks(t, repo)
	before := treeDigest(t, repo)

	dest := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "--repo="+repo, "restore", "--dry-run", "--disc="+t.TempDir(), snapID, dest)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if n := len(planLineRe.FindAllString(out, -1)); n != 1 {
		t.Fatalf("restore --dry-run output = %q, want one disc line, got %d", out, n)
	}
	if discs, items, bytes := planTotals(t, out); discs != 1 || items == 0 || bytes == 0 {
		t.Fatalf("totals %d discs, %d items, %d bytes, want one disc with items", discs, items, bytes)
	}
	if strings.Contains(out, "no disc known") || strings.Contains(out, "next: ") || strings.Contains(out, "objects") {
		t.Fatalf("restore --dry-run output = %q", out)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("restore --dry-run created DEST: %v", err)
	}
	if after := treeDigest(t, repo); after != before {
		t.Fatal("restore --dry-run changed the repository")
	}
}

// TestRestoreDryRunPathNarrows checks that a PATH plans fewer items than
// the whole snapshot, and that two paths both resolve.
func TestRestoreDryRunPathNarrows(t *testing.T) {
	repo, snapID, _, _ := discSwapFixture(t)

	code, fullOut := runRestoreDryRun(t, repo, snapID)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, fullOut)
	}
	if discs, _, _ := planTotals(t, fullOut); discs != 2 {
		t.Fatalf("restore --dry-run = %q, want two discs", fullOut)
	}
	_, fullItems, _ := planTotals(t, fullOut)

	code, narrowOut := runRestoreDryRun(t, repo, snapID, "sub0")
	if code != 0 {
		t.Fatalf("restore --dry-run sub0: exit %d: %s", code, narrowOut)
	}
	_, narrowItems, _ := planTotals(t, narrowOut)
	if narrowItems == 0 || narrowItems >= fullItems {
		t.Fatalf("sub0 plans %d items, want more than 0 and less than %d", narrowItems, fullItems)
	}

	code, twoOut := runRestoreDryRun(t, repo, snapID, "sub0/f.bin", "sub1/")
	if code != 0 {
		t.Fatalf("restore --dry-run with two paths: exit %d: %s", code, twoOut)
	}
}

// TestRestoreDryRunPathNotHeld checks that a PATH the snapshot does not
// hold is a usage error.
func TestRestoreDryRunPathNotHeld(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)
	for _, bad := range []string{"no-such", "a.txt/", "sub/no-such"} {
		code, out := runRestoreDryRun(t, repo, snapID, bad)
		if code != 2 {
			t.Fatalf("restore --dry-run %s: exit %d, want 2: %s", bad, code, out)
		}
		if !strings.Contains(out, "no entry of the snapshot matches "+bad) {
			t.Fatalf("restore --dry-run %s: output %q does not name the path", bad, out)
		}
	}
}

// TestRestoreDryRunNoKnownDisc is row 85a: the catalog lacks the INDEX
// of one disc, so its items have no known disc. The plan names them,
// the totals leave them out, and --dry-run still exits 0.
func TestRestoreDryRunNoKnownDisc(t *testing.T) {
	repo, snapID, _, _ := discSwapFixture(t)
	if err := os.RemoveAll(filepath.Join(repoCatalogDir(t, repo), "discs", catalogDiscUUID(t, repo, 0))); err != nil {
		t.Fatal(err)
	}

	code, out := runRestoreDryRun(t, repo, snapID)
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d, want 0: %s", code, out)
	}
	if !regexp.MustCompile(`(?m)^restore: \d+ item\(s\) have no disc known to the catalog; run recover with more discs$`).MatchString(out) {
		t.Fatalf("restore --dry-run output %q does not report the items with no known disc", out)
	}
	if discs, _, _ := planTotals(t, out); discs != 1 {
		t.Fatalf("restore --dry-run output %q, want one disc in the totals", out)
	}
}

// catalogDiscUUID returns the text form of the uuid of the disc with
// disc_seq seq.
func catalogDiscUUID(t *testing.T, repo string, seq uint64) string {
	t.Helper()
	c, err := catalog.OpenReadOnly(repo)
	if err != nil {
		t.Fatal(err)
	}
	discs, err := c.Discs()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range discs.Rows {
		if row.DiscSeq == seq {
			return uuidText(row.DiscUUID)
		}
	}
	t.Fatalf("no DISCS row in the catalog has disc_seq %d", seq)
	return ""
}

// TestRestoreDryRunNothingPacked plans a snapshot that no disc holds yet,
// with an empty staging store: every item has no known disc, and the
// totals name no disc.
func TestRestoreDryRunNothingPacked(t *testing.T) {
	repo, _ := initAndCommit(t)
	emptyStagingChunks(t, repo)
	code, out := runRestoreDryRun(t, repo, defaultRefName())
	if code != 0 {
		t.Fatalf("restore --dry-run: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "have no disc known to the catalog") {
		t.Fatalf("restore --dry-run output %q does not report the items with no known disc", out)
	}
	if discs, items, bytes := planTotals(t, out); discs != 0 || items != 0 || bytes != 0 {
		t.Fatalf("totals %d discs, %d items, %d bytes, want 0", discs, items, bytes)
	}
}
