// Package disc is the disc e2e harness: real mkudffs images, a real loop
// mount, real corruption and healing, and the real noahsark binary. It
// runs one scenario per test invocation, chosen by environment
// variables, so a CI matrix cell runs exactly one scenario (and, for the
// media scenario, one media preset).
//
// Run with:
//
//	sudo env "PATH=$PATH" NOAHSARK_E2E=1 \
//	  NOAHSARK_E2E_SCENARIO=media NOAHSARK_E2E_MEDIA=dvd+r \
//	  go test ./test/e2e/disc -v
package disc

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// scenarios lists the disc e2e suite's scenario names, matching run.sh.
// Only "media" reads NOAHSARK_E2E_MEDIA; the others use a fixed dvd+r
// fixture.
var scenarios = map[string]bool{
	"media":          true,
	"corrupt-heal":   true,
	"corrupt-parity": true,
	"corrupt-max":    true,
	"corrupt-over":   true,
	"cli":            true,
	"iso":            true,
	"chain":          true,
	"lowmem":         true,
}

func requireHarness(t *testing.T) (scenario, media, order string) {
	t.Helper()
	if os.Getenv("NOAHSARK_E2E") != "1" {
		t.Skip("set NOAHSARK_E2E=1 to run the disc e2e suite")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the disc e2e suite is Linux-only (mkudffs, loop mount)")
	}
	scenario = os.Getenv("NOAHSARK_E2E_SCENARIO")
	media = os.Getenv("NOAHSARK_E2E_MEDIA")
	order = os.Getenv("NOAHSARK_E2E_ORDER")
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
		t.Skip("requires root (loop mount)")
	}
	for _, bin := range []string{"mkudffs", "mount", "sudo"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not in PATH", bin)
		}
	}
	return scenario, media, order
}

// TestDisc runs the scenario named by NOAHSARK_E2E_SCENARIO (and, for
// the media scenario, the media preset named by NOAHSARK_E2E_MEDIA, or
// for the chain scenario, the disc order named by NOAHSARK_E2E_ORDER)
// through run.sh. run.sh carries the scenario logic; this test is the
// harness's skip guard and its entry point from `go test`.
func TestDisc(t *testing.T) {
	scenario, media, order := requireHarness(t)

	if err := runScenario(scenario, media, order); err != nil {
		t.Fatalf("scenario %s (media %q, order %q) failed: %v", scenario, media, order, err)
	}
}

// runScenario runs run.sh with the given arguments, streaming its output
// live to stdout and stderr as it happens, so a `go test -v` run shows
// progress instead of one block of text after the run finishes. It also
// keeps a copy of the combined output, for a failure message.
func runScenario(args ...string) error {
	cmd := exec.Command("bash", append([]string{"./run.sh"}, args...)...)
	var captured bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &captured)
	cmd.Stderr = io.MultiWriter(os.Stderr, &captured)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\noutput:\n%s", err, captured.String())
	}
	return nil
}

// TestChainSmall runs the chain scenario at a small, fast fixture size,
// with tiny forced capacities in place of the real media presets, so
// the chain flow gets exercised on every push without an hours-long
// full-size run. It rides in the cli cell instead of its own matrix
// entry: it only runs when that cell's NOAHSARK_E2E_SCENARIO is "cli".
func TestChainSmall(t *testing.T) {
	scenario, _, _ := requireHarness(t)
	if scenario != "cli" {
		t.Skip("chain-small rides in the cli e2e cell")
	}

	if err := runScenario("chain-small", "", "dvd-bd25-bd10"); err != nil {
		t.Fatalf("chain-small failed: %v", err)
	}
}
