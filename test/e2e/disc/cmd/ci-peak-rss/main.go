// Command ci-peak-rss is CI-only tooling, not a NoahsArk command surface.
// It runs one command with the standard streams of the caller. Then it
// appends one line to LOG: the subcommand and the peak resident set of
// the command in KiB. The peak includes each child that the command
// waited for. It exits with the exit code of the command.
//
// The subcommand is the first argument after COMMAND that does not start
// with "-".
//
// Usage: ci-peak-rss LOG COMMAND [ARG...]
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func main() {
	if len(os.Args) < 3 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: ci-peak-rss LOG COMMAND [ARG...]")
		os.Exit(2)
	}
	logPath, args := os.Args[1], os.Args[2:]

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		_, _ = fmt.Fprintln(os.Stderr, "ci-peak-rss:", err)
		os.Exit(1)
	}

	usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		_, _ = fmt.Fprintln(os.Stderr, "ci-peak-rss: this platform gives no resource usage")
		os.Exit(1)
	}
	if err := appendLine(logPath, fmt.Sprintf("%s %d\n", subcommand(args[1:]), usage.Maxrss)); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ci-peak-rss:", err)
		os.Exit(1)
	}
	os.Exit(cmd.ProcessState.ExitCode())
}

func subcommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return "-"
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
