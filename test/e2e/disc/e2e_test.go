// Package disc is the disc e2e harness: real mkudffs images, a real loop
// mount, and the real noahsark binary. It runs one scenario per test
// invocation, chosen by environment variables, so a CI matrix cell runs
// exactly one scenario (and, for the media scenario, one media preset).
//
// Run with:
//
//	sudo env "PATH=$PATH" NOAHSARK_E2E=1 \
//	  NOAHSARK_E2E_SCENARIO=media NOAHSARK_E2E_MEDIA=dvd+r \
//	  go test ./test/e2e/disc -v
package disc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// scenarios lists the disc e2e suite's scenario names, matching run.sh.
// Only "media" reads NOAHSARK_E2E_MEDIA; the others use a fixed dvd+r
// fixture.
var scenarios = map[string]bool{
	"media":       true,
	"cli":         true,
	"iso":         true,
	"chain":       true,
	"lowmem":      true,
	"incremental": true,
	"rebuild":     true,
	"lifecycle":   true,
}

// requireHarness skips the test when NOAHSARK_E2E is not 1. When it is
// 1, a missing prerequisite fails the test: a skip would give a green
// CI cell that ran no scenario.
func requireHarness(t *testing.T) (scenario, media, order, extras string) {
	t.Helper()
	if os.Getenv("NOAHSARK_E2E") != "1" {
		t.Skip("set NOAHSARK_E2E=1 to run the disc e2e suite")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("NOAHSARK_E2E=1 is set, but the disc e2e suite is Linux-only (mkudffs, loop mount)")
	}
	scenario = os.Getenv("NOAHSARK_E2E_SCENARIO")
	media = os.Getenv("NOAHSARK_E2E_MEDIA")
	order = os.Getenv("NOAHSARK_E2E_ORDER")
	extras = os.Getenv("NOAHSARK_E2E_EXTRAS")
	if scenario == "" {
		t.Fatal("NOAHSARK_E2E_SCENARIO is required when NOAHSARK_E2E=1")
	}
	if !scenarios[scenario] {
		t.Fatalf("unknown NOAHSARK_E2E_SCENARIO: %s", scenario)
	}
	if scenario == "media" && media == "" {
		t.Fatal("NOAHSARK_E2E_MEDIA is required for the media scenario")
	}
	if scenario == "chain" && order == "" {
		t.Fatal("NOAHSARK_E2E_ORDER is required for the chain scenario")
	}
	if os.Geteuid() != 0 {
		t.Fatal("NOAHSARK_E2E=1 is set, but the process is not root; the loop mount needs root")
	}
	for _, bin := range []string{"mkudffs", "mount", "sudo"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("NOAHSARK_E2E=1 is set, but %s is not in PATH", bin)
		}
	}
	return scenario, media, order, extras
}

// TestDisc runs the scenario named by NOAHSARK_E2E_SCENARIO (and, for
// the media scenario, the media preset named by NOAHSARK_E2E_MEDIA, or
// for the chain scenario, the disc order named by NOAHSARK_E2E_ORDER,
// plus any extra scenarios named by NOAHSARK_E2E_EXTRAS) through
// run.sh. run.sh carries the scenario logic; this test is the harness's
// skip guard and its entry point from `go test`.
func TestDisc(t *testing.T) {
	scenario, media, order, extras := requireHarness(t)

	if err := runScenario(scenario, media, order, extras); err != nil {
		t.Fatalf("scenario %s (media %q, order %q, extras %q) failed: %v", scenario, media, order, extras, err)
	}
}

// runScenario runs run.sh with the given arguments, streaming its output
// live to stdout as it happens, so a `go test -v` run shows progress
// instead of one block of text after the run finishes. It also keeps a
// copy of the output, for a failure message. Standard output and
// standard error of run.sh share one scenarioOutput value: os/exec then
// uses one pipe and one copy goroutine for both.
func runScenario(args ...string) error {
	return runWithOutput(exec.Command("bash", append([]string{"./run.sh"}, args...)...), os.Stdout)
}

// runWithOutput runs cmd with its standard output and standard error
// copied to live through one scenarioOutput.
func runWithOutput(cmd *exec.Cmd, live io.Writer) error {
	out := &scenarioOutput{live: live}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\noutput:\n%s", err, out.captured.String())
	}
	return nil
}

// scenarioOutput copies the output of run.sh to live and keeps a copy.
// Write never returns an error. When the copy goroutine of os/exec gets
// an error, it stops and closes the read end of its pipe, and the next
// write of run.sh or of a command that it runs dies of SIGPIPE: exit
// status 141. A failed write to live thus drops only the live copy.
type scenarioOutput struct {
	live     io.Writer
	captured bytes.Buffer
}

func (o *scenarioOutput) Write(p []byte) (int, error) {
	_, _ = o.live.Write(p)
	_, _ = o.captured.Write(p)
	return len(p), nil
}

// brokenWriter fails each write, as a closed log pipe does.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// TestRunWithOutputSurvivesABrokenLiveCopy runs a command that writes
// many lines to both standard output and standard error while each write
// to the live copy fails. The command must not die of SIGPIPE, and the
// kept copy must hold the last line of both streams. This test needs no
// root and no harness.
func TestRunWithOutputSurvivesABrokenLiveCopy(t *testing.T) {
	script := `for i in $(seq 1 2000); do echo "out $i"; echo "err $i" >&2; done; echo out-end; echo err-end >&2`
	cmd := exec.Command("bash", "-c", script)
	out := &scenarioOutput{live: brokenWriter{}}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		t.Fatalf("the command failed: %v", err)
	}
	got := out.captured.String()
	for _, want := range []string{"out-end\n", "err-end\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the kept copy does not hold %q", want)
		}
	}
	if err := runWithOutput(exec.Command("bash", "-c", script), brokenWriter{}); err != nil {
		t.Fatalf("runWithOutput: %v", err)
	}
}

// TestChainSmall runs the chain scenario at a small, fast fixture size,
// with tiny forced capacities in place of the real media presets, so
// the chain flow gets exercised on every push without an hours-long
// full-size run. It rides in the media/dvd+r cell instead of its own
// matrix entry: it only runs when that cell's NOAHSARK_E2E_SCENARIO is
// "media" at media preset "dvd+r", the smallest and fastest cell.
func TestChainSmall(t *testing.T) {
	scenario, media, _, _ := requireHarness(t)
	if scenario != "media" || media != "dvd+r" {
		t.Skip("chain-small rides in the media/dvd+r e2e cell")
	}

	if err := runScenario("chain-small", "", "dvd-bd25-bd10", ""); err != nil {
		t.Fatalf("chain-small failed: %v", err)
	}
}
