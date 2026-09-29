package main

import (
	"fmt"
	"os"
	"path/filepath"
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
		stdout: []string{"recover: ok\n"}, next: true,
		end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
	})
	for _, s := range []stage.DiscState{stage.DiscPacked, stage.DiscBurned, stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscLost} {
		registerStateCases(stateCase{
			row: "70c", name: s.String() + " disc given",
			start: s, args: []string{"recover", "--source={SRC}", "--disc={ROOT}"},
			stdout: []string{"recover: ok; {DISC} already known\n"}, next: true,
			end: s,
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
	if err := os.RemoveAll(fx.repo); err != nil {
		t.Fatal(err)
	}
	return fx
}

// runRecover runs recover of the disc root of fx.
func (fx *discFixture) runRecover(t *testing.T) (int, string, string) {
	t.Helper()
	te := newTestEnv(t.TempDir())
	code, _ := te.run("--repo="+fx.repo, "recover", "--source="+fx.src, "--disc="+fx.root)
	return code, te.out.String(), te.errOut.String()
}

// TestRecoverRow67 recovers a lost repository from its one disc.
func TestRecoverRow67(t *testing.T) {
	fx := recoverFixture(t)
	rr, err := image.Read(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := fx.runRecover(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stdout != "recover: ok\n"+nextStatusLine+"\n" {
		t.Errorf("stdout %q", stdout)
	}
	d := discState(t, fx.repo, fx.uuid)
	if d.State != stage.DiscOnDiscOnly || d.LastCheck != stage.CheckResultNone {
		t.Errorf("disc state %s, last check %d, want on disc only with no check", d.State, d.LastCheck)
	}
	if n := countByState(t, fx.repo, stage.OnDisc); n != len(rr.Index.Objects) {
		t.Errorf("%d on-disc item(s), want %d", n, len(rr.Index.Objects))
	}
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
	if !strings.HasSuffix(string(state), " complete\n") || strings.Contains(string(state), "partial") {
		t.Errorf("catalog-state.txt %q, want every snapshot complete", state)
	}
}

// TestRecoverRow68 recovers a lost repository from the second of two
// discs. The first disc becomes missing.
func TestRecoverRow68(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	if err := os.WriteFile(filepath.Join(fx.src, "second.txt"), []byte("content of the second disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "commit", fx.src)
	packOut := fx.mustRun(t, "pack", "--capacity=64MiB")
	secondUUID := packedDiscUUID(t, packOut)
	second := filepath.Join(fx.work, "disc1")
	copyTree(t, packedTreeDir(t, fx.repo, packOut), second)
	if err := os.RemoveAll(fx.repo); err != nil {
		t.Fatal(err)
	}

	te := newTestEnv(t.TempDir())
	code, _ := te.run("--repo="+fx.repo, "recover", "--source="+fx.src, "--disc="+second)
	stdout := te.out.String()
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, te.errOut.String())
	}
	want := fmt.Sprintf("recover: %s named by another disc, not yet given\n%s\n", discName(0, fx.label, fx.uuidBytes(t)), nextStatusLine)
	if stdout != want {
		t.Errorf("stdout %q, want %q", stdout, want)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscMissing {
		t.Errorf("first disc %s, want missing", got)
	}
	if got := discState(t, fx.repo, secondUUID).State; got != stage.DiscOnDiscOnly {
		t.Errorf("second disc %s, want on disc only", got)
	}
}

// TestRecoverRow70 recovers a disc that the repository does not know
// into an existing repository. An item that the repository staged
// again keeps its state.
func TestRecoverRow70(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	first := fx.root
	if err := os.WriteFile(filepath.Join(fx.src, "second.txt"), []byte("content of the second disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "commit", fx.src)
	packOut := fx.mustRun(t, "pack", "--capacity=64MiB")
	secondUUID := packedDiscUUID(t, packOut)
	second := filepath.Join(fx.work, "disc1")
	copyTree(t, packedTreeDir(t, fx.repo, packOut), second)
	if err := os.RemoveAll(fx.repo); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "recover", "--source="+fx.src, "--disc="+first)
	fx.mustRun(t, "commit", fx.src)
	staged := countByState(t, fx.repo, stage.Staged)
	if staged == 0 {
		t.Fatal("the commit staged no item")
	}

	fx.root = second
	code, stdout, stderr := fx.runRecover(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stdout != "recover: ok\n"+nextStatusLine+"\n" {
		t.Errorf("stdout %q", stdout)
	}
	if got := discState(t, fx.repo, secondUUID).State; got != stage.DiscOnDiscOnly {
		t.Errorf("second disc %s, want on disc only", got)
	}
	if got := countByState(t, fx.repo, stage.Staged); got != staged {
		t.Errorf("%d staged item(s), want %d", got, staged)
	}
}

// TestRecoverRow70a recovers a lost repository from a disc with a
// damaged object. The other objects are recorded, and the disc is on
// disc only with a failed check.
func TestRecoverRow70a(t *testing.T) {
	for _, kind := range []format.ObjectKind{format.ObjectKindChunk, format.ObjectKindTree} {
		t.Run(fmt.Sprintf("kind %d", kind), func(t *testing.T) {
			fx := recoverFixture(t)
			rr, err := image.Read(fx.root)
			if err != nil {
				t.Fatal(err)
			}
			bad := damageObject(t, fx.root, kind)
			code, stdout, stderr := fx.runRecover(t)
			if code != 1 {
				t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			want := fmt.Sprintf("recover: damaged: %s\nrecover: 1 item(s) damaged on %s\n%s\n", bad.TextForm(), fx.name(), nextStatusLine)
			if stdout != want {
				t.Errorf("stdout %q, want %q", stdout, want)
			}
			d := discState(t, fx.repo, fx.uuid)
			if d.State != stage.DiscOnDiscOnly || d.LastCheck != stage.CheckResultFailed {
				t.Errorf("disc state %s, last check %d, want on disc only with a failed check", d.State, d.LastCheck)
			}
			if _, ok := openTestLog(t, fx.repo).Get(bad); ok {
				t.Error("the damaged object has a record")
			}
			if n := countByState(t, fx.repo, stage.OnDisc); n != len(rr.Index.Objects)-1 {
				t.Errorf("%d on-disc item(s), want %d", n, len(rr.Index.Objects)-1)
			}
			state, err := os.ReadFile(testLayout(t, fx.repo).catalogStateFile())
			if err != nil {
				t.Fatal(err)
			}
			wantMark := " complete\n"
			if kind == format.ObjectKindTree {
				wantMark = " partial\n"
			}
			if !strings.HasSuffix(string(state), wantMark) {
				t.Errorf("catalog-state.txt %q, want the mark %q", state, wantMark)
			}
		})
	}
}

// TestRecoverRow70b refuses a disc of another repository.
func TestRecoverRow70b(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	other := repoWithDisc(t, stage.DiscPacked)
	code, stdout, stderr := (&discFixture{repo: fx.repo, src: fx.src, root: other.root}).runRecover(t)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	cfg, err := readConfig(configPath(other.repo))
	if err != nil {
		t.Fatal(err)
	}
	otherRepo, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("disc %s belongs to repository %s, not to this repository\n", other.uuid, uuidText(otherRepo))
	if !strings.HasSuffix(stderr, want) || stdout != "" {
		t.Errorf("stdout %q, stderr %q, want the refusal %q", stdout, stderr, want)
	}
	if n := len(readDiscLog(t, fx.repo).Discs()); n != 1 {
		t.Errorf("the disc state log knows %d disc(s), want 1", n)
	}
}

// TestRecoverRow70d recovers a damaged copy of a verified disc of the
// repository. It writes no event.
func TestRecoverRow70d(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	before := discState(t, fx.repo, fx.uuid)
	bad := damageObject(t, fx.root, format.ObjectKindChunk)
	code, stdout, stderr := fx.runRecover(t)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	want := fmt.Sprintf("recover: damaged: %s\nrecover: 1 item(s) damaged on %s\n%s\n", bad.TextForm(), fx.name(), nextStatusLine)
	if stdout != want {
		t.Errorf("stdout %q, want %q", stdout, want)
	}
	if after := discState(t, fx.repo, fx.uuid); after != before {
		t.Errorf("disc record %+v, want %+v", after, before)
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
