package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// gcFreedNone is the freed line of a gc that frees nothing.
const gcFreedNone = "gc: freed 0 item(s), 0 bytes\n"

func init() {
	registerStateCases(
		stateCase{
			row: "52", name: "verified disc, wait over",
			start: stage.DiscVerified, args: []string{"gc", "--force-after=0d"},
			stdout: []string{"gc: freed "}, absent: []string{gcFreedNone, "held", "skipped"}, next: true,
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "53", name: "verified disc, too soon",
			start: stage.DiscVerified, args: []string{"gc"},
			stdout: []string{gcFreedNone, "gc: disc {SEQ}: too soon; "}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "55", name: "packed disc",
			start: stage.DiscPacked, args: []string{"gc", "--force-after=0d"},
			stdout: []string{gcFreedNone, "gc: disc {SEQ}: not verified; "}, next: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "55", name: "burned disc",
			start: stage.DiscBurned, args: []string{"gc", "--force-after=0d"},
			stdout: []string{gcFreedNone, "gc: disc {SEQ}: not verified; "}, next: true,
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "55", name: "missing disc",
			start: stage.DiscMissing, args: []string{"gc", "--force-after=0d"},
			stdout: []string{gcFreedNone}, absent: []string{"gc: disc"}, next: true,
			end: stage.DiscMissing,
		},
		stateCase{
			row: "56", name: "lost disc",
			start: stage.DiscLost, args: []string{"gc", "--force-after=0d"},
			stdout: []string{gcFreedNone}, absent: []string{"gc: disc"}, next: true,
			end: stage.DiscLost,
		},
	)
}

// gcFreeableBytes sums the chunk files of the Packed items of the disc
// of fx and the files of its plan directory: the bytes that row 52
// frees.
func gcFreeableBytes(t *testing.T, fx *discFixture) (items int, bytes uint64) {
	t.Helper()
	layout := testLayout(t, fx.repo)
	for _, id := range readLogs(t, fx.repo).Items.ItemsOfDiscInState(fx.uuidBytes(t), stage.Packed) {
		items++
		if fi, err := os.Stat(layout.chunkFile(id)); err == nil {
			bytes += uint64(fi.Size())
		}
	}
	dir, _ := dirBytes(layout.planDir(fx.uuidBytes(t)))
	return items, bytes + dir
}

// TestStatesRow52AfterSevenDays runs gc eight days after the verify, with
// no --force-after. gc frees every item of the disc, and names the exact
// items and bytes.
func TestStatesRow52AfterSevenDays(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	verified := discState(t, fx.repo, fx.uuid).VerifiedTime
	items, bytes := gcFreeableBytes(t, fx)
	if items == 0 || bytes == 0 {
		t.Fatalf("the fixture has %d item(s), %d bytes to free; want more", items, bytes)
	}
	setFakeNow(t, func() time.Time { return verified.Add(8 * 24 * time.Hour) })

	out := fx.mustRun(t, "gc")
	want := fmt.Sprintf("gc: freed %d item(s), %d bytes\nnext: noahsark status\n", items, bytes)
	if out != want {
		t.Fatalf("gc output %q, want %q", out, want)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscOnDiscOnly {
		t.Fatalf("disc state %s, want on disc only", got)
	}
	layout := testLayout(t, fx.repo)
	if files := listFilesUnder(t, layout.chunksDir()); len(files) != 0 {
		t.Fatalf("staging chunks after gc: %v, want none", files)
	}
	if _, err := os.Lstat(layout.planDir(fx.uuidBytes(t))); !os.IsNotExist(err) {
		t.Fatalf("plan directory after gc: %v, want it removed", err)
	}
}

// TestStatesRow53NamesTheDate runs gc one day after the verify. gc holds
// every item and names the local date at the end of the wait.
func TestStatesRow53NamesTheDate(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	verified := discState(t, fx.repo, fx.uuid).VerifiedTime
	items, _ := gcFreeableBytes(t, fx)
	setFakeNow(t, func() time.Time { return verified.Add(24 * time.Hour) })

	code, out := fx.run(t, "gc")
	until := verified.Add(7 * 24 * time.Hour).Local().Format("2006-01-02")
	want := fmt.Sprintf("gc: freed 0 item(s), 0 bytes\ngc: disc %d: too soon; %d item(s) held until %s\nnext: noahsark status\n", fx.seq, items, until)
	if code != 0 || out != want {
		t.Fatalf("gc: exit %d, output %q; want exit 0, output %q", code, out, want)
	}
}

// TestStatesRow54NoIndexInTheCatalog removes the catalog tables of a
// verified disc. gc skips every item of the disc and exits 1.
func TestStatesRow54NoIndexInTheCatalog(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	items, _ := gcFreeableBytes(t, fx)
	c, err := catalog.Open(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDisc(fx.uuidBytes(t)); err != nil {
		t.Fatal(err)
	}
	chunksBefore := listFilesUnder(t, testLayout(t, fx.repo).chunksDir())

	code, out := fx.run(t, "gc", "--force-after=0d")
	want := fmt.Sprintf("gc: freed 0 item(s), 0 bytes\ngc: %d item(s) skipped: disc %d's table is not in the catalog\nnext: noahsark status\n", items, fx.seq)
	if code != 1 || out != want {
		t.Fatalf("gc: exit %d, output %q; want exit 1, output %q", code, out, want)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscVerified {
		t.Fatalf("disc state %s, want verified", got)
	}
	if after := listFilesUnder(t, testLayout(t, fx.repo).chunksDir()); len(after) != len(chunksBefore) {
		t.Fatalf("chunk files %d after gc, want the %d kept", len(after), len(chunksBefore))
	}
}

// TestStatesRow54NoCatalogDirectory removes the whole catalog. gc skips
// the items, and does not create the catalog directory again.
func TestStatesRow54NoCatalogDirectory(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	catalogDir := testLayout(t, fx.repo).catalogDir()
	if err := os.RemoveAll(catalogDir); err != nil {
		t.Fatal(err)
	}
	code, out := fx.run(t, "gc", "--force-after=0d")
	if code != 1 || !strings.Contains(out, "skipped: disc 0's table is not in the catalog") {
		t.Fatalf("gc: exit %d, output %q; want exit 1 and the skip line", code, out)
	}
	if _, err := os.Stat(catalogDir); !os.IsNotExist(err) {
		t.Fatalf("gc created the catalog directory: %v", err)
	}
}

// TestStatesRow54aIndexDoesNotListAnItem gives a verified disc one Packed
// item that its catalog INDEX does not list. gc frees no item of the
// disc, appends no Freed event, reports the skip and exits 1.
func TestStatesRow54aIndexDoesNotListAnItem(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	extra := object.ComputeID(format.ObjectKindChunk, []byte("an item that the INDEX does not list"))
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.EnsureStaged(extra); err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkPacked(1, fx.uuidBytes(t), extra); err != nil {
		t.Fatal(err)
	}
	chunksBefore := listFilesUnder(t, testLayout(t, fx.repo).chunksDir())

	code, out := fx.run(t, "gc", "--force-after=0d")
	want := fmt.Sprintf("gc: freed 0 item(s), 0 bytes\ngc: 1 item(s) skipped: disc %d's table does not list them\nnext: noahsark status\n", fx.seq)
	if code != 1 || out != want {
		t.Fatalf("gc: exit %d, output %q; want exit 1, output %q", code, out, want)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscVerified {
		t.Fatalf("disc state %s, want verified: no Freed event", got)
	}
	if n := countByState(t, fx.repo, stage.OnDisc); n != 0 {
		t.Fatalf("%d item(s) OnDisc, want none", n)
	}
	if after := listFilesUnder(t, testLayout(t, fx.repo).chunksDir()); len(after) != len(chunksBefore) {
		t.Fatalf("chunk files %d after gc, want the %d kept", len(after), len(chunksBefore))
	}
}

// TestStatesRow54DryRunExitsOne checks that gc --dry-run exits 1 when gc
// would skip an item, and prints no next line.
func TestStatesRow54DryRunExitsOne(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	items, _ := gcFreeableBytes(t, fx)
	c, err := catalog.Open(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDisc(fx.uuidBytes(t)); err != nil {
		t.Fatal(err)
	}
	code, out := fx.run(t, "gc", "--dry-run", "--force-after=0d")
	want := fmt.Sprintf("gc: would free 0 item(s), 0 bytes\ngc: %d item(s) skipped: disc %d's table is not in the catalog\n", items, fx.seq)
	if code != 1 || out != want {
		t.Fatalf("gc --dry-run: exit %d, output %q; want exit 1, output %q", code, out, want)
	}
}

// TestStatesRow52PackOutRemovesTheSymlinkOnly frees a pack --out disc. gc
// removes the tree symlink of the plan directory and never touches the
// directory that the symlink names.
func TestStatesRow52PackOutRemovesTheSymlinkOnly(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	outDir := filepath.Join(work, "out")
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+outDir)
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	discUUID := packedDiscUUID(t, packOut)
	u, err := decodeUUID(strings.ReplaceAll(discUUID, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	tree := testLayout(t, repo).planTree(u)
	if fi, err := os.Lstat(tree); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("plan tree %s is not a symlink: %v", tree, err)
	}
	mounted := filepath.Join(work, "mounted")
	copyTree(t, outDir, mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	outFiles := listFilesUnder(t, outDir)

	if code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=0d"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatalf("plan tree symlink after gc: %v, want it removed", err)
	}
	if after := listFilesUnder(t, outDir); !slices.Equal(after, outFiles) {
		t.Fatalf("files of the --out directory after gc: %v, want %v", after, outFiles)
	}
}
