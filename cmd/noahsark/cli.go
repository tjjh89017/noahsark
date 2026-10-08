package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"strings"
)

// runFunc runs one command with its positional arguments. It returns the
// exit code: 0 for success, 1 for a failure at run time, 2 for a usage
// error.
type runFunc func(e *env, args []string) int

// command is one entry of the command registry.
type command struct {
	// name is the command word, or the subcommand word in a group.
	name string
	// group is the group word, or empty for a command outside a group.
	group string
	// usage is the synopsis after "noahsark ". It can hold more than
	// one line.
	usage   string
	summary string
	// flags defines the command options on fs and returns the function
	// that runs the command with the parsed values.
	flags func(fs *flag.FlagSet) runFunc
}

// path returns the command words: "status" or "disc burned".
func (c *command) path() string {
	if c.group == "" {
		return c.name
	}
	return c.group + " " + c.name
}

// groups maps each command group to what it does. A group has no
// options of its own, other than -h.
var groups = map[string]string{
	"disc":  "Change the records of one disc.",
	"image": "Build a disc image.",
}

// commands is the command registry. Each command file adds its commands
// in init.
var commands []*command

func register(c *command) {
	commands = append(commands, c)
}

// lookupCommand returns the command with this group and name, or nil.
func lookupCommand(group, name string) *command {
	for _, c := range commands {
		if c.group == group && c.name == name {
			return c
		}
	}
	return nil
}

// subcommands returns the commands of a group, sorted by name.
func subcommands(group string) []*command {
	var out []*command
	for _, c := range commands {
		if c.group == group {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b *command) int { return strings.Compare(a.name, b.name) })
	return out
}

// globalOptionNames lists the global options for a usage message.
const globalOptionNames = "--repo=PATH, -q, --quiet, --yes, --force-yes, -h, --version"

// isHelp reports whether arg asks for help.
func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help"
}

// globalOptionName returns the name of the global option that arg
// gives, or an empty string. -h is not in the set: it is valid in both
// positions.
func globalOptionName(arg string) string {
	name, _, _ := strings.Cut(arg, "=")
	switch name {
	case "--repo", "-q", "--quiet", "--yes", "--force-yes", "--version":
		return name
	}
	return ""
}

// run parses the command line and runs one command. It returns the exit
// code.
func run(e *env, args []string) int {
	help := false
	i := 0
parseGlobals:
	for ; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch {
		case isHelp(arg):
			help = true
		case arg == "--version":
			printVersion(e.stdout)
			return 0
		case arg == "-q" || arg == "--quiet":
			e.global.quiet = true
		case arg == "--yes":
			e.global.yes = true
		case arg == "--force-yes":
			e.global.forceYes = true
		case strings.HasPrefix(arg, "--repo="):
			e.global.repo = strings.TrimPrefix(arg, "--repo=")
			if e.global.repo == "" {
				_, _ = fmt.Fprintln(e.stderr, "noahsark: --repo needs a path: --repo=PATH")
				return 2
			}
		case arg == "--repo":
			if i+1 >= len(args) || args[i+1] == "" {
				_, _ = fmt.Fprintln(e.stderr, "noahsark: --repo needs a path: --repo=PATH")
				return 2
			}
			i++
			e.global.repo = args[i]
		case arg == "--":
			i++
			break parseGlobals
		default:
			return refuseOptionBeforeCommand(e, args[:i], arg, args[i+1:])
		}
	}
	globals, words := args[:i], args[i:]

	if len(words) == 0 {
		if help {
			printTopHelp(e.stdout)
			return 0
		}
		printTopHelp(e.stderr)
		return 2
	}

	cmd, rest, code, ok := resolveCommand(e, globals, words, help)
	if !ok {
		return code
	}
	if help {
		printCommandHelp(e.stdout, cmd)
		return 0
	}
	return runCommand(e, globals, cmd, rest)
}

// resolveCommand finds the command that words name. It returns the
// command and the words after it. When it prints help or a usage error
// instead, ok is false and code is the exit code.
func resolveCommand(e *env, globals, words []string, help bool) (cmd *command, rest []string, code int, ok bool) {
	name := words[0]
	if _, isGroup := groups[name]; !isGroup {
		cmd = lookupCommand("", name)
		if cmd == nil {
			_, _ = fmt.Fprintf(e.stderr, "noahsark: unknown command %q\n", name)
			printTopHelp(e.stderr)
			return nil, nil, 2, false
		}
		return cmd, words[1:], 0, true
	}

	group := name
	if len(words) == 1 {
		if help {
			printGroupHelp(e.stdout, group)
			return nil, nil, 0, false
		}
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s needs a subcommand:\n", group)
		printSubcommands(e.stderr, group)
		return nil, nil, 2, false
	}
	sub := words[1]
	if isHelp(sub) {
		printGroupHelp(e.stdout, group)
		return nil, nil, 0, false
	}
	if strings.HasPrefix(sub, "-") {
		return nil, nil, refuseOptionAfterGroup(e, globals, group, sub, words[2:]), false
	}
	cmd = lookupCommand(group, sub)
	if cmd == nil {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: unknown subcommand of %s: %s; the subcommands are:\n", group, sub)
		printSubcommands(e.stderr, group)
		return nil, nil, 2, false
	}
	return cmd, words[2:], 0, true
}

// refuseOptionAfterGroup refuses an option between a group word and its
// subcommand.
func refuseOptionAfterGroup(e *env, globals []string, group, opt string, after []string) int {
	if globalOptionName(opt) != "" {
		cmdWords := []string{group}
		if len(after) > 0 {
			cmdWords = append(cmdWords, after[0])
			after = after[1:]
		}
		refuseGlobalAfterCommand(e, globals, opt, cmdWords, after)
		return 2
	}
	if len(after) > 0 {
		if cmd := lookupCommand(group, after[0]); cmd != nil && hasFlag(cmd, opt) {
			refuseCommandOption(e, globals, cmd, opt, after[1:])
			return 2
		}
	}
	_, _ = fmt.Fprintf(e.stderr, "noahsark: %s takes no option %s; the subcommands are:\n", group, opt)
	printSubcommands(e.stderr, group)
	return 2
}

// refuseOptionBeforeCommand refuses an option before the command name
// that is not a global option.
func refuseOptionBeforeCommand(e *env, globals []string, opt string, after []string) int {
	var words []string
	for _, a := range after {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	if len(words) > 0 {
		cmd := lookupCommand("", words[0])
		if _, isGroup := groups[words[0]]; isGroup && len(words) > 1 {
			cmd = lookupCommand(words[0], words[1])
		}
		if cmd != nil && hasFlag(cmd, opt) {
			n := len(strings.Fields(cmd.path()))
			refuseCommandOption(e, globals, cmd, opt, words[n:])
			return 2
		}
	}
	_, _ = fmt.Fprintf(e.stderr, "noahsark: unknown global option %s; the global options are %s\n", opt, globalOptionNames)
	return 2
}

// refuseCommandOption prints the usage error for a command option that
// comes before the last subcommand word.
func refuseCommandOption(e *env, globals []string, cmd *command, opt string, rest []string) {
	line := joinLine(globals, strings.Fields(cmd.path()), []string{opt}, rest)
	_, _ = fmt.Fprintf(e.stderr, "noahsark: %s is an option of %s; give it after the last subcommand word: %s; see: noahsark %s -h\n",
		optionName(opt), cmd.path(), line, cmd.path())
}

// refuseGlobalAfterCommand prints the usage error for a global option
// that comes after the command name.
func refuseGlobalAfterCommand(e *env, globals []string, opt string, cmdWords, rest []string) {
	line := joinLine(append(slices.Clone(globals), opt), cmdWords, rest)
	_, _ = fmt.Fprintf(e.stderr, "noahsark: %s is a global option; give it before the command name: %s\n",
		globalOptionName(opt), line)
}

// joinLine builds a command line from its parts.
func joinLine(parts ...[]string) string {
	words := []string{"noahsark"}
	for _, p := range parts {
		words = append(words, p...)
	}
	return strings.Join(words, " ")
}

// optionName returns the option name of arg, without a value.
func optionName(arg string) string {
	name, _, _ := strings.Cut(arg, "=")
	return name
}

// newCommandFlagSet returns a flag set for cmd that prints nothing by
// itself, and the run function of cmd.
func newCommandFlagSet(cmd *command) (*flag.FlagSet, runFunc) {
	fs := flag.NewFlagSet(cmd.path(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs, cmd.flags(fs)
}

// hasFlag reports whether opt is an option of cmd.
func hasFlag(cmd *command, opt string) bool {
	fs, _ := newCommandFlagSet(cmd)
	name := strings.TrimLeft(optionName(opt), "-")
	return name != "" && fs.Lookup(name) != nil
}

// runCommand parses the command options of cmd and runs it.
func runCommand(e *env, globals []string, cmd *command, rest []string) int {
	fs, runFn := newCommandFlagSet(cmd)
	cmdWords := strings.Fields(cmd.path())

	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" {
			break
		}
		if globalOptionName(arg) != "" && fs.Lookup(strings.TrimLeft(optionName(arg), "-")) == nil {
			refuseGlobalAfterCommand(e, globals, arg, cmdWords, slices.Delete(slices.Clone(rest), i, i+1))
			return 2
		}
		if takesSeparateValue(fs, arg) {
			i++
		}
	}

	err := fs.Parse(rest)
	if errors.Is(err, flag.ErrHelp) {
		printCommandHelp(e.stdout, cmd)
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd.path(), err)
		printUsageLine(e.stderr, cmd)
		return 2
	}
	if checkPositionalsForFlags(cmd.path(), fs, e.stderr) {
		return 2
	}
	return runFn(e, fs.Args())
}

// takesSeparateValue reports whether arg is an option of fs whose value
// is the next argument.
func takesSeparateValue(fs *flag.FlagSet, arg string) bool {
	if !strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
		return false
	}
	f := fs.Lookup(strings.TrimLeft(arg, "-"))
	if f == nil {
		return false
	}
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return false
	}
	return true
}

// checkPositionalsForFlags refuses a positional argument that names one
// of the options of fs. The flag package stops at the first positional
// argument, so it reads a later option as a plain string.
func checkPositionalsForFlags(cmd string, fs *flag.FlagSet, stderr io.Writer) bool {
	for _, a := range fs.Args() {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimLeft(optionName(a), "-")
		if name == "" {
			continue
		}
		if fs.Lookup(name) != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: flags must come before positional arguments: %s\n", cmd, a)
			return true
		}
	}
	return false
}

// printVersion prints the module version from the build information.
func printVersion(w io.Writer) {
	version := "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		version = bi.Main.Version
	}
	_, _ = fmt.Fprintf(w, "noahsark %s\n", version)
}

// printTopHelp lists the global options, the commands and the groups.
func printTopHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, "usage: noahsark [GLOBAL-OPTIONS] COMMAND [SUBCOMMAND...] [COMMAND-OPTIONS] [ARGUMENTS]\n\n")
	_, _ = fmt.Fprintln(w, "Global options:")
	_, _ = fmt.Fprintln(w, "  --repo=PATH      the repository")
	_, _ = fmt.Fprintln(w, "  -q, --quiet      print no progress line")
	_, _ = fmt.Fprintln(w, "  --yes            answer yes to an ordinary confirmation")
	_, _ = fmt.Fprintln(w, "  --force-yes      answer yes to an ordinary and to a critical confirmation")
	_, _ = fmt.Fprintln(w, "  -h, --help       print help and exit")
	_, _ = fmt.Fprintln(w, "  --version        print the version and exit")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Commands, in the order of the work:")
	for _, name := range workflowOrder {
		summary, ok := groups[name]
		if ok {
			var subs []string
			for _, c := range subcommands(name) {
				subs = append(subs, c.name)
			}
			summary += " Subcommands: " + strings.Join(subs, ", ") + "."
		} else if c := lookupCommand("", name); c != nil {
			summary = c.summary
		}
		_, _ = fmt.Fprintf(w, "  %-15s  %s\n", name, summary)
	}
	_, _ = fmt.Fprint(w, "\n\"noahsark status\" prints the next step.\n")
	_, _ = fmt.Fprint(w, "Run \"noahsark COMMAND -h\" for the options of a command.\n")
}

// workflowOrder lists the commands and the groups in the order that an
// operator uses them, for the command list of the help.
var workflowOrder = []string{"init", "commit", "status", "pack", "image", "disc", "verify", "gc", "restore", "recover", "ls", "log"}

// printGroupHelp prints the subcommands of a group.
func printGroupHelp(w io.Writer, group string) {
	_, _ = fmt.Fprintf(w, "usage: noahsark %s SUBCOMMAND [OPTIONS] [ARGUMENTS]\n\n", group)
	_, _ = fmt.Fprintln(w, "Subcommands:")
	printSubcommands(w, group)
	_, _ = fmt.Fprintf(w, "\nRun \"noahsark %s SUBCOMMAND -h\" for the options of a subcommand.\n", group)
}

// printSubcommands prints one line for each subcommand of a group.
func printSubcommands(w io.Writer, group string) {
	for _, c := range subcommands(group) {
		_, _ = fmt.Fprintf(w, "  %-10s  %s\n", c.name, c.summary)
	}
}

// printUsageLine prints the synopsis of cmd.
func printUsageLine(w io.Writer, cmd *command) {
	_, _ = fmt.Fprintf(w, "usage: noahsark %s\n", strings.ReplaceAll(cmd.usage, "\n", "\n       noahsark "))
}

// printCommandHelp prints the synopsis, the summary and the options of
// cmd.
func printCommandHelp(w io.Writer, cmd *command) {
	printUsageLine(w, cmd)
	_, _ = fmt.Fprintf(w, "\n%s\n", cmd.summary)
	fs, _ := newCommandFlagSet(cmd)
	hasOptions := false
	fs.VisitAll(func(*flag.Flag) { hasOptions = true })
	if !hasOptions {
		return
	}
	_, _ = fmt.Fprintln(w, "\nOptions:")
	fs.VisitAll(func(f *flag.Flag) {
		_, _ = fmt.Fprintf(w, "  %s\n        %s\n", optionSpelling(f, cmd.usage), f.Usage)
	})
}

// optionSpelling gives the form of option f that the operator types, as
// the synopsis writes it: --name for a switch, --name=VALUE for an option
// with a value, and -n VALUE for a one-letter option with a value. VALUE
// is the placeholder of the synopsis usage, else VALUE.
func optionSpelling(f *flag.Flag, usage string) string {
	dash := "--"
	if len(f.Name) == 1 {
		dash = "-"
	}
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return dash + f.Name
	}
	sep := "="
	if dash == "-" {
		sep = " "
	}
	value := "VALUE"
	if _, after, ok := strings.Cut(usage, dash+f.Name+sep); ok {
		if end := strings.IndexFunc(after, func(r rune) bool { return (r < 'A' || r > 'Z') && r != '-' }); end > 0 {
			value = after[:end]
		} else if end < 0 && after != "" {
			value = after
		}
	}
	return dash + f.Name + sep + value
}
