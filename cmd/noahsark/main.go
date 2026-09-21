// Command noahsark is the NoahsArk command-line tool. OPERATIONS.md's CLI
// reference lists its commands. An unknown command name or flag is a usage
// error.
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
// error.
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
	case "recover":
		return cmdRecover(rest, stdout, stderr, prog)
	case "status":
		return cmdStatus(rest, stdout, stderr)
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

// extractProgressFlags pulls --no-progress and --quiet (-q) out of args,
// wherever they appear, before or after the command name, and returns
// the remaining arguments plus the Reporter to use: nil when progress
// reporting is off. Without --no-progress or --quiet, progress is on
// only when the real process stderr is a terminal, so a command run
// into a file or a pipe writes no progress noise onto it.
func extractProgressFlags(args []string, stderr io.Writer) ([]string, *progress.Reporter, error) {
	var forceOff, quiet bool
	remaining := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--no-progress":
			forceOff = true
		case "--quiet", "-q":
			quiet = true
		default:
			remaining = append(remaining, a)
		}
	}
	if quiet || forceOff {
		return remaining, nil, nil
	}
	if stderrIsTerminal() {
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

Commands:
  init           create a repository directory
  commit         stage a source directory as a snapshot
  pack           write staged snapshots onto the next disc
  image build    build a disc image from a run tree
  verify         check a disc image, or heal it from a second copy
  restore        restore a snapshot from a disc, or from a mounted drive
  ls             list a snapshot's tree
  log            list a repository's snapshots
  plan           plan a restore's disc order from the local cache
  status         show what is staged, every disc's state, and what to do next
  recover        rebuild a repository's state log and ledgers from discs
  disc           mark a disc burned, or undo that mark
  gc             delete staging bytes past their retention period

Every command also accepts --no-progress and --quiet (-q), which turn
off the progress line a long-running command writes to stderr. Progress
is on by default only when stderr is a terminal.

Run "noahsark <command> -h" for a command's own flags.
`)
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
