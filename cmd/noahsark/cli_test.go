package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGlobalOptionAfterCommandRefused is row 72: a global option after
// the command name is a usage error that names the corrected line.
func TestGlobalOptionAfterCommandRefused(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"status", "--repo=/r"}, "--repo is a global option; give it before the command name: noahsark --repo=/r status"},
		{[]string{"-q", "status", "--yes"}, "--yes is a global option; give it before the command name: noahsark -q --yes status"},
		{[]string{"gc", "--dry-run", "--force-yes"}, "--force-yes is a global option; give it before the command name: noahsark --force-yes gc --dry-run"},
		{[]string{"commit", "-q", "/src"}, "-q is a global option; give it before the command name: noahsark -q commit /src"},
		{[]string{"disc", "burned", "--quiet", "0"}, "--quiet is a global option; give it before the command name: noahsark --quiet disc burned 0"},
		{[]string{"commit", "/src", "--repo=/r"}, "--repo is a global option; give it before the command name: noahsark --repo=/r commit /src"},
		{[]string{"log", "--version"}, "--version is a global option; give it before the command name: noahsark --version log"},
	}
	for _, c := range cases {
		code, out := runCmd(t, c.args...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", c.args, code, out)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%v: output %q, want %q", c.args, out, c.want)
		}
	}
}

// TestGlobalOptionValueOfCommandOptionAccepted checks that the value of
// a command option is not read as a global option.
func TestGlobalOptionValueOfCommandOptionAccepted(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	src := writeFixtureSource(t)
	code, out := runCmd(t, "--repo="+repo, "commit", "-m", "--yes", src)
	if code != 0 {
		t.Fatalf("commit -m --yes: exit %d: %s", code, out)
	}
}

// TestCommandOptionBeforeLastSubcommandWordRefused is row 73.
func TestCommandOptionBeforeLastSubcommandWordRefused(t *testing.T) {
	want := "--undo is an option of disc burned; give it after the last subcommand word: noahsark disc burned --undo 0; see: noahsark disc burned -h"
	for _, args := range [][]string{
		{"--undo", "disc", "burned", "0"},
		{"disc", "--undo", "burned", "0"},
	} {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%v: output %q, want %q", args, out, want)
		}
	}

	code, out := runCmd(t, "--dry-run", "gc")
	if code != 2 || !strings.Contains(out, "--dry-run is an option of gc; give it after the last subcommand word: noahsark gc --dry-run; see: noahsark gc -h") {
		t.Errorf("--dry-run gc: exit %d, output %q", code, out)
	}

	code, out = runCmd(t, "--bogus", "status")
	if code != 2 || !strings.Contains(out, "unknown global option --bogus") {
		t.Errorf("--bogus status: exit %d, output %q", code, out)
	}
}

// TestGroupWithNoSubcommandRefused is row 74.
func TestGroupWithNoSubcommandRefused(t *testing.T) {
	code, out := runCmd(t, "disc")
	if code != 2 {
		t.Fatalf("disc: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "disc needs a subcommand:\n") || !strings.Contains(out, "  burned") {
		t.Fatalf("disc: output %q, want the subcommand list", out)
	}

	code, out = runCmd(t, "image")
	if code != 2 || !strings.Contains(out, "image needs a subcommand:\n") || !strings.Contains(out, "  build") {
		t.Fatalf("image: exit %d, output %q", code, out)
	}
}

// TestUnknownSubcommandRefused is row 75.
func TestUnknownSubcommandRefused(t *testing.T) {
	code, out := runCmd(t, "disc", "burnt", "0")
	if code != 2 {
		t.Fatalf("disc burnt 0: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "unknown subcommand of disc: burnt; the subcommands are:\n") || !strings.Contains(out, "  burned") {
		t.Fatalf("disc burnt 0: output %q, want the subcommand list", out)
	}
}

// TestGroupHelp is row 76: -h on a group, in both positions, lists the
// subcommands on standard output and exits 0.
func TestGroupHelp(t *testing.T) {
	for _, args := range [][]string{{"disc", "-h"}, {"-h", "disc"}, {"disc", "--help"}, {"--help", "disc"}} {
		te := newTestEnv(t.TempDir())
		code, _ := te.run(args...)
		if code != 0 {
			t.Errorf("%v: exit %d, want 0: %s", args, code, te.errOut.String())
		}
		if !strings.Contains(te.out.String(), "  burned") {
			t.Errorf("%v: standard output %q, want the burned line", args, te.out.String())
		}
	}
}

// TestCommandHelpInBothPositions checks that -h before and after the
// command name prints the help of the command on standard output.
func TestCommandHelpInBothPositions(t *testing.T) {
	for _, args := range [][]string{
		{"status", "-h"},
		{"-h", "status"},
		{"disc", "burned", "-h"},
		{"-h", "disc", "burned"},
		{"image", "build", "--help"},
		{"--repo=/nowhere", "gc", "-h"},
	} {
		te := newTestEnv(t.TempDir())
		code, _ := te.run(args...)
		if code != 0 {
			t.Errorf("%v: exit %d, want 0: %s", args, code, te.errOut.String())
		}
		if !strings.Contains(te.out.String(), "usage: noahsark ") {
			t.Errorf("%v: standard output %q, want the usage line", args, te.out.String())
		}
	}

	te := newTestEnv(t.TempDir())
	if code, _ := te.run("disc", "burned", "-h"); code != 0 || !strings.Contains(te.out.String(), "-undo") {
		t.Errorf("disc burned -h: exit %d, output %q, want the --undo option", code, te.out.String())
	}
}

// TestTopHelp checks that -h lists the commands and the groups, and that
// no argument is a usage error.
func TestTopHelp(t *testing.T) {
	te := newTestEnv(t.TempDir())
	code, _ := te.run("-h")
	if code != 0 {
		t.Fatalf("-h: exit %d", code)
	}
	for _, want := range []string{"commit", "status", "disc", "image", "--repo=PATH", "--force-yes"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("-h: output %q does not name %s", te.out.String(), want)
		}
	}

	code, _ = te.run()
	if code != 2 || te.out.Len() != 0 || !strings.Contains(te.errOut.String(), "usage:") {
		t.Errorf("no argument: exit %d, stdout %q, stderr %q", code, te.out.String(), te.errOut.String())
	}
}

// TestTopHelpWorkflowOrder checks that -h lists each command and group
// one time, in the order of the work, and names status as the source of
// the next step.
func TestTopHelpWorkflowOrder(t *testing.T) {
	te := newTestEnv(t.TempDir())
	if code, _ := te.run("-h"); code != 0 {
		t.Fatalf("-h: exit %d", code)
	}
	_, list, _ := strings.Cut(te.out.String(), "Commands, in the order of the work:\n")
	list, tail, _ := strings.Cut(list, "\n\n")
	var got []string
	for line := range strings.SplitSeq(list, "\n") {
		got = append(got, strings.Fields(line)[0])
	}
	want := []string{"init", "commit", "status", "pack", "image", "disc", "verify", "gc", "restore", "recover", "ls", "log"}
	if !slices.Equal(got, want) {
		t.Errorf("-h: command list %v, want %v", got, want)
	}
	for _, c := range subcommands("") {
		if !slices.Contains(got, c.name) && !strings.HasPrefix(c.name, "probe-") {
			t.Errorf("-h: the command list does not name %s", c.name)
		}
	}
	if !strings.Contains(tail, "\"noahsark status\" prints the next step.") {
		t.Errorf("-h: output %q, want the status line", te.out.String())
	}
}

// TestCommandHelpOptionSpelling checks that the help of a command shows
// each option in the form of the synopsis, not in the form of the Go
// flag package.
func TestCommandHelpOptionSpelling(t *testing.T) {
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"recover", "-h"}, []string{"\n  --source=PATH\n", "\n  --disc=DIR\n"}},
		{[]string{"pack", "-h"}, []string{"\n  --capacity=SIZE\n", "\n  --out=DIR\n", "\n  --dry-run\n"}},
		{[]string{"commit", "-h"}, []string{"\n  -m MESSAGE\n", "\n  --ref=NAME\n", "\n  --exclude=PATTERN\n"}},
		{[]string{"ls", "-h"}, []string{"\n  -R\n", "\n  --recursive\n"}},
	} {
		te := newTestEnv(t.TempDir())
		if code, _ := te.run(c.args...); code != 0 {
			t.Fatalf("%v: exit %d", c.args, code)
		}
		out := te.out.String()
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%v: output %q does not hold %q", c.args, out, want)
			}
		}
		if strings.Contains(out, " string\n") {
			t.Errorf("%v: output %q holds the Go flag form", c.args, out)
		}
	}
}

// TestVersion checks that --version prints the version and exits 0.
func TestVersion(t *testing.T) {
	te := newTestEnv(t.TempDir())
	code, _ := te.run("--version")
	if code != 0 || !strings.HasPrefix(te.out.String(), "noahsark ") {
		t.Fatalf("--version: exit %d, output %q", code, te.out.String())
	}
}

// TestAnswerFlagsAreStored checks that --yes and --force-yes are parsed
// as global options.
func TestAnswerFlagsAreStored(t *testing.T) {
	te := newTestEnv(t.TempDir())
	var got globalOptions
	register(&command{
		name:  "probe-globals",
		usage: "probe-globals",
		flags: func(*flag.FlagSet) runFunc {
			return func(e *env, _ []string) int {
				got = e.global
				return 0
			}
		},
	})
	t.Cleanup(func() { commands = commands[:len(commands)-1] })

	if code, out := te.run("--yes", "--force-yes", "-q", "--repo", "/r", "probe-globals"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	want := globalOptions{repo: "/r", quiet: true, yes: true, forceYes: true}
	if got != want {
		t.Fatalf("global options = %+v, want %+v", got, want)
	}
}

// TestNoRepository checks the message and the exit code of a command
// that needs a repository and finds none.
func TestNoRepository(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"pack"}, {"gc"}, {"disc", "burned", "0"}} {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, errNoRepo.Error()) {
			t.Errorf("%v: output %q", args, out)
		}
	}
}

// TestRepositoryDiscoveryOrder checks the order: --repo, then
// NOAHSARK_REPO, then the working directory and each ancestor of it.
func TestRepositoryDiscoveryOrder(t *testing.T) {
	work := t.TempDir()
	flagRepo := filepath.Join(work, "flag")
	envRepo := filepath.Join(work, "env")
	cwdRepo := filepath.Join(work, "cwd")
	for _, dir := range []string{flagRepo, envRepo, cwdRepo} {
		if code, out := runIn(t, dir, "init"); code != 0 {
			t.Fatalf("init %s: exit %d: %s", dir, code, out)
		}
	}
	below := filepath.Join(cwdRepo, "a", "b")
	if err := os.MkdirAll(below, 0o755); err != nil {
		t.Fatal(err)
	}

	te := newTestEnv(below)
	te.vars[repoEnvVar] = envRepo
	te.global.repo = flagRepo
	if got, err := te.findRepo(); err != nil || got != flagRepo {
		t.Errorf("with --repo: %q, %v; want %q", got, err, flagRepo)
	}
	te.global.repo = ""
	if got, err := te.findRepo(); err != nil || got != envRepo {
		t.Errorf("with NOAHSARK_REPO: %q, %v; want %q", got, err, envRepo)
	}
	delete(te.vars, repoEnvVar)
	if got, err := te.findRepo(); err != nil || got != cwdRepo {
		t.Errorf("from an ancestor: %q, %v; want %q", got, err, cwdRepo)
	}

	te = newTestEnv(work)
	if _, err := te.findRepo(); !errors.Is(err, errNoRepo) {
		t.Errorf("no repository: %v, want errNoRepo", err)
	}
	te.global.repo = "flag"
	if got, err := te.findRepo(); err != nil || got != flagRepo {
		t.Errorf("relative --repo: %q, %v; want %q", got, err, flagRepo)
	}
	te.global.repo = filepath.Join(work, "none")
	if _, err := te.findRepo(); err == nil || !strings.Contains(err.Error(), "is not a noahsark repository") {
		t.Errorf("--repo of a directory that is not a repository: %v", err)
	}
}

// TestDiscGlobalOptionBeforeSubcommandNamesTheFix checks that a global
// option between disc and its subcommand ("disc --repo=X burned") is
// reported with the corrected command line, not as an unknown
// subcommand.
func TestDiscGlobalOptionBeforeSubcommandNamesTheFix(t *testing.T) {
	code, out := runCmd(t, "disc", "--repo=X", "burned")
	if code != 2 {
		t.Fatalf("disc --repo=X burned: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--repo is a global option; give it before the command name: noahsark --repo=X disc burned") {
		t.Fatalf("disc --repo=X burned: output %q, want the corrected command line", out)
	}
	if strings.Contains(out, "unknown subcommand") {
		t.Fatalf("disc --repo=X burned: output %q, want no unknown-subcommand wording", out)
	}
}

// TestDiscUnknownSubcommandRefused checks that a subcommand this build
// does not have, such as the old "label" and "mark-degraded", is
// refused as an unknown subcommand rather than silently ignored.
func TestDiscUnknownSubcommandRefused(t *testing.T) {
	for _, args := range [][]string{
		{"disc", "label", "00000000-0000-0000-0000-000000000000", "TEXT"},
		{"disc", "mark-degraded", "00000000-0000-0000-0000-000000000000"},
	} {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, "unknown subcommand") {
			t.Fatalf("%v: output %q, want \"unknown subcommand\"", args, out)
		}
	}
}

// TestUnknownCommandRefused asserts that an unrecognized command name
// exits 2 with a message naming it, matching an unknown flag's exit
// code.
func TestUnknownCommandRefused(t *testing.T) {
	code, out := runCmd(t, "sync", "/nowhere")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	if !strings.Contains(out, `unknown command "sync"`) {
		t.Fatalf("output = %q, want it to name the unknown command", out)
	}
}

// TestUnknownFlagRefused asserts that a flag no command defines exits 2,
// not the process crashing or a silent success.
func TestUnknownFlagRefused(t *testing.T) {
	code, _ := runCmd(t, "commit", "--no-such-flag", "/nowhere")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

// TestNoArgsPrintsUsage asserts the exit-code and usage contract for no
// arguments and -h.
func TestNoArgsPrintsUsage(t *testing.T) {
	if code, out := runCmd(t); code != 2 || !strings.Contains(out, "usage:") {
		t.Fatalf("no args: exit %d, output %q", code, out)
	}
	if code, out := runCmd(t, "-h"); code != 0 || !strings.Contains(out, "usage:") {
		t.Fatalf("-h: exit %d, output %q", code, out)
	}
}

// TestRepoFromEnvironment checks NOAHSARK_REPO: the normal cycle runs
// with no --repo at all.
func TestRepoFromEnvironment(t *testing.T) {
	repo, src := initAndCommit(t)
	te := newTestEnv(t.TempDir())
	te.vars["NOAHSARK_REPO"] = repo
	appendConfig(t, repo, "sources:\n  root: "+src+"\n")

	if code, out := te.run("commit"); code != 0 {
		t.Fatalf("commit with no flag: exit %d: %s", code, out)
	}
	if code, out := te.run("pack", "--capacity=64MiB"); code != 0 {
		t.Fatalf("pack with no flag: exit %d: %s", code, out)
	}
	code, out := te.run("status")
	if code != 0 {
		t.Fatalf("status with no flag: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "image build 0 &&") {
		t.Fatalf("status output %q does not name the burn", out)
	}
	if code, out := te.run("disc", "burned", "0"); code != 0 {
		t.Fatalf("disc burned 0 with no flag: exit %d: %s", code, out)
	}
}

// TestGlobalFlagBeforeCommand asserts that -q works before the command
// name, not only after it.
func TestGlobalFlagBeforeCommand(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "-q", "init"); code != 0 {
		t.Fatalf("-q init: exit %d: %s", code, out)
	}
}

// TestCommandHelpExitsZeroAndShowsPositionals asserts that a command's
// own -h prints its usage line, including its positional arguments, and
// exits 0.
func TestCommandHelpExitsZeroAndShowsPositionals(t *testing.T) {
	code, out := runCmd(t, "commit", "-h")
	if code != 0 {
		t.Fatalf("commit -h: exit %d, want 0; output: %s", code, out)
	}
	if !strings.Contains(out, "SOURCE") {
		t.Fatalf("commit -h output = %q, want it to mention SOURCE", out)
	}
	if !strings.Contains(out, "-ref") {
		t.Fatalf("commit -h output = %q, want it to list -ref", out)
	}
}

// TestUsageErrorsExitTwo checks the usage-error convention of the exit
// code registry for the top-level command line: each case exits 2, never
// 0 or 1.
func TestUsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"unknown top-level command", []string{"bogus"}},
		{"disc: unknown subcommand", []string{"disc", "bogus"}},
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
