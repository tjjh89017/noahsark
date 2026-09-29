package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
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
			absent: []string{gcFreedNone, "held", "skipped"},
			exact:  true, exactStderr: true,
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "53", name: "verified disc, too soon",
			start: stage.DiscVerified, args: []string{"gc"},
			exact: true, exactStderr: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "55", name: "packed disc",
			start: stage.DiscPacked, args: []string{"gc", "--force-after=0d"},
			exact: true, exactStderr: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "55", name: "burned disc",
			start: stage.DiscBurned, args: []string{"gc", "--force-after=0d"},
			exact: true, exactStderr: true,
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "55", name: "missing disc",
			start: stage.DiscMissing, args: []string{"gc", "--force-after=0d"},
			omit:  []string{"gc: disc SEQ: not verified; N item(s) held"},
			exact: true, exactStderr: true,
			end: stage.DiscMissing,
		},
		stateCase{
			row: "56", name: "lost disc",
			start: stage.DiscLost, args: []string{"gc", "--force-after=0d"},
			absent: []string{"gc: disc"},
			exact:  true, exactStderr: true,
			end: stage.DiscLost,
		},
		// Row 52 with no --force-after: gc runs eight days after the
		// verify, frees every item of the disc, and names the exact items
		// and bytes.
		stateCase{
			row: "52", name: "verified disc, eight days later",
			start: stage.DiscVerified, setup: gcClockSetup(8 * 24 * time.Hour),
			args:  []string{"gc"},
			exact: true, exactStderr: true,
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				layout := testLayout(t, fx.repo)
				if files := listFilesUnder(t, layout.chunksDir()); len(files) != 0 {
					t.Fatalf("staging chunks after gc: %v, want none", files)
				}
				if _, err := os.Lstat(layout.planDir(fx.uuidBytes(t))); !os.IsNotExist(err) {
					t.Fatalf("plan directory after gc: %v, want it removed", err)
				}
			},
		},
		// Row 53: gc runs one day after the verify, holds every item, and
		// names the local date at the end of the wait.
		stateCase{
			row: "53", name: "verified disc, one day later",
			start: stage.DiscVerified, setup: gcClockSetup(24 * time.Hour),
			args:  []string{"gc"},
			exact: true, exactStderr: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		// Row 54: the catalog tables of the disc are removed. gc skips
		// every item of the disc, keeps each chunk file, and exits 1.
		stateCase{
			row: "54", name: "no INDEX of the disc in the catalog",
			start: stage.DiscVerified, setup: gcRemoveTablesSetup,
			args:  []string{"gc", "--force-after=0d"},
			exact: true, exactStderr: true,
			end: stage.DiscVerified, word: stage.WordClean,
			check: chunkFilesKept,
		},
		// Row 54 with no catalog directory: gc does not create the
		// catalog directory again.
		stateCase{
			row: "54", name: "no catalog directory",
			start: stage.DiscVerified,
			setup: func(t *testing.T, fx *discFixture) {
				if err := os.RemoveAll(testLayout(t, fx.repo).catalogDir()); err != nil {
					t.Fatal(err)
				}
			},
			args:  []string{"gc", "--force-after=0d"},
			exact: true, exactStderr: true,
			end: stage.DiscVerified, word: stage.WordClean,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if _, err := os.Stat(testLayout(t, fx.repo).catalogDir()); !os.IsNotExist(err) {
					t.Fatalf("gc created the catalog directory: %v", err)
				}
			},
		},
		// Row 54a: the disc has one Packed item that its catalog INDEX
		// does not list. gc frees no item of the disc and appends no
		// Freed event.
		stateCase{
			row: "54a", name: "INDEX does not list an item",
			start: stage.DiscVerified,
			setup: func(t *testing.T, fx *discFixture) {
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
				gcCountChunks(t, fx)
			},
			args:  []string{"gc", "--force-after=0d"},
			cells: map[string]string{"N": "1"},
			exact: true, exactStderr: true,
			end: stage.DiscVerified, word: stage.WordClean,
			check: allChecks(chunkFilesKept, func(t *testing.T, fx *discFixture, _, _ string) {
				if n := countByState(t, fx.repo, stage.OnDisc); n != 0 {
					t.Fatalf("%d item(s) OnDisc, want none", n)
				}
			}),
		},
	)
}

// gcClockSetup sets the fake clock to the verified time of the disc of
// fx plus after. It sets the placeholders N and B of the cells to what
// gc can free, and DATE to the local date when the 7-day wait ends.
func gcClockSetup(after time.Duration) func(*testing.T, *discFixture) {
	return func(t *testing.T, fx *discFixture) {
		t.Helper()
		verified := discState(t, fx.repo, fx.uuid).VerifiedTime
		items, bytes := gcFreeableBytes(t, fx)
		if items == 0 || bytes == 0 {
			t.Fatalf("the fixture has %d item(s), %d bytes to free; want more", items, bytes)
		}
		fx.cell("N", strconv.Itoa(items))
		fx.cell("B", strconv.FormatUint(bytes, 10))
		fx.cell("DATE", verified.Add(7*24*time.Hour).Local().Format("2006-01-02"))
		setFakeNow(t, func() time.Time { return verified.Add(after) })
	}
}

// gcRemoveTablesSetup removes the catalog tables of the disc of fx. It
// sets the placeholder N of the cells to the items of the disc, and
// {CHUNKS} to the number of chunk files.
func gcRemoveTablesSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	items, _ := gcFreeableBytes(t, fx)
	fx.cell("N", strconv.Itoa(items))
	c, err := catalog.Open(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDisc(fx.uuidBytes(t)); err != nil {
		t.Fatal(err)
	}
	gcCountChunks(t, fx)
}

// gcCountChunks sets {CHUNKS} to the number of chunk files in staging.
func gcCountChunks(t *testing.T, fx *discFixture) {
	t.Helper()
	fx.set("{CHUNKS}", strconv.Itoa(len(listFilesUnder(t, testLayout(t, fx.repo).chunksDir()))))
}

// chunkFilesKept is a check: staging holds the {CHUNKS} chunk files
// that it held before gc.
func chunkFilesKept(t *testing.T, fx *discFixture, _, _ string) {
	t.Helper()
	after := strconv.Itoa(len(listFilesUnder(t, testLayout(t, fx.repo).chunksDir())))
	if after != fx.vars["{CHUNKS}"] {
		t.Fatalf("chunk files %s after gc, want the %s kept", after, fx.vars["{CHUNKS}"])
	}
}

// gcFreeableBytes sums the allocated blocks of the chunk files of the
// Packed items of the disc of fx and of the regular files of its plan
// directory: the bytes that row 52 frees.
func gcFreeableBytes(t *testing.T, fx *discFixture) (items int, bytes uint64) {
	t.Helper()
	layout := testLayout(t, fx.repo)
	for _, id := range readLogs(t, fx.repo).Items.ItemsOfDiscInState(fx.uuidBytes(t), stage.Packed) {
		items++
		bytes += allocatedBytesUnder(t, layout.chunkFile(id))
	}
	return items, bytes + allocatedBytesUnder(t, layout.planDir(fx.uuidBytes(t)))
}

// allocatedBytesUnder sums st_blocks * 512 of every regular file at or
// below path. It does not follow a symlink. A missing path counts 0.
func allocatedBytesUnder(t *testing.T, path string) uint64 {
	t.Helper()
	var total uint64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		var st syscall.Stat_t
		if err := syscall.Lstat(p, &st); err != nil {
			return err
		}
		total += uint64(st.Blocks) * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// TestGCDryRunSkipped removes the catalog tables of a verified disc and
// runs gc --dry-run. The state x event table has no row for a dry run.
// gc --dry-run exits 1 when gc would skip an item, and prints no next
// line.
func TestGCDryRunSkipped(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	gcRemoveTablesSetup(t, fx)
	te := newTestEnv(t.TempDir())
	code, _ := te.run("--repo="+fx.repo, "gc", "--dry-run", "--force-after=0d")
	want := "gc: would free 0 item(s), 0 bytes\n" +
		"gc: " + fx.cells["N"] + " item(s) skipped: disc 0's table is not in the catalog\n"
	if code != 1 || te.out.String() != want || te.errOut.String() != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 1, %q and no stderr", code, te.out.String(), te.errOut.String(), want)
	}
	chunkFilesKept(t, fx, "", "")
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
