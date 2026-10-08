package main

import (
	"io"
	"os"
	"time"

	"github.com/tjjh89017/noahsark/internal/progress"
)

// env holds every link of a command to the process and the host. main
// builds the real env. A test builds a fake env and runs the CLI in one
// process.
type env struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader

	// stdinTTY and stderrTTY tell whether standard input and standard
	// error are terminals.
	stdinTTY  bool
	stderrTTY bool

	getwd  func() (string, error)
	getenv func(string) string
	now    func() time.Time
	euid   func() int

	// mountinfo opens the mount table of the process.
	mountinfo func() (io.ReadCloser, error)
	// deviceOf returns the device of the file at path, after symlinks.
	deviceOf func(path string) (devNum, error)
	// imageHost holds the host programs that image build runs.
	imageHost imageHost

	global globalOptions
}

// globalOptions holds the options that come before the command name.
type globalOptions struct {
	repo     string
	quiet    bool
	yes      bool
	forceYes bool
}

// realEnv returns the env of the running process.
func realEnv() *env {
	return &env{
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		stdin:     os.Stdin,
		stdinTTY:  isTerminal(os.Stdin),
		stderrTTY: isTerminal(os.Stderr),
		getwd:     os.Getwd,
		getenv:    os.Getenv,
		now:       time.Now,
		euid:      os.Geteuid,
		mountinfo: func() (io.ReadCloser, error) { return os.Open("/proc/self/mountinfo") },
		deviceOf:  statDevice,
		imageHost: realImageHost(),
	}
}

// progress returns the reporter of a long command, or nil. A progress
// line goes to standard error only when standard error is a terminal and
// -q is not given.
func (e *env) progress() *progress.Reporter {
	if e.global.quiet || !e.stderrTTY {
		return nil
	}
	return progress.New(e.stderr)
}
