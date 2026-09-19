// Command noahsark is the NoahsArk command-line tool. This build
// implements the Phase 1 subset of OPERATIONS.md's CLI reference: init,
// commit, pack, image build, verify, restore, ls, log, plan,
// rebuild-cache, disc list and gc. Every other command name, and every
// flag or config key of a later phase, is refused.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tjjh89017/noahsark/internal/progress"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command and returns the process exit code: 0 for
// success, 1 for a failure the command reports cleanly, 2 for a usage
// error, including a refused later-phase name.
func run(args []string, stdout, stderr io.Writer) int {
	args, prog, err := extractProgressFlags(args, stderr)
	if err != nil {
		return 2
	}

	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		printUsage(stdout)
		return 0
	}

	cmd := args[0]
	rest := args[1:]

	if phase, ok := laterPhaseCommands[cmd]; ok {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s is a %s command; this build implements Phase 1\n", cmd, phase)
		return 2
	}
	if notYetInBuildCommands[cmd] {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s is not in this build yet\n", cmd)
		return 2
	}

	switch cmd {
	case "init":
		return cmdInit(rest, stdout, stderr)
	case "commit":
		return cmdCommit(rest, stdout, stderr, prog)
	case "pack":
		return cmdPack(rest, stdout, stderr, prog)
	case "image":
		return cmdImage(rest, stdout, stderr, prog)
	case "verify":
		return cmdVerify(rest, stdout, stderr, prog)
	case "restore":
		return cmdRestore(rest, stdout, stderr, prog)
	case "ls":
		return cmdLs(rest, stdout, stderr)
	case "log":
		return cmdLog(rest, stdout, stderr)
	case "rebuild-cache":
		return cmdRebuildCache(rest, stdout, stderr, prog)
	case "plan":
		return cmdPlan(rest, stdout, stderr)
	case "disc":
		return cmdDisc(rest, stdout, stderr)
	case "gc":
		return cmdGC(rest, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "noahsark: unknown command %q\n", cmd)
		printUsage(stderr)
		return 2
	}
}

// extractProgressFlags pulls --progress, --no-progress and --quiet (-q)
// out of args, wherever they appear, before or after the command name,
// and returns the remaining arguments plus the Reporter to use: nil when
// progress reporting is off. Without --progress or --no-progress,
// progress is on only when the real process stderr is a terminal, so a
// command run into a file or a pipe writes no progress noise onto it;
// --no-progress and --quiet always turn it off, and --progress always
// forces it on. --progress and --no-progress are mutually exclusive.
func extractProgressFlags(args []string, stderr io.Writer) ([]string, *progress.Reporter, error) {
	var forceOn, forceOff, quiet bool
	remaining := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--progress":
			forceOn = true
		case "--no-progress":
			forceOff = true
		case "--quiet", "-q":
			quiet = true
		default:
			remaining = append(remaining, a)
		}
	}
	if forceOn && forceOff {
		_, _ = fmt.Fprintln(stderr, "noahsark: --progress and --no-progress are mutually exclusive")
		return nil, nil, fmt.Errorf("mutually exclusive flags")
	}
	if quiet || forceOff {
		return remaining, nil, nil
	}
	if forceOn || stderrIsTerminal() {
		return remaining, progress.New(stderr), nil
	}
	return remaining, nil, nil
}

// stderrIsTerminal reports whether the process's real standard error is
// a terminal. It always checks the process's own os.Stderr, never a
// writer a caller substituted, since a progress line's whole purpose is
// to be readable by a human watching a real terminal.
func stderrIsTerminal() bool {
	return isTerminal(os.Stderr)
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `usage: noahsark <command> [arguments]

Phase 1 commands:
  init    [--repo=PATH] [--source=PATH]
  commit  [SOURCE] [--ref=NAME] [-m MESSAGE]
  pack    [--ref=NAME | --snapshot=ID]... --capacity=N [--physical-capacity=N] [--label=TEXT] [--media=NAME] [--out=DIR] [--fec | --no-fec] [--close]
  image build --out=FILE [--capacity=N] [--force] TREE-DIR
  verify  [DISC-ROOT] [--repo=DIR] --image=PATH [--heal] [--out=DIR]
  restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR
  restore [--include=PATH]... [--overwrite] --mount=DIR [--no-eject] [--interactive] [--staging-budget=SIZE] SNAPSHOT OUT-DIR
  restore --plan=FILE --mount=DIR [--overwrite] [--no-eject] [--interactive] [--staging-budget=SIZE] OUT-DIR
  ls      [DISC-ROOT] SNAPSHOT [PATH] [--long] [--recursive] [--json] [--unstable-only]
  log     [DISC-ROOT] [REF|SNAPSHOT] [--limit=N] [--json]
  plan    [--include=PATH]... [--out=FILE] [--staging-budget=SIZE] SNAPSHOT
  rebuild-cache --from-disc [--disc=ROOT]... [--discs-dir=DIR] [--level=1] [--snapshot=ID]
  disc list [--json]
  disc burned [--undo] DISC [DISC...]
  gc      [--dry-run] [--verbose] [--keep-snapshots=N] [--force-after=DURATION] [--yes]

ls and log resolve SNAPSHOT through the local cache when no disc is
given; give a DISC-ROOT, or --disc=ROOT (repeatable) or --discs-dir=DIR
as rebuild-cache and restore also accept, to read a disc instead. plan
always reads the local cache; it takes no disc.

Not yet implemented (Phase 1 commands OPERATIONS.md defines, absent from
this build): burn, scrub, health.

Every command also accepts --progress, --no-progress and --quiet (-q),
which control the progress line a long-running command writes to
stderr. Progress is on by default only when stderr is a terminal.

Run "noahsark <command> -h" for a command's own flags.
`)
}

// laterPhaseCommands names every OPERATIONS.md command that this build
// does not implement because it belongs to a later phase.
var laterPhaseCommands = map[string]string{
	"sync":        "Phase 2",
	"append":      "Phase 2",
	"close":       "Phase 2",
	"watch":       "Phase 3",
	"consolidate": "Phase 3",
	"reindex":     "Phase 3",
	"catalog":     "Backlog",
	"import":      "Backlog",
}

// notYetInBuildCommands names every Phase 1 command OPERATIONS.md's CLI
// reference defines that this build does not implement yet. Unlike
// laterPhaseCommands, these are not deferred to a later phase; they are
// simply not built yet.
var notYetInBuildCommands = map[string]bool{
	"burn":   true,
	"scrub":  true,
	"health": true,
}

// refuseLaterPhaseFlags scans args for any flag name later than Phase 1,
// per the table for this command, and reports the first one it finds.
// It runs before flag.Parse so the message names the phase, not just
// "flag provided but not defined".
func refuseLaterPhaseFlags(cmd string, args []string, stderr io.Writer) bool {
	table := laterPhaseFlags[cmd]
	for _, a := range args {
		name := a
		if i := indexByte(a, '='); i >= 0 {
			name = a[:i]
		}
		if phase, ok := table[name]; ok {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s is a %s option; not available in Phase 1\n", cmd, name, phase)
			return true
		}
	}
	return false
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// laterPhaseFlags names every later-phase flag OPERATIONS.md lists for a
// Phase 1 command. Every other flag OPERATIONS.md lists for these
// commands is a Phase 1 option this build does not implement yet; it is
// simply not defined, so passing it is a plain usage error. See
// docs/decisions.md, "16. CLI reference".
var laterPhaseFlags = map[string]map[string]string{
	"commit": {
		"--from":       "Phase 2",
		"--copy-first": "Phase 2",
		"--out":        "Backlog",
		"--catalog":    "Backlog",
	},
	"pack": {
		"--disc": "Phase 2",
	},
	"restore": {
		"--no-xattr":      "Phase 2",
		"--no-acl":        "Phase 2",
		"--translate-acl": "Phase 2",
	},
}

// notYetImplementedFlags names every Phase 1 flag OPERATIONS.md lists for
// a command that this build does not implement yet, but that is not
// deferred to a later phase. refuseNotYetImplementedFlags reports one of
// these with a clear message instead of letting flag.Parse fail with the
// raw "flag provided but not defined".
var notYetImplementedFlags = map[string]map[string]bool{
	"init": {
		"--hash":          true,
		"--chunker":       true,
		"--fs-profile":    true,
		"--preset":        true,
		"--repo-uuid":     true,
		"--next-run-seq":  true,
		"--next-disc-seq": true,
		"--scan-discs":    true,
	},
	"commit": {
		"--dry-run":         true,
		"--checksum":        true,
		"--full-scan":       true,
		"--force":           true,
		"--exclude":         true,
		"--one-file-system": true,
		"--source":          true,
		"--source-root":     true,
		"--source-type":     true,
		"--retry-unstable":  true,
	},
	"verify": {
		"--disc":    true,
		"--run":     true,
		"--mapfile": true,
		"--level":   true,
		"--drive":   true,
		"--report":  true,
	},
	"restore": {
		"--plan":            true,
		"--drives":          true,
		"--staging-budget":  true,
		"--no-owner":        true,
		"--numeric-owner":   true,
		"--no-flags":        true,
		"--no-times":        true,
		"--no-hardlinks":    true,
		"--metadata-strict": true,
		"--report":          true,
		"--report-replay":   true,
		"--strict-unstable": true,
	},
	"image build": {
		"--run": true,
	},
}

// refuseNotYetImplementedFlags scans args for a flag notYetImplementedFlags
// names for cmd, and reports the first one it finds. It runs before
// flag.Parse so the message is clear, not the raw flag package error.
func refuseNotYetImplementedFlags(cmd string, args []string, stderr io.Writer) bool {
	table := notYetImplementedFlags[cmd]
	for _, a := range args {
		name := a
		if i := indexByte(a, '='); i >= 0 {
			name = a[:i]
		}
		if table[name] {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: flag %s is not in this build yet\n", cmd, name)
			return true
		}
	}
	return false
}

// newFlagSet builds the flag.FlagSet every command parses its own flags
// with. usageLine is the "noahsark <cmd> ..." synopsis, and desc is a
// one-line description. -h and --help print this text and make
// flag.Parse return flag.ErrHelp, which the caller must turn into exit
// code 0, not the usual usage-error 2.
func newFlagSet(usageLine, desc string, stderr io.Writer) *flag.FlagSet {
	name, _, _ := strings.Cut(usageLine, " ")
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "usage: %s\n\n%s\n\nFlags:\n", usageLine, desc)
		fs.PrintDefaults()
	}
	return fs
}

// exitForFlagParse turns a flag.Parse error into the right exit code:
// 0 for -h or --help, 2 for every other usage error. Only call it when
// fs.Parse itself returned a non-nil error.
func exitForFlagParse(err error) int {
	if err == flag.ErrHelp {
		return 0
	}
	return 2
}

// checkPositionalsForFlags refuses a positional argument that names one
// of fs's own flags. The standard flag package stops parsing flags at
// the first positional argument, so a flag placed after a positional
// argument is read back as a plain string instead of being applied; this
// catches that case and reports it instead of silently misreading the
// flag as data.
func checkPositionalsForFlags(cmd string, fs *flag.FlagSet, stderr io.Writer) bool {
	for _, a := range fs.Args() {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if name == "" {
			continue
		}
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if fs.Lookup(name) != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: flags must come before positional arguments: %s\n", cmd, a)
			return true
		}
	}
	return false
}
