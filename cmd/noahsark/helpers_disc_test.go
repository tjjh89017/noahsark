package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// discFixture is a repository with one disc in a chosen state. The
// helpers build it through the CLI with the fake env, where a command
// exists for the step.
type discFixture struct {
	work string
	repo string
	src  string
	// uuid is the uuid text of the disc. seq and label name it.
	uuid  string
	seq   uint64
	label string
	// root is a copy of the disc root outside the repository: the
	// stand-in for the mounted disc.
	root string
	// vars maps more placeholders of a state case to their text.
	vars map[string]string
	// cells maps placeholders of the cells of the state x event table,
	// such as N, to the value that the event prints.
	cells map[string]string
}

// cell makes the placeholder name of the cells stand for value. An
// empty value matches the placeholder by its kind.
func (fx *discFixture) cell(name, value string) {
	if fx.cells == nil {
		fx.cells = map[string]string{}
	}
	fx.cells[name] = value
}

// name is the disc name of a message that reports a change:
// disc SEQ "LABEL".
func (fx *discFixture) name() string { return discNameShort(fx.seq, fx.label) }

// set makes the placeholder key stand for value in the texts of a state
// case.
func (fx *discFixture) set(key, value string) {
	if fx.vars == nil {
		fx.vars = map[string]string{}
	}
	fx.vars[key] = value
}

// filler returns the function that replaces the placeholders of a state
// case with the texts of fx.
func (fx *discFixture) filler() func(string) string {
	pairs := []string{
		"{DISC}", fx.name(),
		"{SEQ}", strconv.FormatUint(fx.seq, 10),
		"{LABEL}", fx.label,
		"{UUID}", fx.uuid,
		"{ROOT}", fx.root,
		"{SRC}", fx.src,
		"{REPO}", fx.repo,
		"{REF}", defaultRefName(),
	}
	for k, v := range fx.vars {
		pairs = append(pairs, k, v)
	}
	return strings.NewReplacer(pairs...).Replace
}

// uuidBytes is the uuid of the disc.
func (fx *discFixture) uuidBytes(t *testing.T) [16]byte {
	t.Helper()
	u, err := decodeUUID(strings.ReplaceAll(fx.uuid, "-", ""))
	if err != nil {
		t.Fatalf("bad disc uuid %q: %v", fx.uuid, err)
	}
	return u
}

// run runs the CLI with --repo of the fixture, then args.
func (fx *discFixture) run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	return runCmd(t, append([]string{"--repo=" + fx.repo}, args...)...)
}

// mustRun runs the CLI like run, and fails the test on an exit code
// other than 0.
func (fx *discFixture) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out := fx.run(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d: %s", args, code, out)
	}
	return out
}

// repoWithDisc returns a repository with one disc in state, and nothing
// staged:
//
//   - packed: init, commit, pack.
//   - burned: then disc burned.
//   - verified: then a verify of the copy of the disc root.
//   - on disc only: then gc with no wait.
//   - lost: a verified disc, then the records that disc lost writes.
//   - missing: two discs packed, the repository removed, and recover of
//     the second disc only. The fixture names the first disc.
//   - undone: a packed disc, then the records that pack --undo writes.
func repoWithDisc(t *testing.T, state stage.DiscState) *discFixture {
	t.Helper()
	if state == stage.DiscMissing {
		return repoWithMissingDisc(t)
	}
	work := t.TempDir()
	fx := &discFixture{work: work, repo: filepath.Join(work, "repo"), src: writeFixtureSource(t)}
	if code, out := runIn(t, fx.repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	fx.mustRun(t, "commit", fx.src)
	packOut := fx.mustRun(t, "pack", "--capacity=64MiB")
	fx.uuid = packedDiscUUID(t, packOut)
	fx.seq, fx.label = 0, defaultRefName()+" disc 0"
	fx.root = filepath.Join(work, "disc")
	copyTree(t, packedTreeDir(t, fx.repo, packOut), fx.root)

	switch state {
	case stage.DiscPacked:
	case stage.DiscUndone:
		markPackUndoneInLog(t, fx)
	case stage.DiscBurned:
		fx.mustRun(t, "disc", "burned", fx.uuid)
	case stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscLost:
		fx.mustRun(t, "disc", "burned", fx.uuid)
		fx.mustRun(t, "verify", fx.root)
		switch state {
		case stage.DiscOnDiscOnly:
			fx.mustRun(t, "gc")
		case stage.DiscLost:
			markDiscLostInLog(t, fx)
		}
	default:
		t.Fatalf("repoWithDisc: no fixture for state %s", state)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != state {
		t.Fatalf("repoWithDisc: disc state %s, want %s", got, state)
	}
	return fx
}

// repoWithMissingDisc packs two discs, removes the repository, and
// recovers it from the second disc only. The first disc is then
// missing. The fixture names the first disc; its root is the pack --out
// directory of that disc.
func repoWithMissingDisc(t *testing.T) *discFixture {
	t.Helper()
	work := t.TempDir()
	fx := &discFixture{work: work, repo: filepath.Join(work, "repo"), src: writeFixtureSource(t)}
	if code, out := runIn(t, fx.repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	fx.mustRun(t, "commit", fx.src)
	fx.root = filepath.Join(work, "disc0")
	fx.uuid = packedDiscUUID(t, fx.mustRun(t, "pack", "--capacity=64MiB", "--out="+fx.root))
	fx.seq, fx.label = 0, defaultRefName()+" disc 0"
	if err := os.WriteFile(filepath.Join(fx.src, "second.txt"), []byte("content of the second disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "commit", fx.src)
	second := filepath.Join(work, "disc1")
	fx.mustRun(t, "pack", "--capacity=64MiB", "--out="+second)
	if err := os.RemoveAll(fx.repo); err != nil {
		t.Fatal(err)
	}
	addFakeMount(t, fx.root, true)
	addFakeMount(t, second, true)
	if code, out := fx.run(t, "recover", "--source="+fx.src, "--disc="+second); code != 1 {
		t.Fatalf("recover of the second disc: exit %d, want 1: %s", code, out)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscMissing {
		t.Fatalf("repoWithMissingDisc: disc state %s, want missing", got)
	}
	return fx
}

// markDiscLostInLog writes the records that disc lost writes for the
// disc of fx, with no confirmation: the Lost event, then the item records
// of the lost disc, and the removal of its plan directory.
func markDiscLostInLog(t *testing.T, fx *discFixture) {
	t.Helper()
	u := fx.uuidBytes(t)
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Discs.Append(discEvent(fakeNow(), u, stage.EventLost)); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.CompleteDisc(u, nil, catalogHolds(fx.repo)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(testLayout(t, fx.repo).planDir(u)); err != nil {
		t.Fatal(err)
	}
}

// markPackUndoneInLog writes the records that pack --undo writes for the
// packed disc of fx: its items return to Staged, the PackUndone event,
// and the removal of its ledger row, its catalog tables and its plan
// directory.
func markPackUndoneInLog(t *testing.T, fx *discFixture) {
	t.Helper()
	u := fx.uuidBytes(t)
	layout := testLayout(t, fx.repo)
	logs, err := stage.OpenLogs(layout.stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkPackUndone(logs.Items.ItemsOfDiscInState(u, stage.Packed)...); err != nil {
		t.Fatal(err)
	}
	if err := logs.Discs.Append(discEvent(fakeNow(), u, stage.EventPackUndone)); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfig(configPath(fx.repo))
	if err != nil {
		t.Fatal(err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		t.Fatal(err)
	}
	rows := slices.DeleteFunc(ledger.Rows, func(r format.DiscsRow) bool { return r.DiscUUID == u })
	if err := image.SaveDiscsLedger(layout.discsLedgerFile(), repoUUID, rows); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDisc(u); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(layout.planDir(u)); err != nil {
		t.Fatal(err)
	}
}
