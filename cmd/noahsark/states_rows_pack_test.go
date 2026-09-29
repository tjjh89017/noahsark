package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// addFileSetup adds a file to the source of fx and commits it. The new
// items are staged.
func addFileSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.src, "second.txt"), []byte("content of the second disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "commit", fx.src)
}

// outDirSetup sets {OUT} to a path for pack --out. With holdsFile, it is
// a directory with one file. Else it is a file.
func outDirSetup(holdsFile bool) func(*testing.T, *discFixture) {
	return func(t *testing.T, fx *discFixture) {
		t.Helper()
		out := filepath.Join(t.TempDir(), "out")
		file := out
		if holdsFile {
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			file = filepath.Join(out, "x")
		}
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		fx.set("{OUT}", out)
		fx.cell("DIR", out)
	}
}

// nextDiscCells gives the cells the number and the label of the disc
// that the next pack writes: disc 1 with the ref of today. Its uuid is
// not known before the pack.
func nextDiscCells(_ *testing.T, fx *discFixture) {
	fx.cell("SEQ", "1")
	fx.cell("LABEL", defaultRefName()+" disc 1")
	fx.cell("UUID", "")
}

// newDiscUUID is the uuid of the disc that the uuid line of pack names.
func newDiscUUID(t *testing.T, _ *discFixture, stdout string) string {
	t.Helper()
	return packedDiscUUID(t, stdout)
}

// newDiscIsPacked is a check: the uuid line of pack names a new disc,
// and the new disc is packed.
func newDiscIsPacked(t *testing.T, fx *discFixture, stdout, _ string) {
	t.Helper()
	u := packedDiscUUID(t, stdout)
	if u == fx.uuid {
		t.Fatalf("pack names the disc of the fixture %s, want a new disc", u)
	}
	if got := discState(t, fx.repo, u).State; got != stage.DiscPacked {
		t.Errorf("new disc %s, want packed", got)
	}
}

// onlyTheFixtureDisc is a check: the disc state log knows only the disc of the
// fixture.
func onlyTheFixtureDisc(t *testing.T, fx *discFixture, _, _ string) {
	t.Helper()
	if n := len(readDiscLog(t, fx.repo).Discs()); n != 1 {
		t.Errorf("the disc state log knows %d disc(s), want 1", n)
	}
}

func init() {
	// The fixture of an undone disc has every item staged and no disc in
	// the repository. Its number 0 is not used again, thus the next disc
	// is disc 1.
	registerStateCases(
		stateCase{
			row: "5", name: "pack of staged items",
			start: stage.DiscUndone, setup: nextDiscCells,
			args:    []string{"pack", "--capacity=64MiB"},
			exact:   true,
			subject: newDiscUUID,
			end:     stage.DiscUndone, check: newDiscIsPacked,
		},
		stateCase{
			row: "6", name: "pack with no --capacity",
			start: stage.DiscUndone, args: []string{"pack"},
			noEvent: true,
			end:     stage.DiscUndone,
		},
		stateCase{
			row: "7", name: "pack with a capacity that holds not one item",
			start: stage.DiscUndone, args: []string{"pack", "--capacity=50KiB"},
			noEvent: true,
			absent:  []string{"internal error"},
			end:     stage.DiscUndone,
		},
		stateCase{
			row: "8", name: "pack with nothing staged",
			start: stage.DiscPacked, args: []string{"pack", "--capacity=64MiB"},
			exact: true, noEvent: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "9", name: "pack while another disc is packed",
			start: stage.DiscPacked,
			setup: func(t *testing.T, fx *discFixture) {
				addFileSetup(t, fx)
				nextDiscCells(t, fx)
			},
			args:  []string{"pack", "--capacity=64MiB"},
			exact: true,
			end:   stage.DiscPacked, word: stage.WordPacked, check: newDiscIsPacked,
		},
		stateCase{
			row: "10a", name: "dry run with nothing staged",
			start: stage.DiscPacked, args: []string{"pack", "--capacity=64MiB", "--dry-run"},
			exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "10b", name: "dry run of staged items",
			start: stage.DiscUndone, args: []string{"pack", "--capacity=64MiB", "--dry-run"},
			cells:   map[string]string{"N": "1", "D": "1"},
			exact:   true,
			noEvent: true, sameCatalog: true,
			end: stage.DiscUndone, check: onlyTheFixtureDisc,
		},
		stateCase{
			row: "10c", name: "pack --out of a directory that holds files",
			start: stage.DiscUndone, setup: outDirSetup(true),
			args:    []string{"pack", "--capacity=64MiB", "--out={OUT}"},
			noEvent: true, sameCatalog: true,
			end: stage.DiscUndone,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if files := listFilesUnder(t, fx.vars["{OUT}"]); len(files) != 1 {
					t.Errorf("files of --out after the refusal: %v, want the one file", files)
				}
			},
		},
		stateCase{
			row: "10d", name: "pack --out of a file",
			start: stage.DiscUndone, setup: outDirSetup(false),
			args:    []string{"pack", "--capacity=64MiB", "--out={OUT}"},
			noEvent: true, sameCatalog: true,
			end: stage.DiscUndone,
		},
	)
}
