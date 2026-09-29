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

// packUndoWarning is the warning of "pack --undo" in rows 11, 11a, 80
// and 81.
var packUndoWarning = []string{
	`warning: {DISC} ({UUID}): packed -> undone`,
	"the items return to staged; the disc number {SEQ} is not used again",
}

// packUndoDone is the line of a pack --undo that returned the items of
// the one disc of the fixture.
const packUndoDone = `{DISC}: pack undone, `

func init() {
	refused := func(row, name string, start stage.DiscState, reason string, word stage.ItemWord) stateCase {
		return stateCase{
			row: row, name: name,
			start: start, args: []string{"--yes", "pack", "--undo", "{SEQ}"},
			exit:   1,
			stderr: []string{"disc {SEQ} " + reason + "; pack cannot be undone"},
			absent: []string{confirmQuestion, "warning:"},
			end:    start, word: word,
		}
	}
	registerStateCases(
		stateCase{
			row: "11", name: "pack undone, answer yes",
			start: stage.DiscPacked, args: []string{"pack", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			stderr: append(slices.Clone(packUndoWarning), confirmQuestion),
			stdout: []string{packUndoDone, "item(s) returned to staged"}, next: true,
			absent: []string{"disc root"},
			end:    stage.DiscUndone,
		},
		stateCase{
			row: "11a", name: "pack undo, answer no",
			start: stage.DiscPacked, args: []string{"pack", "--undo", "{SEQ}"},
			stdin: stdinNo, exit: 1,
			stderr: append(slices.Clone(packUndoWarning), confirmQuestion),
			stdout: []string{"nothing changed"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		refused("13", "pack undo of a burned disc", stage.DiscBurned, "has a burn record", stage.WordBurned),
		refused("14", "pack undo of a verified disc", stage.DiscVerified, "is no longer packed", stage.WordClean),
		refused("14", "pack undo of an on disc only disc", stage.DiscOnDiscOnly, "is no longer packed", stage.WordOnDisc),
		refused("14", "pack undo of a lost disc", stage.DiscLost, "is no longer packed", ""),
		refused("14", "pack undo of a missing disc", stage.DiscMissing, "is no longer packed", ""),
		stateCase{
			row: "80", name: "pack undo with --yes",
			start: stage.DiscPacked, args: []string{"--yes", "pack", "--undo", "{SEQ}"},
			stderr: packUndoWarning, absent: []string{confirmQuestion},
			stdout: []string{packUndoDone}, next: true,
			end: stage.DiscUndone,
		},
		stateCase{
			row: "80", name: "pack undo with --force-yes",
			start: stage.DiscPacked, args: []string{"--force-yes", "pack", "--undo", "{SEQ}"},
			stderr: packUndoWarning, absent: []string{confirmQuestion},
			stdout: []string{packUndoDone}, next: true,
			end: stage.DiscUndone,
		},
		stateCase{
			row: "81", name: "pack undo with no terminal and no answer flag",
			start: stage.DiscPacked, args: []string{"pack", "--undo", "{SEQ}"},
			exit:   1,
			stderr: packUndoWarning, absent: []string{confirmQuestion},
			stdout: []string{"nothing changed"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
	)
}

// undoStdout runs "--yes pack --undo DISC" on the repository of fx and
// returns its standard output. It fails the test on an exit code other
// than 0.
func undoStdout(t *testing.T, fx *discFixture, disc string) string {
	t.Helper()
	te := newTestEnv(t.TempDir())
	if code, out := te.run("--repo="+fx.repo, "--yes", "pack", "--undo", disc); code != 0 {
		t.Fatalf("pack --undo %s: exit %d: %s", disc, code, out)
	}
	return te.out.String()
}

// ledgerRows returns the rows of the disc ledger of repo.
func ledgerRows(t *testing.T, repo string) []format.DiscsRow {
	t.Helper()
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := image.LoadDiscsLedger(testLayout(t, repo).discsLedgerFile(), repoUUID)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Rows
}

// wantUndoneDiscGone checks that the repository at repo holds no file of
// the undone disc u: no ledger row, no catalog tables, no plan
// directory, and no Packed item.
func wantUndoneDiscGone(t *testing.T, repo string, u [16]byte) {
	t.Helper()
	if slices.ContainsFunc(ledgerRows(t, repo), func(r format.DiscsRow) bool { return r.DiscUUID == u }) {
		t.Error("the disc ledger still names the undone disc")
	}
	if _, err := os.Lstat(filepath.Join(repoCatalogDir(t, repo), "discs", uuidText(u))); !os.IsNotExist(err) {
		t.Errorf("the catalog tables of the undone disc stay: %v", err)
	}
	if _, err := os.Lstat(testLayout(t, repo).planDir(u)); !os.IsNotExist(err) {
		t.Errorf("the plan directory of the undone disc stays: %v", err)
	}
	if n := len(readLogs(t, repo).Items.ItemsOfDiscInState(u, stage.Packed)); n != 0 {
		t.Errorf("%d item(s) are still Packed on the undone disc", n)
	}
}

// TestPackUndoRemovesTheDiscAndSkipsItsNumber is row 11: the undo removes
// the ledger row, the catalog tables and the plan directory. The next
// pack gets the next number, and its DISCS table does not name the
// undone disc.
func TestPackUndoRemovesTheDiscAndSkipsItsNumber(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	u := fx.uuidBytes(t)
	staged := countByState(t, fx.repo, stage.Staged)
	packed := countByState(t, fx.repo, stage.Packed)

	out := undoStdout(t, fx, fx.uuid)
	want := fx.name() + ": pack undone, " + strconv.Itoa(packed) + " item(s) returned to staged\n" + nextStatusLine + "\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("pack --undo output %q, want the suffix %q", out, want)
	}
	wantUndoneDiscGone(t, fx.repo, u)
	if got := countByState(t, fx.repo, stage.Staged); got != staged+packed {
		t.Errorf("%d staged item(s) after the undo, want %d", got, staged+packed)
	}

	if code, out := fx.run(t, "--yes", "pack", "--undo", "0"); code != 2 || !strings.Contains(out, "no disc matches 0") {
		t.Errorf("pack --undo of the undone number: exit %d, want 2 and no match: %s", code, out)
	}

	out = fx.mustRun(t, "pack", "--capacity=64MiB")
	if !strings.Contains(out, "packed disc 1 ") {
		t.Fatalf("pack after the undo: %q, want disc 1", out)
	}
	c, err := catalog.Open(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	discs, err := c.Discs()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range discs.Rows {
		if r.DiscUUID == u || r.DiscSeq == 0 {
			t.Errorf("the DISCS table of the next disc names the undone disc: seq %d, %s", r.DiscSeq, uuidText(r.DiscUUID))
		}
	}
}

// TestPackUndoKeepsTheOutDirectory is row 11 for a pack --out=DIR disc:
// the undo removes the symlink and keeps DIR.
func TestPackUndoKeepsTheOutDirectory(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	fx := &discFixture{work: work, repo: repo, src: writeFixtureSource(t)}
	fx.mustRun(t, "commit", fx.src)
	outDir := filepath.Join(work, "disc0")
	fx.uuid = packedDiscUUID(t, fx.mustRun(t, "pack", "--capacity=64MiB", "--out="+outDir))
	u := fx.uuidBytes(t)

	out := undoStdout(t, fx, "0")
	want := "disc root " + outDir + " kept; delete it yourself\n" + nextStatusLine + "\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("pack --undo output %q, want the suffix %q", out, want)
	}
	wantUndoneDiscGone(t, repo, u)
	if n, err := countFiles(outDir); err != nil || n == 0 {
		t.Errorf("the --out directory lost its files: %d files, %v", n, err)
	}
}

// TestPackUndoRefusesAnOlderDisc is row 12: a later disc exists. After
// the undo of the later disc, the older disc is the newest again.
func TestPackUndoRefusesAnOlderDisc(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	if err := os.WriteFile(filepath.Join(fx.src, "second.txt"), []byte("content of the second disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "commit", fx.src)
	fx.mustRun(t, "pack", "--capacity=64MiB")

	code, out := fx.run(t, "--yes", "pack", "--undo", "0")
	if code != 1 || !strings.Contains(out, "disc 0 is not the newest disc; pack cannot be undone") {
		t.Fatalf("pack --undo 0: exit %d, want 1 and the refusal: %s", code, out)
	}
	if strings.Contains(out, "warning:") || strings.Contains(out, nextStatusLine) {
		t.Errorf("a refusal printed the warning or the next line: %s", out)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscPacked {
		t.Errorf("disc 0 is %s after the refusal, want packed", got)
	}

	fx.mustRun(t, "--yes", "pack", "--undo", "1")
	fx.mustRun(t, "--yes", "pack", "--undo", "0")
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscUndone {
		t.Errorf("disc 0 is %s, want undone", got)
	}
	if n := countByState(t, fx.repo, stage.Packed); n != 0 {
		t.Errorf("%d item(s) still Packed after both undos", n)
	}
}

// TestPackUndoTakesNoOtherOption refuses --undo with a pack option, and
// --undo with no DISC, as usage errors.
func TestPackUndoTakesNoOtherOption(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	for _, args := range [][]string{
		{"--yes", "pack", "--undo", "--capacity=64MiB", "0"},
		{"--yes", "pack", "--undo", "--dry-run", "0"},
		{"--yes", "pack", "--undo"},
		{"--yes", "pack", "--undo", "0", "1"},
	} {
		if code, out := fx.run(t, args...); code != 2 || !strings.Contains(out, packUndoUsage) {
			t.Errorf("%v: exit %d, want 2 and the usage line: %s", args, code, out)
		}
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscPacked {
		t.Errorf("disc state %s after the usage errors, want packed", got)
	}
}

// TestPackFinishesAStoppedUndo writes only the item records and the
// PackUndone event, as a pack --undo that stopped after its first step.
// The next pack removes the other records of the disc before it writes
// a disc, and the new disc does not name the undone disc.
func TestPackFinishesAStoppedUndo(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	u := fx.uuidBytes(t)
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkPackUndone(logs.Items.ItemsOfDiscInState(u, stage.Packed)...); err != nil {
		t.Fatal(err)
	}
	if err := logs.Discs.Append(discEvent(fakeNow(), u, stage.EventPackUndone)); err != nil {
		t.Fatal(err)
	}

	code, out := fx.run(t, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "disc 0: an earlier pack --undo stopped") || !strings.Contains(out, "packed disc 1 ") {
		t.Errorf("pack output %q, want the finish note and disc 1", out)
	}
	wantUndoneDiscGone(t, fx.repo, u)
}
