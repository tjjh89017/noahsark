package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// verifyUndoWarning is the warning of "verify --undo" in rows 47, 48, 80
// and 81.
var verifyUndoWarning = []string{
	`warning: {DISC} ({UUID}): verified -> burned`,
	"the disc is no longer verified, and gc holds its data",
}

// The fake mount table lists {ROOT} as a read-only loop mount, so each
// verify of {ROOT} in these cases is a counted verify.
func init() {
	registerStateCases(
		stateCase{
			row: "31", name: "packed disc verified",
			start: stage.DiscPacked, args: []string{"verify", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nburn recorded; verified\n"}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "32", name: "packed disc verified on a loop mount",
			start: stage.DiscPacked, args: []string{"verify", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nburn recorded; verified\n"}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "32", name: "burned disc verified on a loop mount",
			start: stage.DiscBurned, args: []string{"verify", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nverified\n"}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "33", name: "burned disc verified",
			start: stage.DiscBurned, args: []string{"verify", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nverified\n"}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "34", name: "verified disc verified again",
			start: stage.DiscVerified, args: []string{"verify", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nalready verified; check logged\n"}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "35", name: "on disc only disc verified",
			start: stage.DiscOnDiscOnly, args: []string{"verify", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\ncheck logged\n"}, next: true,
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "36", name: "packed disc verified with --no-mark",
			start: stage.DiscPacked, args: []string{"verify", "--no-mark", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nnot marked; to record this burn, run: noahsark disc burned {SEQ}\n"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "37", name: "burned disc verified with --no-mark",
			start: stage.DiscBurned, args: []string{"verify", "--no-mark", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nnot marked\n"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "37", name: "verified disc verified with --no-mark",
			start: stage.DiscVerified, args: []string{"verify", "--no-mark", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nnot marked\n"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "37", name: "on disc only disc verified with --no-mark",
			start: stage.DiscOnDiscOnly, args: []string{"verify", "--no-mark", "{ROOT}"},
			stdout: []string{`{DISC}: `, " items, ok\nnot marked\n"},
			end:    stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "44", name: "lost disc verified",
			start: stage.DiscLost, args: []string{"verify", "{ROOT}"},
			exit: 1, stderr: []string{"disc {SEQ} is marked lost"},
			absent: []string{"items, ok", "bad;"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "44", name: "lost disc verified with --no-mark",
			start: stage.DiscLost, args: []string{"verify", "--no-mark", "{ROOT}"},
			exit: 1, stderr: []string{"disc {SEQ} is marked lost"},
			absent: []string{"items, ok"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "44", name: "lost disc healed",
			start: stage.DiscLost, args: []string{"verify", "--heal", "--out={ROOT}.healed", "{ROOT}"},
			exit: 1, stderr: []string{"disc {SEQ} is marked lost"},
			absent: []string{"healed", "items, ok"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "45", name: "missing disc verified",
			start: stage.DiscMissing, args: []string{"verify", "{ROOT}"},
			exit: 1, stderr: []string{"disc {SEQ} is missing; give it to recover"},
			absent: []string{"items, ok"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "45", name: "missing disc healed",
			start: stage.DiscMissing, args: []string{"verify", "--heal", "--out={ROOT}.healed", "{ROOT}"},
			exit: 1, stderr: []string{"disc {SEQ} is missing; give it to recover"},
			absent: []string{"healed", "items, ok"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "46", name: "disc of an undone pack verified",
			start: stage.DiscUndone, args: []string{"verify", "{ROOT}"},
			exit: 1, stderr: []string{"disc {UUID} is not in this repository"},
			absent: []string{"items, ok"},
			end:    stage.DiscUndone,
		},
		stateCase{
			row: "47", name: "verify undone, answer yes",
			start: stage.DiscVerified, args: []string{"verify", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			stderr: append(verifyUndoWarning, confirmQuestion),
			stdout: []string{`{DISC}: verified record removed; burn record kept`}, next: true,
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "48", name: "verify undo, answer no",
			start: stage.DiscVerified, args: []string{"verify", "--undo", "{SEQ}"},
			stdin: stdinNo, exit: 1,
			stderr: append(verifyUndoWarning, confirmQuestion),
			stdout: []string{"nothing changed"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "49", name: "verify undo of an on disc only disc",
			start: stage.DiscOnDiscOnly, args: []string{"verify", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} is on disc only; gc already freed the staged copy; verify cannot be undone"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "50", name: "verify undo of a packed disc",
			start: stage.DiscPacked, args: []string{"verify", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} has no verified record"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "50", name: "verify undo of a burned disc",
			start: stage.DiscBurned, args: []string{"verify", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} has no verified record"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "51", name: "verify undo of a lost disc",
			start: stage.DiscLost, args: []string{"verify", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} is marked lost"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "51", name: "verify undo of a missing disc",
			start: stage.DiscMissing, args: []string{"verify", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} is missing"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "80", name: "verify undo with --yes",
			start: stage.DiscVerified, args: []string{"--yes", "verify", "--undo", "{SEQ}"},
			stderr: verifyUndoWarning, absent: []string{confirmQuestion},
			stdout: []string{`{DISC}: verified record removed; burn record kept`}, next: true,
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "81", name: "verify undo with no terminal and no answer flag",
			start: stage.DiscVerified, args: []string{"verify", "--undo", "{SEQ}"},
			exit:   1,
			stderr: verifyUndoWarning, absent: []string{confirmQuestion},
			stdout: []string{"nothing changed"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
	)
}

// verifyRootKind is the disc root that a verify row case checks.
type verifyRootKind int

const (
	// rootCounted is the copy of the disc root, a read-only loop mount.
	rootCounted verifyRootKind = iota
	// rootNotMountPoint is a copy of the disc root that is no mount point.
	rootNotMountPoint
	// rootReadWrite is a copy of the disc root on a read-write mount.
	rootReadWrite
	// rootPackedTree is the disc root that pack wrote under staging.
	rootPackedTree
)

// verifyRowCase is a row case of verify that the state case harness
// cannot express: it damages the disc root, checks a root that is not
// counted, or runs with no repository.
type verifyRowCase struct {
	row, name string
	start     stage.DiscState
	root      verifyRootKind
	damaged   bool
	noRepo    bool
	noMark    bool
	exit      int
	// stdout holds the lines after the ok line or the bad line. The
	// case builds the first line from the disc name, the count and bad.
	bad    string
	stdout []string
	next   bool
	end    stage.DiscState
	// last is the result of the last check after the event.
	last stage.CheckResult
}

// TestVerifyRowsOfDamageAndRoots runs the rows of verify that damage the
// disc, check a root that is not counted, or run with no repository.
func TestVerifyRowsOfDamageAndRoots(t *testing.T) {
	cases := []verifyRowCase{
		{row: "38", name: "packed tree of a packed disc", start: stage.DiscPacked, root: rootPackedTree,
			stdout: []string{notCountedDisc}, end: stage.DiscPacked},
		{row: "38", name: "not a mount point, burned disc", start: stage.DiscBurned, root: rootNotMountPoint,
			stdout: []string{notCountedDisc}, end: stage.DiscBurned},
		{row: "38", name: "read-write mount, verified disc", start: stage.DiscVerified, root: rootReadWrite,
			stdout: []string{notCountedDisc}, end: stage.DiscVerified, last: stage.CheckResultOK},
		{row: "38", name: "packed tree with --no-mark", start: stage.DiscPacked, root: rootPackedTree, noMark: true,
			stdout: []string{notCountedDisc}, end: stage.DiscPacked},
		{row: "38a", name: "no repository", start: stage.DiscVerified, noRepo: true,
			stdout: []string{notCountedNoRepo}, end: stage.DiscVerified, last: stage.CheckResultOK},
		{row: "38b", name: "damaged, not a mount point, verified disc", start: stage.DiscVerified, root: rootNotMountPoint, damaged: true,
			exit: 1, bad: reasonDiscRootDamaged, stdout: []string{notCountedDisc}, end: stage.DiscVerified, last: stage.CheckResultOK},
		{row: "38b", name: "damaged, read-write mount, on disc only disc", start: stage.DiscOnDiscOnly, root: rootReadWrite, damaged: true,
			exit: 1, bad: reasonDiscRootDamaged, stdout: []string{notCountedDisc}, end: stage.DiscOnDiscOnly, last: stage.CheckResultOK},
		{row: "38c", name: "damaged, no repository", start: stage.DiscVerified, noRepo: true, damaged: true,
			exit: 1, bad: reasonDiscRootDamaged, stdout: []string{notCountedNoRepo}, end: stage.DiscVerified, last: stage.CheckResultOK},
		{row: "39", name: "damaged packed tree", start: stage.DiscPacked, root: rootPackedTree, damaged: true,
			exit: 1, bad: reasonPackedTreeDamaged, stdout: []string{notCountedDisc}, end: stage.DiscPacked},
		{row: "40", name: "damaged packed disc", start: stage.DiscPacked, damaged: true,
			exit: 1, bad: "this disc is bad; no record to remove", next: true, end: stage.DiscPacked, last: stage.CheckResultFailed},
		{row: "41", name: "damaged burned disc", start: stage.DiscBurned, damaged: true,
			exit: 1, bad: "this disc is bad; burn record removed", next: true, end: stage.DiscPacked, last: stage.CheckResultFailed},
		{row: "42", name: "damaged verified disc", start: stage.DiscVerified, damaged: true,
			exit: 1, bad: "this disc is bad; verified record removed; gc holds the data", next: true, end: stage.DiscBurned, last: stage.CheckResultFailed},
		{row: "43", name: "damaged on disc only disc", start: stage.DiscOnDiscOnly, damaged: true,
			exit: 1, bad: "the staged copy is already freed; copy this disc now, or use your second copy, or run: noahsark disc lost 0",
			next: true, end: stage.DiscOnDiscOnly, last: stage.CheckResultFailed},
		{row: "37", name: "damaged verified disc with --no-mark", start: stage.DiscVerified, noMark: true, damaged: true,
			exit: 1, bad: reasonDiscRootDamaged, stdout: []string{notMarked}, end: stage.DiscVerified, last: stage.CheckResultOK},
	}
	for _, c := range cases {
		t.Run("row "+c.row+"/"+c.name, func(t *testing.T) {
			runVerifyRowCase(t, c)
		})
	}
}

// runVerifyRowCase runs one verifyRowCase.
func runVerifyRowCase(t *testing.T, c verifyRowCase) {
	t.Helper()
	fx := repoWithDisc(t, c.start)
	root := verifyRoot(t, fx, c.root)
	if c.damaged {
		corruptDiscRoot(t, root)
	}
	logBefore := discLogBytes(t, fx.repo)
	catalogBefore := listFilesUnder(t, testLayout(t, fx.repo).catalogDir())

	name := fx.name()
	if c.noRepo {
		name = `disc ` + fx.uuid + ` "` + fx.label + `"`
	}
	args := []string{"verify"}
	if !c.noRepo {
		args = append([]string{"--repo=" + fx.repo}, args...)
	}
	if c.noMark {
		args = append(args, "--no-mark")
	}
	args = append(args, root)
	te := newTestEnv(t.TempDir())
	code, _ := te.run(args...)
	stdout, stderr := te.out.String(), te.errOut.String()
	if code != c.exit {
		t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, c.exit, stdout, stderr)
	}

	first := name + ": bad; " + c.bad
	if c.bad == "" {
		items := len(readLogs(t, fx.repo).Items.ItemsOfDisc(fx.uuidBytes(t)))
		if c.noRepo {
			items = objectsOnDisc(t, root)
		}
		first = name + ": " + strconv.Itoa(items) + " items, ok"
	}
	want := append([]string{first}, c.stdout...)
	if c.next {
		want = append(want, nextStatusLine)
	}
	if got := strings.Split(strings.TrimRight(stdout, "\n"), "\n"); !slices.Equal(got, want) {
		t.Errorf("stdout lines %q, want %q\nstderr: %s", got, want, stderr)
	}
	if c.damaged && !strings.HasPrefix(stderr, "noahsark: verify: ") {
		t.Errorf("stderr %q, want the detail of the failed check", stderr)
	}

	d := discState(t, fx.repo, fx.uuid)
	if d.State != c.end || d.LastCheck != c.last {
		t.Errorf("disc %s, last check %d; want %s, %d", d.State, d.LastCheck, c.end, c.last)
	}
	if !c.next {
		if !bytes.Equal(discLogBytes(t, fx.repo), logBefore) {
			t.Error("the verify wrote the disc state log")
		}
		if got := listFilesUnder(t, testLayout(t, fx.repo).catalogDir()); !slices.Equal(got, catalogBefore) {
			t.Error("the verify changed the catalog")
		}
	}
}

// verifyRoot returns the disc root of kind for the disc of fx.
func verifyRoot(t *testing.T, fx *discFixture, kind verifyRootKind) string {
	t.Helper()
	switch kind {
	case rootCounted:
		return fx.root
	case rootPackedTree:
		return testLayout(t, fx.repo).planTree(fx.uuidBytes(t))
	case rootNotMountPoint, rootReadWrite:
		dir := filepath.Join(fx.work, "plain-copy")
		if out, err := exec.Command("cp", "-a", fx.root, dir).CombinedOutput(); err != nil {
			t.Fatalf("cp -a %s %s: %v: %s", fx.root, dir, err, out)
		}
		if kind == rootReadWrite {
			addFakeMount(t, dir, false)
		}
		return dir
	}
	t.Fatalf("no disc root of kind %d", kind)
	return ""
}

// objectsOnDisc counts the objects that pass the full check of root.
func objectsOnDisc(t *testing.T, root string) int {
	t.Helper()
	rr, err := image.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	return rr.ObjectsVerified
}
