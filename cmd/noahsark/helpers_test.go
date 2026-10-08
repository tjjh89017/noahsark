package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeNow is the clock of every fake env. A test that needs a fixed time
// replaces it, and puts the old value back when it ends.
var fakeNow = time.Now

// fakeStdin is the standard input of every fake env. A test replaces it
// with setFakeStdin. nil gives an empty standard input.
var fakeStdin io.Reader

// fakeStdinTTY tells every fake env that its standard input is a
// terminal. A test sets it with setFakeTerminal.
var fakeStdinTTY bool

// setFakeStdin makes r the standard input of every fake env until the
// test ends.
func setFakeStdin(t *testing.T, r io.Reader) {
	t.Helper()
	old := fakeStdin
	fakeStdin = r
	t.Cleanup(func() { fakeStdin = old })
}

// setFakeTerminal makes standard input of every fake env a terminal that
// holds answer, until the test ends. answer is the text that the
// operator types, for example "y\n".
func setFakeTerminal(t *testing.T, answer string) {
	t.Helper()
	setFakeStdin(t, strings.NewReader(answer))
	old := fakeStdinTTY
	fakeStdinTTY = true
	t.Cleanup(func() { fakeStdinTTY = old })
}

// defaultRefName is the ref a commit moves with no --ref under the fake
// clock: the local date of today, as YYYY-MM-DD.
func defaultRefName() string {
	return fakeNow().Format("2006-01-02")
}

// testEnv is a fake env and the buffers that collect its output.
type testEnv struct {
	*env
	out    bytes.Buffer
	errOut bytes.Buffer
	vars   map[string]string
}

// newTestEnv returns a fake env whose working directory is dir. It has
// no environment variables, the clock fakeNow, the standard input
// fakeStdin, a terminal on standard input when fakeStdinTTY is true, the
// fake mount table, and the fake image build programs.
func newTestEnv(dir string) *testEnv {
	te := &testEnv{vars: map[string]string{}}
	stdin := fakeStdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	te.env = &env{
		stdout:    &te.out,
		stderr:    &te.errOut,
		stdin:     stdin,
		stdinTTY:  fakeStdinTTY,
		getwd:     func() (string, error) { return dir, nil },
		getenv:    func(k string) string { return te.vars[k] },
		now:       func() time.Time { return fakeNow() },
		euid:      os.Geteuid,
		mountinfo: fakeMountinfo,
		deviceOf:  fakeDeviceOf,
		imageHost: imageHost{mkudffsVersion: fakeMkudffsVersion, makeImage: fakeMakeImage},
	}
	return te
}

// run runs the CLI with args in the fake env. It returns the exit code
// and the text of standard output and then standard error of this run.
func (te *testEnv) run(args ...string) (int, string) {
	te.out.Reset()
	te.errOut.Reset()
	te.global = globalOptions{}
	code := run(te.env, args)
	return code, te.out.String() + te.errOut.String()
}

// runIn runs the CLI with args in a fake env whose working directory is
// dir. It creates dir first. It returns the exit code and the text of
// standard output and then standard error.
func runIn(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return newTestEnv(dir).run(args...)
}

// runCmd runs the CLI with args in a fake env whose working directory is
// a new empty directory. It returns the exit code and the text of
// standard output and then standard error.
func runCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	return newTestEnv(t.TempDir()).run(args...)
}

// setFakeNow sets the clock of every fake env until the test ends.
func setFakeNow(t *testing.T, now func() time.Time) {
	t.Helper()
	old := fakeNow
	t.Cleanup(func() { fakeNow = old })
	fakeNow = now
}
