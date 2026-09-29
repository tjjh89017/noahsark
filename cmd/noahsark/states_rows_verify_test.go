package main

import (
	"os/exec"
	"path/filepath"
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

// verifyRootSetup points {ROOT} to the disc root of kind, and damages it
// when damaged is true. For a good disc root, {N} is the number of
// objects that pass the full check.
func verifyRootSetup(kind verifyRootKind, damaged bool) func(*testing.T, *discFixture) {
	return func(t *testing.T, fx *discFixture) {
		t.Helper()
		fx.root = verifyRoot(t, fx, kind)
		if damaged {
			corruptDiscRoot(t, fx.root)
			return
		}
		fx.set("{N}", strconv.Itoa(objectsOnDisc(t, fx.root)))
	}
}

// verifyFailDetail is a check: standard error starts with the detail of
// the failed check.
func verifyFailDetail(t *testing.T, _ *discFixture, _, stderr string) {
	t.Helper()
	if !strings.HasPrefix(stderr, "noahsark: verify: ") {
		t.Errorf("stderr %q, want the detail of the failed check", stderr)
	}
}

// noRepoDisc is the disc name of verify with no repository.
const noRepoDisc = `disc {UUID} "{LABEL}"`

func init() {
	ok := func(disc string) string { return disc + ": {N} items, ok\n" }
	bad := func(disc, reason string) string { return disc + ": bad; " + reason + "\n" }
	notCounted := notCountedDisc + "\n"
	registerStateCases(
		// Row 38: a good disc root that is not a counted mount. Nothing
		// is written.
		stateCase{
			row: "38", name: "packed tree of a packed disc",
			start: stage.DiscPacked, setup: verifyRootSetup(rootPackedTree, false),
			args:   []string{"verify", "{ROOT}"},
			stdout: []string{ok("{DISC}"), notCounted}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked, check: lastCheckIs(stage.CheckResultNone),
		},
		stateCase{
			row: "38", name: "not a mount point, burned disc",
			start: stage.DiscBurned, setup: verifyRootSetup(rootNotMountPoint, false),
			args:   []string{"verify", "{ROOT}"},
			stdout: []string{ok("{DISC}"), notCounted}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscBurned, word: stage.WordBurned, check: lastCheckIs(stage.CheckResultNone),
		},
		stateCase{
			row: "38", name: "read-write mount, verified disc",
			start: stage.DiscVerified, setup: verifyRootSetup(rootReadWrite, false),
			args:   []string{"verify", "{ROOT}"},
			stdout: []string{ok("{DISC}"), notCounted}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscVerified, word: stage.WordClean, check: lastCheckIs(stage.CheckResultOK),
		},
		stateCase{
			row: "38", name: "packed tree with --no-mark",
			start: stage.DiscPacked, setup: verifyRootSetup(rootPackedTree, false),
			args:   []string{"verify", "--no-mark", "{ROOT}"},
			stdout: []string{ok("{DISC}"), notCounted}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked, check: lastCheckIs(stage.CheckResultNone),
		},
		// Row 38a: a good disc root with no repository.
		stateCase{
			row: "38a", name: "no repository",
			start: stage.DiscVerified, setup: verifyRootSetup(rootCounted, false), noRepo: true,
			args:   []string{"verify", "{ROOT}"},
			stdout: []string{ok(noRepoDisc), notCountedNoRepo + "\n"}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscVerified, word: stage.WordClean, check: lastCheckIs(stage.CheckResultOK),
		},
		// Row 38b: a damaged disc root that is not a counted mount. No
		// record is removed and no verify log event is added.
		stateCase{
			row: "38b", name: "damaged, not a mount point, verified disc",
			start: stage.DiscVerified, setup: verifyRootSetup(rootNotMountPoint, true),
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", reasonDiscRootDamaged), notCounted}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscVerified, word: stage.WordClean,
			check: allChecks(lastCheckIs(stage.CheckResultOK), verifyFailDetail),
		},
		stateCase{
			row: "38b", name: "damaged, read-write mount, on disc only disc",
			start: stage.DiscOnDiscOnly, setup: verifyRootSetup(rootReadWrite, true),
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", reasonDiscRootDamaged), notCounted}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
			check: allChecks(lastCheckIs(stage.CheckResultOK), verifyFailDetail),
		},
		// Row 38c: a damaged disc root with no repository.
		stateCase{
			row: "38c", name: "damaged, no repository",
			start: stage.DiscVerified, setup: verifyRootSetup(rootCounted, true), noRepo: true,
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad(noRepoDisc, reasonDiscRootDamaged), notCountedNoRepo + "\n"}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscVerified, word: stage.WordClean,
			check: allChecks(lastCheckIs(stage.CheckResultOK), verifyFailDetail),
		},
		// Row 39: a damaged packed tree.
		stateCase{
			row: "39", name: "damaged packed tree",
			start: stage.DiscPacked, setup: verifyRootSetup(rootPackedTree, true),
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", reasonPackedTreeDamaged), notCounted}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
			check: allChecks(lastCheckIs(stage.CheckResultNone), verifyFailDetail),
		},
		// Rows 40 to 43: a damaged counted disc. A failed check is logged,
		// and the disc loses one record where it has one.
		stateCase{
			row: "40", name: "damaged packed disc",
			start: stage.DiscPacked, setup: verifyRootSetup(rootCounted, true),
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", "this disc is bad; no record to remove")}, exact: true, next: true,
			end: stage.DiscPacked, word: stage.WordPacked,
			check: allChecks(lastCheckIs(stage.CheckResultFailed), verifyFailDetail),
		},
		stateCase{
			row: "41", name: "damaged burned disc",
			start: stage.DiscBurned, setup: verifyRootSetup(rootCounted, true),
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", "this disc is bad; burn record removed")}, exact: true, next: true,
			end: stage.DiscPacked, word: stage.WordPacked,
			check: allChecks(lastCheckIs(stage.CheckResultFailed), verifyFailDetail),
		},
		stateCase{
			row: "42", name: "damaged verified disc",
			start: stage.DiscVerified, setup: verifyRootSetup(rootCounted, true),
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", "this disc is bad; verified record removed; gc holds the data")}, exact: true, next: true,
			end: stage.DiscBurned, word: stage.WordBurned,
			check: allChecks(lastCheckIs(stage.CheckResultFailed), verifyFailDetail),
		},
		stateCase{
			row: "43", name: "damaged on disc only disc",
			start: stage.DiscOnDiscOnly, setup: verifyRootSetup(rootCounted, true),
			args: []string{"verify", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", "the staged copy is already freed; copy this disc now, or use your second copy, or run: noahsark disc lost {SEQ}")}, exact: true, next: true,
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
			check: allChecks(lastCheckIs(stage.CheckResultFailed), verifyFailDetail),
		},
		// Row 37 with a damaged disc: --no-mark writes nothing.
		stateCase{
			row: "37", name: "damaged verified disc with --no-mark",
			start: stage.DiscVerified, setup: verifyRootSetup(rootCounted, true),
			args: []string{"verify", "--no-mark", "{ROOT}"}, exit: 1,
			stdout: []string{bad("{DISC}", reasonDiscRootDamaged), notMarked + "\n"}, exact: true, noEvent: true, sameCatalog: true,
			end: stage.DiscVerified, word: stage.WordClean,
			check: allChecks(lastCheckIs(stage.CheckResultOK), verifyFailDetail),
		},
	)
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
