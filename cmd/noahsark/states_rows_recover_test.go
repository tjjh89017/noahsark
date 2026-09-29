package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	registerStateCases(stateCase{
		row: "69", name: "missing disc given",
		start: stage.DiscMissing, args: []string{"recover", "--source={SRC}", "--disc={ROOT}"},
		exact: true,
		end:   stage.DiscOnDiscOnly, word: stage.WordOnDisc,
	})
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscLost} {
		registerStateCases(stateCase{
			row: "70c", name: s.String() + " disc given",
			start: s, args: []string{"recover", "--source={SRC}", "--disc={ROOT}"},
			exact: true,
			end:   s,
		})
	}
}

// damageObject flips the last byte of the first object of kind in the
// disc root root, and returns the id of that object.
func damageObject(t *testing.T, root string, kind format.ObjectKind) object.ID {
	t.Helper()
	rr, err := image.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := image.ObjectPaths(base, &rr.Index, names)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rr.Index.Objects {
		if row.Kind != kind {
			continue
		}
		info, err := os.Stat(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		flipByte(t, paths[i], info.Size()-1)
		return object.ID(row.ContentID)
	}
	t.Fatalf("the disc root %s holds no object of kind %d", root, kind)
	return object.ID{}
}

// recoverFixture is a repository with one disc, packed, then removed.
// Only the copy of the disc root stays.
func recoverFixture(t *testing.T) *discFixture {
	t.Helper()
	fx := repoWithDisc(t, stage.DiscPacked)
	removeRepoSetup(t, fx)
	return fx
}

// removeRepoSetup removes the repository of fx. Only the copy of the
// disc root stays.
func removeRepoSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	if err := os.RemoveAll(fx.repo); err != nil {
		t.Fatal(err)
	}
}

// runRecover runs recover of the disc root of fx.
func (fx *discFixture) runRecover(t *testing.T) (int, string, string) {
	t.Helper()
	te := newTestEnv(t.TempDir())
	code, _ := te.run("--repo="+fx.repo, "recover", "--source="+fx.src, "--disc="+fx.root)
	return code, te.out.String(), te.errOut.String()
}

// secondDiscCopySetup packs disc 1 after disc 0, copies its disc root
// outside the repository as a counted mount, and removes the
// repository. {UUID1} is the uuid of disc 1 and {ROOT1} is the copy.
func secondDiscCopySetup(t *testing.T, fx *discFixture) {
	t.Helper()
	secondDiscSetup(t, fx)
	second := filepath.Join(fx.work, "disc1")
	copyTree(t, fx.vars["{ROOT1}"], second)
	fx.set("{ROOT1}", second)
	removeRepoSetup(t, fx)
}

// indexObjects sets {OBJECTS} to the number of objects that the INDEX of
// the disc root of fx lists.
func indexObjects(t *testing.T, fx *discFixture) {
	t.Helper()
	rr, err := image.Read(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	fx.set("{OBJECTS}", strconv.Itoa(len(rr.Index.Objects)))
}

// countIs is a check: the number of items in state is the number of the
// placeholder key plus add.
func countIs(state stage.State, key string, add int) func(*testing.T, *discFixture, string, string) {
	return func(t *testing.T, fx *discFixture, _, _ string) {
		t.Helper()
		want, err := strconv.Atoi(fx.vars[key])
		if err != nil {
			t.Fatal(err)
		}
		if n := countByState(t, fx.repo, state); n != want+add {
			t.Errorf("%d item(s) in state %s, want %d", n, state, want+add)
		}
	}
}

// catalogStateEnds is a check: catalog-state.txt ends with mark.
func catalogStateEnds(mark string) func(*testing.T, *discFixture, string, string) {
	return func(t *testing.T, fx *discFixture, _, _ string) {
		t.Helper()
		state, err := os.ReadFile(testLayout(t, fx.repo).catalogStateFile())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(state), mark) {
			t.Errorf("catalog-state.txt %q, want the mark %q", state, mark)
		}
	}
}

// damageSetup removes the repository of fx and damages the first object
// of kind in its disc root.
func damageSetup(kind format.ObjectKind) func(*testing.T, *discFixture) {
	return func(t *testing.T, fx *discFixture) {
		t.Helper()
		removeRepoSetup(t, fx)
		indexObjects(t, fx)
		damageOneSetup(kind)(t, fx)
	}
}

// damageOneSetup damages the first object of kind in the disc root of
// fx. {BAD} and the placeholder ID of the cells are the full text id of
// that object. N, the count of damaged items, is 1.
func damageOneSetup(kind format.ObjectKind) func(*testing.T, *discFixture) {
	return func(t *testing.T, fx *discFixture) {
		t.Helper()
		bad := damageObject(t, fx.root, kind).TextForm()
		fx.set("{BAD}", bad)
		fx.cell("ID", bad)
		fx.cell("N", "1")
	}
}

// badHasNoRecord is a check: the damaged object {BAD} has no record in
// the item state log.
func badHasNoRecord(t *testing.T, fx *discFixture, _, _ string) {
	t.Helper()
	bad, err := object.ParseID(fx.vars["{BAD}"])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := openTestLog(t, fx.repo).Get(bad); ok {
		t.Error("the damaged object has a record")
	}
}

// secondDiscUUID is the uuid of disc 1 of secondDiscCopySetup.
func secondDiscUUID(_ *testing.T, fx *discFixture, _ string) string {
	return fx.vars["{UUID1}"]
}

func init() {
	recoverArgs := []string{"recover", "--source={SRC}", "--disc={ROOT}"}
	registerStateCases(
		// Row 67: recover of a lost repository from its one disc.
		stateCase{
			row: "67", name: "no repository, one disc",
			start: stage.DiscPacked,
			setup: func(t *testing.T, fx *discFixture) {
				removeRepoSetup(t, fx)
				indexObjects(t, fx)
			},
			args:  recoverArgs,
			exact: true,
			end:   stage.DiscOnDiscOnly, word: stage.WordOnDisc,
			check: allChecks(
				lastCheckIs(stage.CheckResultNone),
				countIs(stage.OnDisc, "{OBJECTS}", 0),
				catalogStateEnds(" complete\n"),
				func(t *testing.T, fx *discFixture, _, _ string) {
					cfg, err := readConfig(configPath(fx.repo))
					if err != nil {
						t.Fatal(err)
					}
					if cfg.SourceRoot != fx.src {
						t.Errorf("sources.root %q, want %q", cfg.SourceRoot, fx.src)
					}
					if _, err := os.Stat(testLayout(t, fx.repo).gitignoreFile()); err != nil {
						t.Errorf(".gitignore: %v", err)
					}
					state, err := os.ReadFile(testLayout(t, fx.repo).catalogStateFile())
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(state), "partial") {
						t.Errorf("catalog-state.txt %q, want every snapshot complete", state)
					}
				},
			),
		},
		// Row 68: recover of a lost repository from the second of two
		// discs. The first disc becomes missing.
		stateCase{
			row: "68", name: "no repository, the second of two discs",
			start: stage.DiscPacked, setup: secondDiscCopySetup,
			args:    []string{"recover", "--source={SRC}", "--disc={ROOT1}"},
			exact:   true,
			subject: secondDiscUUID,
			end:     stage.DiscMissing,
		},
		// Row 70: recover of a disc that the repository does not know
		// into an existing repository. An item that the repository staged
		// again keeps its state.
		stateCase{
			row: "70", name: "a disc that the repository does not know",
			start: stage.DiscPacked,
			setup: func(t *testing.T, fx *discFixture) {
				secondDiscCopySetup(t, fx)
				fx.mustRun(t, "recover", "--source="+fx.src, "--disc="+fx.root)
				fx.mustRun(t, "commit", fx.src)
				staged := countByState(t, fx.repo, stage.Staged)
				if staged == 0 {
					t.Fatal("the commit staged no item")
				}
				fx.set("{STAGED}", strconv.Itoa(staged))
			},
			args:    []string{"recover", "--source={SRC}", "--disc={ROOT1}"},
			exact:   true,
			subject: secondDiscUUID,
			end:     stage.DiscOnDiscOnly,
			check:   countIs(stage.Staged, "{STAGED}", 0),
		},
		// Row 70b: a disc of another repository is refused.
		stateCase{
			row: "70b", name: "a disc of another repository",
			start: stage.DiscPacked,
			setup: func(t *testing.T, fx *discFixture) {
				other := repoWithDisc(t, stage.DiscPacked)
				cfg, err := readConfig(configPath(other.repo))
				if err != nil {
					t.Fatal(err)
				}
				otherRepo, err := decodeUUID(cfg.RepoUUID)
				if err != nil {
					t.Fatal(err)
				}
				fx.cell("UUID", other.uuid)
				fx.cell("RUUID", uuidText(otherRepo))
				fx.root = other.root
			},
			args:  recoverArgs,
			exact: true, noEvent: true,
			end: stage.DiscPacked, word: stage.WordPacked,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if n := len(readDiscLog(t, fx.repo).Discs()); n != 1 {
					t.Errorf("the disc state log knows %d disc(s), want 1", n)
				}
			},
		},
		// Row 70d: a damaged copy of a verified disc of the repository.
		// recover writes no event.
		stateCase{
			row: "70d", name: "a damaged copy of a verified disc",
			start: stage.DiscVerified, setup: damageOneSetup(format.ObjectKindChunk),
			args:  recoverArgs,
			exact: true, noEvent: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
	)
	// Row 70a: a lost repository from a disc with a damaged object. The
	// other objects are recorded, and the disc is on disc only with a
	// failed check. A damaged tree makes the snapshot partial.
	for _, damaged := range []struct {
		kind format.ObjectKind
		mark string
	}{{format.ObjectKindChunk, " complete\n"}, {format.ObjectKindTree, " partial\n"}} {
		registerStateCases(stateCase{
			row: "70a", name: fmt.Sprintf("no repository, a damaged object of kind %d", damaged.kind),
			start: stage.DiscPacked, setup: damageSetup(damaged.kind),
			args:  recoverArgs,
			exact: true,
			end:   stage.DiscOnDiscOnly,
			check: allChecks(
				lastCheckIs(stage.CheckResultFailed),
				badHasNoRecord,
				countIs(stage.OnDisc, "{OBJECTS}", -1),
				catalogStateEnds(damaged.mark),
			),
		})
	}
}

// TestRecoverRefusals checks a disc root that is not a counted mount,
// and a disc that pack --undo removed. Each changes nothing and prints
// no next line.
func TestRecoverRefusals(t *testing.T) {
	t.Run("not a counted mount", func(t *testing.T) {
		fx := recoverFixture(t)
		addFakeMount(t, fx.root, false)
		code, stdout, stderr := fx.runRecover(t)
		if code != 1 || stdout != "" {
			t.Fatalf("exit %d, stdout %q, want 1 and no stdout", code, stdout)
		}
		if !strings.Contains(stderr, fx.root+" is not counted: not a read-only mount;") {
			t.Errorf("stderr %q", stderr)
		}
		if _, err := os.Stat(fx.repo); !os.IsNotExist(err) {
			t.Errorf("the repository exists: %v", err)
		}
	})
	t.Run("undone disc", func(t *testing.T) {
		fx := repoWithDisc(t, stage.DiscUndone)
		code, stdout, stderr := fx.runRecover(t)
		if code != 1 || stdout != "" {
			t.Fatalf("exit %d, stdout %q, want 1 and no stdout", code, stdout)
		}
		if !strings.Contains(stderr, "disc "+fx.uuid+" was undone by pack --undo; it is not in this repository") {
			t.Errorf("stderr %q", stderr)
		}
		if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscUndone {
			t.Errorf("disc state %s, want undone", got)
		}
	})
}
