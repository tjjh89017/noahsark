package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// restoreArgs is a restore of the snapshot of the fixture commit into a
// new directory beside the copy of the disc root.
func restoreArgs(dest string, extra ...string) []string {
	args := append([]string{"restore"}, extra...)
	return append(args, defaultRefName(), "{ROOT}-"+dest)
}

func init() {
	// Row 85: the plan names a lost disc, and restore does not ask for it.
	registerStateCases(stateCase{
		row: "85", name: "the plan names a lost disc",
		start: stage.DiscLost, args: restoreArgs("r85", "--disc={ROOT}"),
		exit:   1,
		stdout: []string{`{DISC} ({UUID}): `, ` bytes (lost)`, "totals: 1 discs, ", "restored snapshot "},
		stderr: []string{"noahsark: restore: warning: ", "on a lost disc"},
		absent: []string{": found", "insert disc"},
		end:    stage.DiscLost,
	})
	// Row 86: the disc at --disc is the expected disc.
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified, stage.DiscOnDiscOnly} {
		registerStateCases(stateCase{
			row: "86", name: "restore from a " + s.String() + " disc",
			start: s, args: restoreArgs("r86", "--disc={ROOT}"),
			stdout: []string{`{DISC} ({UUID}): `, "totals: 1 discs, ", "{DISC}: found", "restored snapshot "},
			absent: []string{"warning:", "insert disc", "(lost)"},
			end:    s,
		})
	}
	registerStateCases(
		// Row 87: no terminal, and no disc at --disc.
		stateCase{
			row: "87", name: "no terminal and not the expected disc",
			start: stage.DiscVerified, args: restoreArgs("r87", "--disc={ROOT}-none"),
			exit:   1,
			stderr: []string{`restore: insert {DISC} ({UUID}) into {ROOT}-none and run restore again`},
			absent: []string{"restored snapshot", "press Enter"},
			end:    stage.DiscVerified,
		},
		// Row 88: the dry run prints the plan and changes nothing.
		stateCase{
			row: "88", name: "dry run with a lost disc",
			start: stage.DiscLost, args: restoreArgs("r88", "--dry-run", "--disc={ROOT}"),
			stdout: []string{`{DISC} ({UUID}): `, ` bytes (lost)`, "totals: 1 discs, "},
			absent: []string{"restored snapshot", "found"},
			end:    stage.DiscLost,
		},
		// Row 89a: the snapshot is partial. recover of the second disc
		// alone leaves the trees of the first disc out of the catalog.
		stateCase{
			row: "89a", name: "restore of a partial snapshot",
			start: stage.DiscMissing, args: restoreArgs("r89a", "--disc={ROOT}"),
			exit:   1,
			stderr: []string{" is partial; run recover with more discs"},
			absent: []string{"totals:", "restored snapshot"},
			end:    stage.DiscMissing,
		},
	)
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscVerified, stage.DiscOnDiscOnly} {
		registerStateCases(stateCase{
			row: "88", name: "dry run with a " + s.String() + " disc",
			start: s, args: restoreArgs("r88", "--dry-run", "--disc={ROOT}"),
			stdout: []string{`{DISC} ({UUID}): `, " items, ", "totals: 1 discs, "},
			absent: []string{"restored snapshot", "(lost)", "found"},
			end:    s,
		})
	}
}

// TestRestoreRow86ChangesNoFile checks that a restore and a dry run
// change no file of the repository, and that the restored tree matches
// the source.
func TestRestoreRow86ChangesNoFile(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	before := treeDigest(t, fx.repo)
	dest := filepath.Join(t.TempDir(), "out")
	fx.mustRun(t, "restore", "--dry-run", "--disc="+fx.root, defaultRefName(), dest)
	out := fx.mustRun(t, "restore", "--disc="+fx.root, defaultRefName(), dest)
	if !regexp.MustCompile(`(?m)^totals: 1 discs, [1-9]\d* items, [1-9]\d* bytes$`).MatchString(out) {
		t.Fatalf("output %q has no totals line with the fixed plural form", out)
	}
	compareTrees(t, dest, fx.src)
	if after := treeDigest(t, fx.repo); after != before {
		t.Fatal("restore changed a file of the repository")
	}
}

// TestRestoreRow85aNoKnownDisc removes the catalog INDEX of the one
// disc. The plan names the items with no known disc, restore reads no
// disc and still walks the snapshot, names each file it cannot restore,
// and exits 1.
func TestRestoreRow85aNoKnownDisc(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	if err := os.RemoveAll(filepath.Join(repoCatalogDir(t, fx.repo), "discs", fx.uuid)); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	code, out := fx.run(t, "restore", "--disc="+fx.root, defaultRefName(), dest)
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out)
	}
	wantLines(t, out,
		"restore: 2 item(s) have no disc known to the catalog; run recover with more discs\n",
		"totals: 0 discs, 0 items, 0 bytes\n",
		"restored snapshot ")
	for _, name := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if !strings.Contains(out, "noahsark: restore: warning: "+filepath.Join(dest, name)+": ") {
			t.Errorf("output %q does not name %s as not restored", out, name)
		}
	}
	if strings.Contains(out, "found") || strings.Contains(out, nextStatusLine) {
		t.Errorf("output %q reads a disc or holds the next line", out)
	}
	assertRestoredDirectory(t, filepath.Join(dest, "sub"))
}

// TestRestoreRow89NoRepository checks that restore with no repository
// is a usage error that names recover.
func TestRestoreRow89NoRepository(t *testing.T) {
	dir := t.TempDir()
	code, out := runIn(t, dir, "restore", "--disc="+dir, "x", filepath.Join(dir, "out"))
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, out)
	}
	if out != "noahsark: restore: no repository; run recover first, one time for each disc\n" {
		t.Fatalf("output %q", out)
	}
	if _, err := os.Lstat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
		t.Fatalf("restore with no repository created DEST: %v", err)
	}
}
