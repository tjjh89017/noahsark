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
	}
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
			start: stage.DiscUndone, args: []string{"pack", "--capacity=64MiB"},
			stdout: []string{`packed disc 1 "{REF} disc 1": `, " item(s), ", " bytes\nuuid: "}, next: true,
			end: stage.DiscUndone, check: newDiscIsPacked,
		},
		stateCase{
			row: "6", name: "pack with no --capacity",
			start: stage.DiscUndone, args: []string{"pack"},
			exit: 2, stderr: []string{"pack needs --capacity"}, noEvent: true,
			end: stage.DiscUndone,
		},
		stateCase{
			row: "7", name: "pack with a capacity that holds not one item",
			start: stage.DiscUndone, args: []string{"pack", "--capacity=50KiB"},
			exit: 2, stderr: []string{"capacity ", " holds not one item"}, noEvent: true,
			absent: []string{"internal error"},
			end:    stage.DiscUndone,
		},
		stateCase{
			row: "8", name: "pack with nothing staged",
			start: stage.DiscPacked, args: []string{"pack", "--capacity=64MiB"},
			stdout: []string{"pack: nothing staged\n"}, exact: true, next: true, noEvent: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "9", name: "pack while another disc is packed",
			start: stage.DiscPacked, setup: addFileSetup,
			args:   []string{"pack", "--capacity=64MiB"},
			stdout: []string{`packed disc 1 "{REF} disc 1": `, " item(s), ", " bytes\nuuid: "}, next: true,
			end: stage.DiscPacked, word: stage.WordPacked, check: newDiscIsPacked,
		},
		stateCase{
			row: "10a", name: "dry run with nothing staged",
			start: stage.DiscPacked, args: []string{"pack", "--capacity=64MiB", "--dry-run"},
			stdout: []string{"pack: nothing staged\n"}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "10b", name: "dry run of staged items",
			start: stage.DiscUndone, args: []string{"pack", "--capacity=64MiB", "--dry-run"},
			stdout:  []string{"disc 1: ", " items, ", " bytes\n", "total: 1 discs, ", " items, ", " bytes\n"},
			noEvent: true, sameCatalog: true,
			end: stage.DiscUndone, check: onlyTheFixtureDisc,
		},
		stateCase{
			row: "10c", name: "pack --out of a directory that holds files",
			start: stage.DiscUndone, setup: outDirSetup(true),
			args: []string{"pack", "--capacity=64MiB", "--out={OUT}"},
			exit: 2, stderr: []string{"--out={OUT} holds files; give an empty or absent directory\n"},
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
			args: []string{"pack", "--capacity=64MiB", "--out={OUT}"},
			exit: 2, stderr: []string{"--out={OUT} is not a directory\n"},
			noEvent: true, sameCatalog: true,
			end: stage.DiscUndone,
		},
	)
}
