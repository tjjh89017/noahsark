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
	registerStateCases(
		// Row 85a: the catalog INDEX of the one disc is removed. The plan
		// names the items with no known disc. restore reads no disc, still
		// walks the snapshot, names each file that it cannot restore, and
		// exits 1.
		stateCase{
			row: "85a", name: "no disc known to the catalog",
			start: stage.DiscVerified,
			setup: func(t *testing.T, fx *discFixture) {
				if err := os.RemoveAll(filepath.Join(repoCatalogDir(t, fx.repo), "discs", fx.uuid)); err != nil {
					t.Fatal(err)
				}
			},
			args: restoreArgs("r85a", "--disc={ROOT}"),
			exit: 1,
			stdout: []string{
				"restore: 2 item(s) have no disc known to the catalog; run recover with more discs\n",
				"totals: 0 discs, 0 items, 0 bytes\n",
				"restored snapshot ",
			},
			absent: []string{"found"},
			end:    stage.DiscVerified, word: stage.WordClean,
			check: func(t *testing.T, fx *discFixture, stdout, stderr string) {
				dest := fx.root + "-r85a"
				for _, name := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
					if !strings.Contains(stderr, "noahsark: restore: warning: "+filepath.Join(dest, name)+": file not restored: ") {
						t.Errorf("stderr %q does not name %s as not restored", stderr, name)
					}
				}
				assertRestoredDirectory(t, filepath.Join(dest, "sub"))
			},
		},
		// Row 89: restore with no repository is a usage error that names
		// recover, and creates no DEST.
		stateCase{
			row: "89", name: "restore with no repository",
			start: stage.DiscVerified, noRepo: true,
			args:   restoreArgs("r89", "--disc={ROOT}"),
			exit:   2,
			stdout: nil, exact: true,
			end: stage.DiscVerified, word: stage.WordClean,
			check: allChecks(
				stderrIs("noahsark: restore: no repository; run recover first, one time for each disc\n"),
				func(t *testing.T, fx *discFixture, _, _ string) {
					if _, err := os.Lstat(fx.root + "-r89"); !os.IsNotExist(err) {
						t.Fatalf("restore with no repository created DEST: %v", err)
					}
				},
			),
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
