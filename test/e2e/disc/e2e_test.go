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
}

func requireHarness(t *testing.T) (scenario, media string) {
	t.Helper()
	if os.Getenv("NOAHSARK_E2E") != "1" {
		t.Skip("set NOAHSARK_E2E=1 to run the disc e2e suite")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the disc e2e suite is Linux-only (mkudffs, loop mount)")
	}
	scenario = os.Getenv("NOAHSARK_E2E_SCENARIO")
	media = os.Getenv("NOAHSARK_E2E_MEDIA")
	if scenario == "" {
		t.Fatal("NOAHSARK_E2E_SCENARIO is required when NOAHSARK_E2E=1")
	}
	if !scenarios[scenario] {
		t.Fatalf("unknown NOAHSARK_E2E_SCENARIO: %s", scenario)
	}
	if scenario == "media" && media == "" {
		t.Fatal("NOAHSARK_E2E_MEDIA is required for the media scenario")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root (loop mount)")
	}
	for _, bin := range []string{"mkudffs", "mount", "sudo"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not in PATH", bin)
		}
	}
	return scenario, media
}

// TestDisc runs the scenario named by NOAHSARK_E2E_SCENARIO (and, for
// the media scenario, the media preset named by NOAHSARK_E2E_MEDIA)
// through run.sh. run.sh carries the scenario logic; this test is the
// harness's skip guard and its entry point from `go test`.
func TestDisc(t *testing.T) {
	scenario, media := requireHarness(t)

	cmd := exec.Command("bash", "./run.sh", scenario, media)
	out, err := cmd.CombinedOutput()
	t.Logf("run.sh %s %s:\n%s", scenario, media, out)
	if err != nil {
		t.Fatalf("scenario %s (media %q) failed: %v", scenario, media, err)
	}
}
