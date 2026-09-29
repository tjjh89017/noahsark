package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// fakeMount is one entry of the fake mount table. An empty root is "/",
// and an empty fsType is "udf".
type fakeMount struct {
	readOnly bool
	device   devNum
	root     string
	fsType   string
}

// fakeRootDevice is the device of the fake root filesystem: every path
// that is not under a fake mount is on it.
var fakeRootDevice = devNum{major: 8, minor: 1}

// fakeMounts is the fake mount table of every fake env, by mount point.
// Each entry has a loop device of its own.
var (
	fakeMountsMu  sync.Mutex
	fakeMounts    = map[string]fakeMount{}
	fakeNextMinor uint32
)

// addFakeMount lists dir in the fake mount table until the test ends. A
// loop mount of an image stands behind each entry.
func addFakeMount(t *testing.T, dir string, readOnly bool) {
	t.Helper()
	addFakeMountEntry(t, dir, fakeMount{readOnly: readOnly})
}

// addFakeMountEntry lists dir in the fake mount table with the fields of
// m until the test ends. It gives the entry a loop device of its own.
func addFakeMountEntry(t *testing.T, dir string, m fakeMount) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	fakeMountsMu.Lock()
	defer fakeMountsMu.Unlock()
	fakeNextMinor++
	m.device = devNum{major: 7, minor: fakeNextMinor}
	if m.root == "" {
		m.root = "/"
	}
	if m.fsType == "" {
		m.fsType = "udf"
	}
	fakeMounts[resolved] = m
	t.Cleanup(func() {
		fakeMountsMu.Lock()
		defer fakeMountsMu.Unlock()
		delete(fakeMounts, resolved)
	})
}

// fakeMountinfo returns the fake mount table in the format of
// /proc/self/mountinfo.
func fakeMountinfo() (io.ReadCloser, error) {
	fakeMountsMu.Lock()
	defer fakeMountsMu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "22 1 %s / / rw,relatime shared:1 - ext4 /dev/sda1 rw\n", fakeRootDevice)
	id := 100
	for dir, m := range fakeMounts {
		opt := "rw"
		if m.readOnly {
			opt = "ro"
		}
		fmt.Fprintf(&b, "%d 22 %s %s %s %s,relatime shared:%d - %s /dev/loop%d %s\n",
			id, m.device, m.root, strings.ReplaceAll(dir, " ", `\040`), opt, id, m.fsType, m.device.minor, opt)
		id++
	}
	return io.NopCloser(strings.NewReader(b.String())), nil
}

// fakeDeviceOf returns the device of path in the fake mount table: the
// device of the deepest fake mount that holds path, else the device of
// the fake root filesystem. It fails as a stat does for a missing path.
func fakeDeviceOf(path string) (devNum, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return devNum{}, err
	}
	fakeMountsMu.Lock()
	defer fakeMountsMu.Unlock()
	dev, depth := fakeRootDevice, -1
	for dir, m := range fakeMounts {
		if isWithin(resolved, dir) && len(dir) > depth {
			dev, depth = m.device, len(dir)
		}
	}
	return dev, nil
}

// discLogBytes returns the bytes of the disc state log of repo.
func discLogBytes(t *testing.T, repo string) []byte {
	t.Helper()
	data, err := os.ReadFile(testLayout(t, repo).discLogFile())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return data
}

// corruptDiscRoot flips one byte in the payload of a chunk of the disc
// root root, and returns a function that flips it back.
func corruptDiscRoot(t *testing.T, root string) func() {
	t.Helper()
	base, err := image.FindNoahsark(root, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	chunkPath := findAChunkFile(t, base)
	flipByte(t, chunkPath, 70) // inside the payload, past the header
	return func() { flipByte(t, chunkPath, 70) }
}

// TestVerifyRecordsTheCheck verifies the disc of each disc state, once
// with a good disc root and once with a damaged one. A good check
// records BurnRecorded then CheckOK for a packed disc, and CheckOK for
// every other state. A failed check records CheckFailed, and the disc
// goes one step down. A lost or a missing disc is refused.
func TestVerifyRecordsTheCheck(t *testing.T) {
	cases := []struct {
		name     string
		start    stage.DiscState
		damaged  bool
		exit     int
		lines    []string
		want     stage.DiscState
		wantLast stage.CheckResult
	}{
		{"packed, good", stage.DiscPacked, false, 0, []string{"burn recorded; verified", nextStatusLine}, stage.DiscVerified, stage.CheckResultOK},
		{"burned, good", stage.DiscBurned, false, 0, []string{"verified", nextStatusLine}, stage.DiscVerified, stage.CheckResultOK},
		{"verified, good", stage.DiscVerified, false, 0, []string{"already verified; check logged", nextStatusLine}, stage.DiscVerified, stage.CheckResultOK},
		{"on disc only, good", stage.DiscOnDiscOnly, false, 0, []string{"check logged", nextStatusLine}, stage.DiscOnDiscOnly, stage.CheckResultOK},
		{"packed, bad", stage.DiscPacked, true, 1, []string{": bad; this disc is bad; no record to remove", nextStatusLine}, stage.DiscPacked, stage.CheckResultFailed},
		{"burned, bad", stage.DiscBurned, true, 1, []string{": bad; this disc is bad; burn record removed", nextStatusLine}, stage.DiscPacked, stage.CheckResultFailed},
		{"verified, bad", stage.DiscVerified, true, 1, []string{": bad; this disc is bad; verified record removed; gc holds the data", nextStatusLine}, stage.DiscBurned, stage.CheckResultFailed},
		{"on disc only, bad", stage.DiscOnDiscOnly, true, 1, []string{": bad; the staged copy is already freed; copy this disc now, or use your second copy, or run: noahsark disc lost 0", nextStatusLine}, stage.DiscOnDiscOnly, stage.CheckResultFailed},
		{"lost", stage.DiscLost, false, 1, []string{"disc 0 is marked lost"}, stage.DiscLost, stage.CheckResultOK},
		{"missing", stage.DiscMissing, false, 1, []string{"disc 0 is missing; give it to recover"}, stage.DiscMissing, stage.CheckResultNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := repoWithDisc(t, c.start)
			if c.damaged {
				corruptDiscRoot(t, fx.root)
			}
			code, out := fx.run(t, "verify", fx.root)
			if code != c.exit {
				t.Fatalf("exit %d, want %d: %s", code, c.exit, out)
			}
			for _, line := range c.lines {
				if !strings.Contains(out, line) {
					t.Fatalf("output %q, want %q", out, line)
				}
			}
			if c.exit == 0 && !strings.Contains(out, fx.name()+": ") {
				t.Fatalf("output %q does not name %s", out, fx.name())
			}
			d := discState(t, fx.repo, fx.uuid)
			if d.State != c.want || d.LastCheck != c.wantLast {
				t.Fatalf("disc %s, last check %d; want %s, %d", d.State, d.LastCheck, c.want, c.wantLast)
			}
		})
	}
}

// TestVerifyOfAPackedDiscMakesItsItemsClean checks the derived item
// words after a good verify of a packed disc.
func TestVerifyOfAPackedDiscMakesItsItemsClean(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	fx.mustRun(t, "verify", fx.root)
	words := itemWords(t, fx.repo, fx.uuid)
	if len(words) != 1 || words[stage.WordClean] == 0 {
		t.Fatalf("item words %v, want every item clean", words)
	}
}

// findAChunkFile walks base/objects and returns the path of the first
// file whose magic_kind is CHUNK.
func findAChunkFile(t *testing.T, base string) string {
	t.Helper()
	var found string
	err := filepath.Walk(filepath.Join(base, "objects"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		var h format.CommonHeader
		if h.Decode(data) != nil {
			return nil
		}
		if h.MagicKind == format.MagicChunk {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("no chunk file found")
	}
	return found
}

// flipByte flips one bit at offset in the file at path.
func flipByte(t *testing.T, path string, offset int64) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[offset] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyFailureReturnsBurnedToPacked marks a disc burned and
// corrupts a chunk in its mounted copy: verify must fail, remove the
// burn record, and return the disc to packed. Repairing the byte,
// marking the disc burned again, and verifying once more must then
// reach verified, showing the disc is not stuck.
func TestVerifyFailureReturnsBurnedToPacked(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	repair := corruptDiscRoot(t, fx.root)

	code, out := fx.run(t, "verify", fx.root)
	if code != 1 {
		t.Fatalf("verify (corrupt): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, fx.name()+": bad; this disc is bad; burn record removed") {
		t.Fatalf("verify (corrupt) output %q, want the burn-record-removed line", out)
	}
	if d := discState(t, fx.repo, fx.uuid); d.State != stage.DiscPacked {
		t.Fatalf("disc state %s after a failed verify, want packed", d.State)
	}

	repair()
	fx.mustRun(t, "disc", "burned", fx.uuid)
	out = fx.mustRun(t, "verify", fx.root)
	if !strings.Contains(out, "\nverified\n") {
		t.Fatalf("verify (repaired) output %q, want the verified line", out)
	}
	if d := discState(t, fx.repo, fx.uuid); d.State != stage.DiscVerified {
		t.Fatalf("disc state %s after a good verify, want verified", d.State)
	}
}

// TestVerifyIgnoresATreeWithNoLedgerRow verifies a NOAHSARK tree whose
// disc uuid the repository has never seen: verify must refuse instead
// of marking anything, or reporting ok.
func TestVerifyIgnoresATreeWithNoLedgerRow(t *testing.T) {
	work := t.TempDir()
	repoA := filepath.Join(work, "repoA")
	repoB := filepath.Join(work, "repoB")
	src := writeFixtureSource(t)

	for _, repo := range []string{repoA, repoB} {
		if code, out := runIn(t, repo, "init"); code != 0 {
			t.Fatalf("init %s: exit %d: %s", repo, code, out)
		}
	}
	if code, out := runCmd(t, "--repo="+repoB, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repoB, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	treeFromB := packedTreeDir(t, repoB, packOut)
	mounted := filepath.Join(work, "mounted")
	copyTree(t, treeFromB, mounted)

	code, out := runCmd(t, "--repo="+repoA, "verify", mounted)
	if code != 1 {
		t.Fatalf("verify: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "disc "+packedDiscUUID(t, packOut)+" is not in this repository") {
		t.Fatalf("verify output %q missing the not-in-this-repository refusal", out)
	}
	if strings.Contains(out, ", ok") {
		t.Fatalf("verify output %q, want no ok line for a disc not in this repository", out)
	}
	for _, f := range []string{testLayout(t, repoA).stateLogFile(), testLayout(t, repoA).discLogFile()} {
		if _, err := os.Stat(f); err == nil {
			t.Fatalf("verify against an unknown disc wrote %s", f)
		}
	}
}

// TestVerifyAcceptsPositionalDiscRoot checks that verify takes exactly
// one DISC-ROOT positional argument, and refuses two.
func TestVerifyAcceptsPositionalDiscRoot(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, repo, packOut)
	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)

	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify DISC-ROOT: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "disc 0 ") || !strings.Contains(out, ", ok") {
		t.Fatalf("verify DISC-ROOT output %q missing the disc line with ok", out)
	}

	code, out = runCmd(t, "--repo="+repo, "verify", mounted, mounted)
	if code != 2 {
		t.Fatalf("verify with two DISC-ROOT arguments: exit %d, want 2: %s", code, out)
	}
}

// TestVerifyHealReportsFiles checks that verify --heal reports in the
// operator's words: healed files, with no talk of stripes.
func TestVerifyHealReportsFiles(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	tree := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec", "--out="+tree); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	healed := filepath.Join(work, "healed")
	code, out := runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+healed, tree)
	if code != 0 {
		t.Fatalf("verify --heal: exit %d: %s", code, out)
	}
	if !strings.Contains(out, `: healed 0 file(s) into `+healed+"\n") || !strings.Contains(out, "\n"+notCountedDisc+"\n") {
		t.Fatalf("verify --heal output %q, want the healed line and the not counted line", out)
	}
	if strings.Contains(out, "stripe") {
		t.Fatalf("verify --heal output %q still uses the word stripe", out)
	}
}

// TestVerifyHealNeverCountsAsACopy verifies the real disc once, then
// heals it into a directory on the hard disk. A heal must leave the disc
// state log exactly as the one real verify left it, and it must say that
// the healed tree is not counted.
func TestVerifyHealNeverCountsAsACopy(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	mounted := filepath.Join(work, "mounted")
	copyTree(t, packedTreeDir(t, repo, packOut), mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", packedDiscUUID(t, packOut)); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "\nverified\n") {
		t.Fatalf("verify output %q, want the verified line", out)
	}
	logAfterVerify := discLogBytes(t, repo)

	healed := filepath.Join(work, "healed")
	code, out = runCmd(t, "--repo="+repo, "verify", "--heal", "--out="+healed, mounted)
	if code != 0 {
		t.Fatalf("verify --heal: exit %d: %s", code, out)
	}
	if !strings.Contains(out, `disc 0 "`) || !strings.Contains(out, `: healed 0 file(s) into `+healed+"\n") {
		t.Fatalf("verify --heal output %q, want the healed line", out)
	}
	if !strings.Contains(out, "\n"+notCountedDisc+"\n") {
		t.Fatalf("verify --heal output %q, want the not counted line: a healed tree is not a disc", out)
	}
	if strings.Contains(out, nextStatusLine) {
		t.Fatalf("verify --heal output %q, want no next line: a heal records nothing", out)
	}
	if !bytes.Equal(discLogBytes(t, repo), logAfterVerify) {
		t.Fatal("verify --heal wrote the disc state log")
	}
}

// TestVerifyHealRefusesWithNoOut checks that --heal with no --out is a
// usage error: healing in place is no longer supported.
func TestVerifyHealRefusesWithNoOut(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit"); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	tree := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec", "--out="+tree); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "verify", "--heal", tree)
	if code != 2 {
		t.Fatalf("verify --heal (no --out): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--heal needs --out") {
		t.Fatalf("verify --heal (no --out) output %q, want it to name the missing --out", out)
	}
}

// TestVerifyUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// verify: each case exits 2, never 0 or 1.
func TestVerifyUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"missing DISC-ROOT", []string{"--repo=" + repo, "verify"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runCmd(t, c.args...)
			if code != 2 {
				t.Fatalf("args %v: exit %d, want 2: %s", c.args, code, out)
			}
		})
	}
}

// TestVerifyHealRepairsADamagedDisc damages one chunk of a disc with
// FEC, heals it, and checks the three lines of a heal. The heal names
// the one repaired file and records nothing. With no repository, the
// lines name the disc by its uuid.
func TestVerifyHealRepairsADamagedDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	mounted := filepath.Join(work, "mounted")
	copyTree(t, packedTreeDir(t, repo, packOut), mounted)
	corruptDiscRoot(t, mounted)
	logBefore := discLogBytes(t, repo)
	discUUID := packedDiscUUID(t, packOut)
	label := defaultRefName() + " disc 0"

	healed := filepath.Join(work, "healed")
	te := newTestEnv(work)
	code, _ = te.run("--repo="+repo, "verify", "--heal", "--out="+healed, mounted)
	want := []string{
		`disc 0 "` + label + `": healed 1 file(s) into ` + healed,
		`disc 0 "` + label + `": ` + strconv.Itoa(objectsOnDisc(t, healed)) + ` items, ok`,
		notCountedDisc,
	}
	if got := strings.Split(strings.TrimRight(te.out.String(), "\n"), "\n"); code != 0 || !slices.Equal(got, want) {
		t.Fatalf("verify --heal: exit %d, lines %q, want 0 and %q\nstderr: %s", code, got, want, te.errOut.String())
	}
	if !bytes.Equal(discLogBytes(t, repo), logBefore) {
		t.Fatal("verify --heal wrote the disc state log")
	}

	healedNoRepo := filepath.Join(work, "healed-no-repo")
	code, _ = te.run("verify", "--heal", "--out="+healedNoRepo, mounted)
	if code != 0 || !strings.HasPrefix(te.out.String(), `disc `+discUUID+` "`+label+`": healed 1 file(s) into `+healedNoRepo+"\n") {
		t.Fatalf("verify --heal with no repository: exit %d: %s%s", code, te.out.String(), te.errOut.String())
	}
}

// TestVerifyHealRepairsDamagedParity damages parity blocks of a disc with
// FEC, alone and with one chunk, and heals it. The count of healed files
// holds each repaired parity file, and the healed root checks ok.
func TestVerifyHealRepairsDamagedParity(t *testing.T) {
	cases := []struct {
		name      string
		chunk     bool
		parity    []int
		wantFiles int
	}{
		{"two parity files", false, []int{0, 1}, 2},
		{"a chunk and three parity files", true, []int{1, 2, 22}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			repo := filepath.Join(work, "repo")
			src := writeFixtureSource(t)
			if code, out := runIn(t, repo, "init"); code != 0 {
				t.Fatalf("init: exit %d: %s", code, out)
			}
			if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
				t.Fatalf("commit: exit %d: %s", code, out)
			}
			code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--fec")
			if code != 0 {
				t.Fatalf("pack: exit %d: %s", code, packOut)
			}
			mounted := filepath.Join(work, "mounted")
			copyTree(t, packedTreeDir(t, repo, packOut), mounted)
			if tc.chunk {
				corruptDiscRoot(t, mounted)
			}
			for _, j := range tc.parity {
				flipByte(t, parityFilePath(t, mounted, j), 0)
			}
			label := defaultRefName() + " disc 0"

			healed := filepath.Join(work, "healed")
			te := newTestEnv(work)
			code, _ = te.run("--repo="+repo, "verify", "--heal", "--out="+healed, mounted)
			want := []string{
				`disc 0 "` + label + `": healed ` + strconv.Itoa(tc.wantFiles) + ` file(s) into ` + healed,
				`disc 0 "` + label + `": ` + strconv.Itoa(objectsOnDisc(t, healed)) + ` items, ok`,
				notCountedDisc,
			}
			if got := strings.Split(strings.TrimRight(te.out.String(), "\n"), "\n"); code != 0 || !slices.Equal(got, want) {
				t.Fatalf("verify --heal: exit %d, lines %q, want 0 and %q\nstderr: %s", code, got, want, te.errOut.String())
			}
		})
	}
}

// parityFilePath gives the path of parity column j of the newest run of
// the disc root root.
func parityFilePath(t *testing.T, root string, j int) string {
	t.Helper()
	base, err := image.FindNoahsark(root, image.NewNameCache())
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(runDir, "parity", fmt.Sprintf("p%04d.bin", fec.K+1+j))
}

// TestVerifyHealRefusesADiscWithNoFEC checks that --heal refuses a disc
// whose run has no FEC, and writes nothing into --out.
func TestVerifyHealRefusesADiscWithNoFEC(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	healed := filepath.Join(fx.work, "healed")
	code, out := fx.run(t, "verify", "--heal", "--out="+healed, fx.root)
	if code != 1 || !strings.Contains(out, "disc 0 has no FEC; --heal needs a disc with FEC") {
		t.Fatalf("verify --heal of a disc with no FEC: exit %d, want 1 and the refusal: %s", code, out)
	}
	if _, err := os.Stat(healed); !os.IsNotExist(err) {
		t.Fatalf("verify --heal of a disc with no FEC wrote %s", healed)
	}
}

// TestVerifyUndoTakesNoOtherOption checks that --undo with another
// option of verify is a usage error.
func TestVerifyUndoTakesNoOtherOption(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	for _, opt := range []string{"--no-mark", "--heal", "--out=" + fx.work} {
		code, out := fx.run(t, "verify", "--undo", opt, "0")
		if code != 2 || !strings.Contains(out, "--undo takes no other option") {
			t.Fatalf("verify --undo %s: exit %d, want 2: %s", opt, code, out)
		}
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscVerified {
		t.Fatalf("disc state %s, want verified", got)
	}
}
