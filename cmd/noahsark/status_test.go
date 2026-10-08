package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// statusDiscLineRe matches one disc line of "status": the disc number,
// the label, the state with its suffix, and the uuid.
var statusDiscLineRe = regexp.MustCompile(`^disc (\d+) "([^"]*)"  ([a-z]+(?: [a-z]+)*(?:, [a-z0-9-]+(?: [a-z0-9-]+)*)?)  ([0-9a-f-]{36})$`)

// statusLines runs status in the repository repo and returns its lines.
func statusLines(t *testing.T, repo string) []string {
	t.Helper()
	code, out := runCmd(t, "--repo="+repo, "status")
	if code != 0 {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

// appendDiscEvent appends event for the disc of fx at the time at, as
// the command that writes the event would.
func appendDiscEvent(t *testing.T, fx *discFixture, event stage.DiscEvent, at time.Time) {
	t.Helper()
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	rec := stage.DiscRecord{TimeSec: at.Unix(), DiscUUID: fx.uuidBytes(t), Event: event}
	if err := logs.Discs.Append(rec); err != nil {
		t.Fatal(err)
	}
}

// statusImage is the image path of the disc of fx.
func statusImage(t *testing.T, fx *discFixture) string {
	t.Helper()
	return testLayout(t, fx.repo).planImage(fx.uuidBytes(t))
}

// wantStatusDate is the DATE of status for the time t.
func wantStatusDate(t time.Time) string { return t.Local().Format("2006-01-02") }

// TestStatusPackedBlock checks the whole output for a packed disc: the
// staged line, the disc line, the block with the image build line, and
// the folder burn alternative after it.
func TestStatusPackedBlock(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	tree := testLayout(t, fx.repo).planTree(fx.uuidBytes(t))
	want := []string{
		"staged: 0 items, 0 bytes",
		fmt.Sprintf("%s  packed  %s", fx.name(), fx.uuid),
		nextLoadBlankLine,
		"sudo noahsark --repo=" + fx.repo + " image build 0 &&",
		"growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=" + statusImage(t, fx) + " &&",
		"eject /dev/sr0 && eject -t /dev/sr0 &&",
		"sudo -v && " + statusMountLine + " &&",
		"noahsark verify /mnt/ark;",
		"sudo umount /mnt/ark; eject /dev/sr0",
		folderBurnHead,
		"noahsark verify " + tree + " &&",
		"growisofs -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK_0000 " + tree + " &&",
		"eject /dev/sr0 && eject -t /dev/sr0 &&",
		"sudo -v && " + statusMountLine + " &&",
		"noahsark verify /mnt/ark;",
		"sudo umount /mnt/ark; eject /dev/sr0",
	}
	if got := statusLines(t, fx.repo); !slices.Equal(got, want) {
		t.Fatalf("status:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestStatusPackedBlockWithImage checks that the block has no image
// build line when the image exists.
func TestStatusPackedBlockWithImage(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	if err := os.WriteFile(statusImage(t, fx), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	lines := statusLines(t, fx.repo)
	if lines[2] != nextLoadBlankLine || !strings.HasPrefix(lines[3], "growisofs ") {
		t.Fatalf("status = %q, want the growisofs line right after the first block line", lines)
	}
}

// TestStatusClosedDisc checks a disc packed with --close: the disc line
// has the state and the uuid, and both burn lines of the block seal the
// disc.
func TestStatusClosedDisc(t *testing.T) {
	repo, _ := initAndCommit(t)
	out := statusMustRun(t, "--repo="+repo, "pack", "--capacity=64MiB", "--close")
	uuid := packedDiscUUID(t, out)
	lines := statusLines(t, repo)
	m := statusDiscLineRe.FindStringSubmatch(lines[1])
	if m == nil || m[3] != "packed" || m[4] != uuid {
		t.Fatalf("disc line %q, want a packed disc %s", lines[1], uuid)
	}
	var burns []string
	for _, l := range lines {
		if strings.HasPrefix(l, "growisofs ") {
			burns = append(burns, l)
		}
	}
	if len(burns) != 2 ||
		!strings.HasPrefix(burns[0], "growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty -Z /dev/sr0=") ||
		!strings.HasPrefix(burns[1], "growisofs -dvd-compat -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK_0000 ") {
		t.Fatalf("burn lines %q, want the two sealing lines", burns)
	}
}

// statusMustRun runs the CLI with args and fails the test on an exit code
// other than 0.
func statusMustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out := runCmd(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d: %s", args, code, out)
	}
	return out
}

// TestStatusNoDiscRootBlock checks a packed disc whose disc root is
// gone: the block names disc lost.
func TestStatusNoDiscRootBlock(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	if err := os.RemoveAll(testLayout(t, fx.repo).planTree(fx.uuidBytes(t))); err != nil {
		t.Fatal(err)
	}
	lines := statusLines(t, fx.repo)
	want := []string{
		"next: disc 0 has no disc root; no new disc can be burned from it. Discard the disc, then run:",
		"noahsark disc lost 0",
	}
	if got := lines[2:]; !slices.Equal(got, want) {
		t.Fatalf("block %q, want %q", got, want)
	}
}

// TestStatusBurnedBlock checks the block of a burned disc.
func TestStatusBurnedBlock(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	want := []string{
		"staged: 0 items, 0 bytes",
		fmt.Sprintf("%s  burned  %s", fx.name(), fx.uuid),
		"next: load disc 0, then run:",
		statusMountLine + " &&",
		"noahsark verify /mnt/ark;",
		"sudo umount /mnt/ark; eject /dev/sr0",
	}
	if got := statusLines(t, fx.repo); !slices.Equal(got, want) {
		t.Fatalf("status:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestStatusBurnedLastCheckFailed checks a verified disc whose check
// then failed: it is burned with a failed last check, and its block
// burns a new disc from the kept disc root.
func TestStatusBurnedLastCheckFailed(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	at := time.Now()
	appendDiscEvent(t, fx, stage.EventCheckFailed, at)
	lines := statusLines(t, fx.repo)
	if want := fmt.Sprintf("%s  burned, last check failed %s  %s", fx.name(), wantStatusDate(at), fx.uuid); lines[1] != want {
		t.Fatalf("disc line %q, want %q", lines[1], want)
	}
	if lines[2] != nextLoadBlankLine || !slices.Contains(lines, folderBurnHead) {
		t.Fatalf("status = %q, want the packed block", lines)
	}
}

// TestStatusVerified checks a verified disc: the advice line, then the
// gc line, at once after the verify. With the image, the advice line
// names the image.
func TestStatusVerified(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	info := discState(t, fx.repo, fx.uuid)
	want := []string{
		"staged: 0 items, 0 bytes",
		fmt.Sprintf("%s  verified, last check %s  %s", fx.name(), wantStatusDate(info.LastCheckTime), fx.uuid),
		`advice: copy disc 0 before gc; see the guide, "A second copy"`,
		nextGCLine,
	}
	if got := statusLines(t, fx.repo); !slices.Equal(got, want) {
		t.Fatalf("status:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	img := statusImage(t, fx)
	if err := os.WriteFile(img, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	lines := statusLines(t, fx.repo)
	if want := `advice: burn a second copy of ` + img + ` before gc; see the guide, "A second copy"`; lines[2] != want {
		t.Fatalf("advice line %q, want %q", lines[2], want)
	}
}

// TestStatusNotChecked checks the suffix of a disc that disc verified
// marked: verified, not checked.
func TestStatusNotChecked(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	appendDiscEvent(t, fx, stage.EventMarkedVerified, time.Now())
	lines := statusLines(t, fx.repo)
	if want := fmt.Sprintf("%s  verified, not checked  %s", fx.name(), fx.uuid); lines[1] != want {
		t.Fatalf("disc line %q, want %q", lines[1], want)
	}
}

// TestStatusOnDiscOnlyFailed checks an on disc only disc whose last
// check failed: the disc line and the block that names disc lost and
// commit.
func TestStatusOnDiscOnlyFailed(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscOnDiscOnly)
	at := time.Now()
	appendDiscEvent(t, fx, stage.EventCheckFailed, at)
	want := []string{
		"staged: 0 items, 0 bytes",
		fmt.Sprintf("%s  on disc only, last check failed %s  %s", fx.name(), wantStatusDate(at), fx.uuid),
		`next: disc 0 failed its last check. Copy it now, or use your second copy; see the guide, "A second copy". When no copy can be read, run:`,
		"noahsark disc lost 0 && noahsark commit",
	}
	if got := statusLines(t, fx.repo); !slices.Equal(got, want) {
		t.Fatalf("status:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestStatusMissingBlock checks the whole block of a missing disc.
func TestStatusMissingBlock(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscMissing)
	cfg, err := readConfig(configPath(fx.repo))
	if err != nil {
		t.Fatal(err)
	}
	lines := statusLines(t, fx.repo)
	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "next:") })
	want := []string{
		fmt.Sprintf("next: load disc 0 %q, then run:", fx.label),
		statusMountLine + " &&",
		"noahsark recover --source=" + quoteShellWord(cfg.SourceRoot) + " --disc=/mnt/ark;",
		"sudo umount /mnt/ark; eject /dev/sr0",
		"or, when disc 0 is gone for good, run:",
		"noahsark disc lost 0",
	}
	if i < 0 || !slices.Equal(lines[i:], want) {
		t.Fatalf("status %q, want the block %q", lines, want)
	}
}

// TestStatusOnDiscOnlyAfterRecover recovers a repository from one disc
// and checks that the disc reads "on disc only" with no date, since no
// verify read it.
func TestStatusOnDiscOnlyAfterRecover(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	statusMustRun(t, "--repo="+repo, "commit", src)
	treeDir := filepath.Join(work, "tree")
	statusMustRun(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	addFakeMount(t, treeDir, true)
	statusMustRun(t, "--repo="+repo, "recover", "--source="+src, "--disc="+treeDir)

	lines := statusLines(t, repo)
	m := statusDiscLineRe.FindStringSubmatch(lines[1])
	if m == nil || m[3] != "on disc only" {
		t.Fatalf("disc line %q, want the plain word on disc only", lines[1])
	}
	if lines[len(lines)-1] != nextNothingLine {
		t.Fatalf("status %q, want next: nothing to do", lines)
	}
}

// TestStatusEmptyRepository checks status on a repository with no
// commit: the staged line and the commit line.
func TestStatusEmptyRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "status")
	if code != 0 || out != "staged: 0 items, 0 bytes\nnext: noahsark commit\n" {
		t.Fatalf("status: exit %d, output %q", code, out)
	}
}

// TestStatusStagedBlock checks the block after a commit with no pack:
// the snapshot line, the media line, and the pack line that the
// operator completes.
func TestStatusStagedBlock(t *testing.T) {
	repo, _ := initAndCommit(t)
	lines := statusLines(t, repo)
	if !strings.HasPrefix(lines[0], "staged: ") || strings.HasPrefix(lines[0], "staged: 0 items") {
		t.Fatalf("staged line %q, want a count", lines[0])
	}
	if !statusSnapshotLineRe.MatchString(lines[1]) {
		t.Fatalf("line %q, want the snapshot line", lines[1])
	}
	want := []string{
		nextLoadBlankLine,
		"dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks'",
		"then paste this line, type the capacity, and press Enter:",
		"noahsark pack --capacity=",
	}
	if got := lines[2:]; !slices.Equal(got, want) {
		t.Fatalf("block %q, want %q", got, want)
	}
}

// TestStatusQuietPrintsTheSame checks that -q changes nothing in the
// output of status: status prints no progress line.
func TestStatusQuietPrintsTheSame(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	_, plain := runCmd(t, "--repo="+fx.repo, "status")
	_, quiet := runCmd(t, "--repo="+fx.repo, "-q", "status")
	if plain != quiet {
		t.Fatalf("status -q:\n%s\nwant:\n%s", quiet, plain)
	}
}

// treeEntry is one file or directory of a tree: its type, size and
// modification time.
type treeEntry struct {
	mode  fs.FileMode
	size  int64
	mtime time.Time
}

// treeSnapshot lists every file and directory under root, without a
// follow of a symlink.
func treeSnapshot(t *testing.T, root string) map[string]treeEntry {
	t.Helper()
	out := map[string]treeEntry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = treeEntry{mode: fi.Mode(), size: fi.Size(), mtime: fi.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStatusChangesNoFile lists every file and directory of the work
// tree, with its size and modification time, before and after status,
// in each state. The two lists must be equal.
func TestStatusChangesNoFile(t *testing.T) {
	t.Run("empty repository", func(t *testing.T) {
		work := t.TempDir()
		repo := filepath.Join(work, "repo")
		if code, out := runIn(t, repo, "init"); code != 0 {
			t.Fatalf("init: exit %d: %s", code, out)
		}
		statusKeepsTree(t, work, repo)
	})
	t.Run("no catalog directory", func(t *testing.T) {
		work := t.TempDir()
		repo := filepath.Join(work, "repo")
		if code, out := runIn(t, repo, "init"); code != 0 {
			t.Fatalf("init: exit %d: %s", code, out)
		}
		if err := os.RemoveAll(filepath.Join(repo, "catalog")); err != nil {
			t.Fatal(err)
		}
		statusKeepsTree(t, work, repo)
	})
	t.Run("staged", func(t *testing.T) {
		repo, _ := initAndCommit(t)
		statusKeepsTree(t, filepath.Dir(repo), repo)
	})
	for _, state := range []stage.DiscState{
		stage.DiscPacked, stage.DiscBurned, stage.DiscVerified,
		stage.DiscOnDiscOnly, stage.DiscLost, stage.DiscMissing,
	} {
		t.Run(state.String(), func(t *testing.T) {
			fx := repoWithDisc(t, state)
			statusKeepsTree(t, fx.work, fx.repo)
		})
	}
}

// statusKeepsTree runs status in repo and compares the tree under work
// before and after.
func statusKeepsTree(t *testing.T, work, repo string) {
	t.Helper()
	before := treeSnapshot(t, work)
	if code, out := runCmd(t, "--repo="+repo, "status"); code != 0 {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	after := treeSnapshot(t, work)
	for path, b := range before {
		a, ok := after[path]
		if !ok {
			t.Errorf("status removed %s", path)
		} else if a != b {
			t.Errorf("status changed %s: %+v, was %+v", path, a, b)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("status created %s", path)
		}
	}
}

// TestNextBlockOrder checks the order of the steps of the next block,
// and the lowest number inside one step.
func TestNextBlockOrder(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	disc := func(arg string, state stage.DiscState) nextDisc {
		return nextDisc{arg: arg, label: "L" + arg, treeExists: true, imageExists: true, image: "/img" + arg,
			info: stage.DiscInfo{State: state, VerifiedTime: now.Add(-time.Hour)}}
	}
	failed := func(d nextDisc) nextDisc {
		d.info.LastCheck = stage.CheckResultFailed
		return d
	}
	lostFrom := func(arg string, before stage.DiscState) nextDisc {
		d := disc(arg, stage.DiscLost)
		d.info.BeforeLost, d.info.LastEvent, d.info.LastEventTime = before, stage.EventLost, now.Add(-time.Minute)
		return d
	}
	found := func(d nextDisc) nextDisc {
		d.info.LastEvent = stage.EventLostUndone
		return d
	}
	cases := []struct {
		name     string
		staged   int
		discs    []nextDisc
		repairs  []nextRepair
		noStage  bool
		snapshot time.Time
		first    string
	}{
		{name: "staging directory before repair", staged: 1, discs: []nextDisc{disc("1", stage.DiscPacked)}, repairs: []nextRepair{{arg: "1", command: "gc"}}, noStage: true,
			first: "next: staging directory /r/staging does not exist."},
		{"repair before missing", 1, []nextDisc{disc("1", stage.DiscMissing), disc("2", stage.DiscLost)}, []nextRepair{{arg: "2", command: "disc lost"}}, false, time.Time{},
			"next: disc 2: an earlier disc lost stopped before it wrote the records of its items; gc writes them"},
		{"repair instead of nothing", 0, []nextDisc{disc("1", stage.DiscOnDiscOnly)}, []nextRepair{{arg: "1", command: "gc"}}, false, time.Time{},
			"next: disc 1: an earlier gc stopped before it wrote the records of its items; gc writes them"},
		{name: "on disc only failed before a lost disc", staged: 1, discs: []nextDisc{lostFrom("1", stage.DiscOnDiscOnly), failed(disc("2", stage.DiscOnDiscOnly))}, snapshot: now.Add(-time.Hour),
			first: "next: disc 2 failed its last check."},
		{name: "a lost disc waits for a commit before packed", staged: 1, discs: []nextDisc{disc("1", stage.DiscPacked), lostFrom("2", stage.DiscMissing)}, snapshot: now.Add(-time.Hour),
			first: "next: disc 2 is lost; a new commit stages what the source still holds; run:"},
		{name: "a commit in the second of the lost event ends the wait", discs: []nextDisc{lostFrom("1", stage.DiscOnDiscOnly)}, snapshot: now.Add(-time.Minute),
			first: nextNothingLine},
		{name: "a verified disc marked lost waits for no commit", discs: []nextDisc{lostFrom("1", stage.DiscVerified)}, snapshot: now.Add(-time.Hour),
			first: nextNothingLine},
		{name: "a found on disc only disc before gc", staged: 1, discs: []nextDisc{disc("1", stage.DiscVerified), found(disc("2", stage.DiscOnDiscOnly))},
			first: "next: load disc 2, then run:"},
		{name: "a found on disc only disc after a failed check", discs: []nextDisc{failed(found(disc("1", stage.DiscOnDiscOnly)))},
			first: "next: disc 1 failed its last check."},
		{"missing first", 1, []nextDisc{failed(disc("1", stage.DiscOnDiscOnly)), disc("2", stage.DiscPacked), disc("3", stage.DiscMissing)}, nil, false, time.Time{},
			`next: load disc 3 "L3", then run:`},
		{"on disc only failed before packed", 1, []nextDisc{disc("1", stage.DiscPacked), failed(disc("2", stage.DiscOnDiscOnly))}, nil, false, time.Time{},
			"next: disc 2 failed its last check."},
		{"lowest number of packed and burned", 1, []nextDisc{disc("1", stage.DiscVerified), disc("2", stage.DiscBurned), disc("3", stage.DiscPacked)}, nil, false, time.Time{},
			"next: load disc 2, then run:"},
		{"gc before staged", 1, []nextDisc{disc("1", stage.DiscVerified)}, nil, false, time.Time{},
			nextGCLine},
		{"staged", 1, []nextDisc{disc("1", stage.DiscOnDiscOnly), disc("2", stage.DiscLost)}, nil, false, time.Time{},
			nextLoadBlankLine},
		{"nothing", 0, []nextDisc{disc("1", stage.DiscOnDiscOnly), disc("2", stage.DiscLost)}, nil, false, time.Time{},
			nextNothingLine},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines := nextBlock(nextRepo{repo: "/r", staging: "/r/staging", stagingMissing: c.noStage, device: "/dev/sr0", source: "/src",
				staged: c.staged, discs: c.discs, repairs: c.repairs, newestSnapshot: c.snapshot})
			if !strings.HasPrefix(lines[0], c.first) {
				t.Fatalf("block %q, want the first line %q", lines, c.first)
			}
		})
	}
}

// TestNextBlockNoSnapshot checks that a repository with no snapshot and
// nothing staged gets the commit line, and that a lost disc that waits
// for a commit still comes first.
func TestNextBlockNoSnapshot(t *testing.T) {
	if got := nextBlock(nextRepo{noSnapshot: true}); !slices.Equal(got, []string{nextCommitLine}) {
		t.Fatalf("block %q, want the commit line", got)
	}
	lost := nextDisc{arg: "1", info: stage.DiscInfo{State: stage.DiscLost, BeforeLost: stage.DiscMissing, LastEvent: stage.EventLost, LastEventTime: time.Now()}}
	if got := nextBlock(nextRepo{noSnapshot: true, discs: []nextDisc{lost}}); got[0] != "next: disc 1 is lost; a new commit stages what the source still holds; run:" {
		t.Fatalf("block %q, want the block of the lost disc", got)
	}
}

// TestAdviceLinesForEachVerifiedDisc checks that each verified disc gets
// one advice line, in the order of the numbers, and that the advice
// lines do not depend on the block: a packed disc wins the block.
func TestAdviceLinesForEachVerifiedDisc(t *testing.T) {
	r := nextRepo{device: "/dev/sr0", discs: []nextDisc{
		{arg: "1", image: "/img1", imageExists: true, treeExists: true, info: stage.DiscInfo{State: stage.DiscVerified}},
		{arg: "2", image: "/img2", imageExists: true, treeExists: true, info: stage.DiscInfo{State: stage.DiscPacked}},
		{arg: "3", image: "/img3", treeExists: true, info: stage.DiscInfo{State: stage.DiscVerified}},
		{arg: "4", info: stage.DiscInfo{State: stage.DiscOnDiscOnly}},
	}}
	want := []string{
		`advice: burn a second copy of /img1 before gc; see the guide, "A second copy"`,
		`advice: copy disc 3 before gc; see the guide, "A second copy"`,
	}
	if got := adviceLines(r); !slices.Equal(got, want) {
		t.Fatalf("advice lines %q, want %q", got, want)
	}
	if got := nextBlock(r); got[0] != nextLoadBlankLine {
		t.Fatalf("block %q, want the block of the packed disc", got)
	}
}

// TestStatusAdviceBeforeAnotherBlock verifies one disc, then packs a
// second disc. status prints the advice line of the verified disc before
// the block of the packed disc.
func TestStatusAdviceBeforeAnotherBlock(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	if err := os.WriteFile(filepath.Join(fx.src, "new.txt"), []byte("a file of the next disc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "commit", fx.src)
	fx.mustRun(t, "pack", "--capacity=64MiB")
	lines := statusLines(t, fx.repo)
	i := slices.Index(lines, `advice: copy disc 0 before gc; see the guide, "A second copy"`)
	if i < 0 || lines[i+1] != nextLoadBlankLine {
		t.Fatalf("status %q, want the advice line of disc 0 right before the block of disc 1", lines)
	}
}

// TestStatusFolderBurnOfPackOut checks the folder burn alternative of a
// pack --out disc: it names the --out directory, the target of the
// symlink of the plan directory.
func TestStatusFolderBurnOfPackOut(t *testing.T) {
	repo, _ := initAndCommit(t)
	outDir := filepath.Join(filepath.Dir(repo), "out dir")
	statusMustRun(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+outDir)
	lines := statusLines(t, repo)
	want := []string{
		"noahsark verify '" + outDir + "' &&",
		"growisofs -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK_0000 '" + outDir + "' &&",
	}
	i := slices.Index(lines, folderBurnHead)
	if i < 0 || !slices.Equal(lines[i+1:i+3], want) {
		t.Fatalf("status %q, want after the folder burn line %q", lines, want)
	}
}

// TestNextBlockQuotesPaths checks that a path with a space is one shell
// word in a command line.
func TestNextBlockQuotesPaths(t *testing.T) {
	d := nextDisc{arg: "4", image: "/my repo/it's.img", treeExists: true,
		info: stage.DiscInfo{State: stage.DiscPacked}}
	lines := nextBlock(nextRepo{repo: "/my repo", device: "/dev/sr0", discs: []nextDisc{d}})
	if want := "sudo noahsark --repo='/my repo' image build 4 &&"; lines[1] != want {
		t.Fatalf("image build line %q, want %q", lines[1], want)
	}
	if want := `growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z '/dev/sr0=/my repo/it'\''s.img' &&`; lines[2] != want {
		t.Fatalf("burn line %q, want %q", lines[2], want)
	}
}

// TestNextDiscsNamesAnAmbiguousNumberByUUID checks that two discs with
// the same number are named by their full uuids.
func TestNextDiscsNamesAnAmbiguousNumberByUUID(t *testing.T) {
	layout := repoLayout{repo: t.TempDir(), staging: t.TempDir()}
	a := "00000000-0000-4000-8000-000000000001"
	b := "00000000-0000-4000-8000-000000000002"
	discs := []discSummary{
		{UUID: a, Seq: 0, Info: stage.DiscInfo{UUID: [16]byte{15: 1}}},
		{UUID: b, Seq: 0, Info: stage.DiscInfo{UUID: [16]byte{15: 2}}},
		{UUID: "00000000-0000-4000-8000-000000000003", Seq: 1},
	}
	got := nextDiscs(layout, discs)
	if got[0].arg != a || got[1].arg != b || got[2].arg != "1" {
		t.Fatalf("args %q %q %q, want %q %q 1", got[0].arg, got[1].arg, got[2].arg, a, b)
	}
}

// TestStatusNamesARepair stops a command after its disc event. status
// names the disc and the stopped command as the first step, and never
// prints next: nothing to do. It writes no record: a second status prints
// the same block.
func TestStatusNamesARepair(t *testing.T) {
	cases := []struct {
		name  string
		start stage.DiscState
		event stage.DiscEvent
		cmd   string
	}{
		{"disc lost", stage.DiscVerified, stage.EventLost, "disc lost"},
		{"pack --undo", stage.DiscPacked, stage.EventPackUndone, "pack --undo"},
		{"gc", stage.DiscVerified, stage.EventFreed, "gc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := repoWithDisc(t, c.start)
			appendEventOnly(t, fx, c.event)
			want := []string{
				"next: disc 0: an earlier " + c.cmd + " stopped before it wrote the records of its items; gc writes them, and also frees the data of each verified disc; run:",
				"noahsark gc",
			}
			for range 2 {
				lines := statusLines(t, fx.repo)
				if got := lines[len(lines)-2:]; !slices.Equal(got, want) {
					t.Fatalf("status:\n%s\nwant the last lines:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
				}
			}
			// gc wrote the item records, thus it ends with the next block.
			wantNextBlock(t, fx.repo, fx.mustRun(t, "gc"))
			for _, line := range statusLines(t, fx.repo) {
				if strings.Contains(line, "stopped before it wrote") {
					t.Fatalf("status after gc still names the repair: %s", line)
				}
			}
		})
	}
}

// TestNextRepairsNamesAnAmbiguousNumberByUUID checks the disc argument
// of a repair: the number of its ledger row, the full uuid when another
// disc has the same number, and the full uuid with no ledger row.
func TestNextRepairsNamesAnAmbiguousNumberByUUID(t *testing.T) {
	a, b, c := [16]byte{15: 1}, [16]byte{15: 2}, [16]byte{15: 3}
	rows := []format.DiscsRow{{DiscUUID: a, DiscSeq: 0}, {DiscUUID: b, DiscSeq: 0}, {DiscUUID: c, DiscSeq: 1}}
	discs := []discSummary{{Seq: 0, Info: stage.DiscInfo{UUID: a}}, {Seq: 0, Info: stage.DiscInfo{UUID: b}}, {Seq: 1, Info: stage.DiscInfo{UUID: c}}}
	d := [16]byte{15: 4}
	got := nextRepairs(rows, discs, []stage.Repair{
		{Disc: stage.DiscInfo{UUID: a}, Command: "disc lost"},
		{Disc: stage.DiscInfo{UUID: c}, Command: "gc"},
		{Disc: stage.DiscInfo{UUID: d}, Command: "pack --undo"},
	})
	want := []nextRepair{{uuidText(a), "disc lost"}, {"1", "gc"}, {uuidText(d), "pack --undo"}}
	if !slices.Equal(got, want) {
		t.Fatalf("repairs %+v, want %+v", got, want)
	}
}

// TestStatusRunsWhileRepoLockHeld checks that status takes no lock: it
// runs while a writer holds the lock.
func TestStatusRunsWhileRepoLockHeld(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	held, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	code, out := runCmd(t, "--repo="+repo, "status")
	if code != 0 {
		t.Fatalf("status while a writer holds the lock: exit %d, want 0: %s", code, out)
	}
}

// TestStatusUsageErrorsExitTwo checks that a positional argument is a
// usage error.
func TestStatusUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "status", "extra"); code != 2 {
		t.Fatalf("status extra: exit %d, want 2: %s", code, out)
	}
}

// statusSnapshotLineRe matches one snapshot line of "status": the short
// snapshot id and the count of its staged items.
var statusSnapshotLineRe = regexp.MustCompile(`^snapshot ([0-9a-f]{12}): (\d+) items staged, not complete on discs; its snapshot object is not on a disc yet$`)

// TestStatusNamesASnapshotPackedInParts packs a part of a snapshot. status
// prints its snapshot line after the staged line and before the disc
// line, with the count of the staged items. After a pack of the rest,
// status prints no snapshot line.
func TestStatusNamesASnapshotPackedInParts(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", writeSeededSource(t, 42, 6))
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)
	packPart(t, repo, filepath.Join(work, "disc0"))

	lines := statusLines(t, repo)
	if len(lines) < 5 {
		t.Fatalf("status lines %q, want the staged, snapshot and disc lines, then a block", lines)
	}
	m := statusSnapshotLineRe.FindStringSubmatch(lines[1])
	if !strings.HasPrefix(lines[0], "staged: ") || m == nil || !statusDiscLineRe.MatchString(lines[2]) {
		t.Fatalf("status lines %q, want the staged line, one snapshot line, then the disc line", lines)
	}
	if m[1] != snapID[4:16] {
		t.Fatalf("snapshot line names %s, want %s", m[1], snapID[4:16])
	}
	if want := fmt.Sprint(countByState(t, repo, stage.Staged)); m[2] != want {
		t.Fatalf("snapshot line counts %s items, want %s", m[2], want)
	}
	if lines[3] != nextLoadBlankLine || !strings.HasPrefix(lines[4], "sudo noahsark ") {
		t.Fatalf("status lines %q, want the block of the packed disc", lines)
	}

	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+filepath.Join(work, "disc1")); code != 0 {
		t.Fatalf("pack of the rest: exit %d: %s", code, out)
	}
	for _, line := range statusLines(t, repo) {
		if strings.HasPrefix(line, "snapshot ") {
			t.Fatalf("status prints %q after the rest is packed", line)
		}
	}
}

// TestStatusNamesEachStagedSnapshot commits two snapshots that share no
// item and packs nothing. status prints one line for each. The two
// counts add up to the staged total.
func TestStatusNamesEachStagedSnapshot(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", writeSeededSource(t, 6, 1))
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	first := snapshotIDFromCommit(t, out)
	code, out = runCmd(t, "--repo="+repo, "commit", writeSeededSource(t, 7, 1))
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	second := snapshotIDFromCommit(t, out)

	var ids []string
	total := 0
	for _, line := range statusLines(t, repo) {
		if m := statusSnapshotLineRe.FindStringSubmatch(line); m != nil {
			ids = append(ids, m[1])
			n, _ := strconv.Atoi(m[2])
			total += n
		}
	}
	slices.Sort(ids)
	want := []string{first[4:16], second[4:16]}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("snapshot lines name %v, want %v", ids, want)
	}
	if want := countByState(t, repo, stage.Staged); total != want {
		t.Fatalf("the snapshot lines count %d items, want the %d staged items", total, want)
	}
}

// TestPrintNextPointsToStatusWhenTheReadFails checks that a command
// whose repository does not read for the next block points to status.
func TestPrintNextPointsToStatusWhenTheReadFails(t *testing.T) {
	te := newTestEnv(t.TempDir())
	printNext(te.env, filepath.Join(t.TempDir(), "no-repo"))
	if got, want := te.out.String(), nextStatusLine+"\n"; got != want {
		t.Fatalf("stdout %q, want %q", got, want)
	}
}
