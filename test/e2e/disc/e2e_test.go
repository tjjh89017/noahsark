// Package disc is the disc e2e harness: real mkudffs images, a real loop
// mount, real corruption and healing, and the real noahsark binary. It
// runs one scenario against one media preset per test invocation, chosen
// by environment variables, so a CI matrix cell runs exactly one
// scenario/media pair.
//
// Run with:
//
//	sudo env "PATH=$PATH" NOAHSARK_E2E=1 \
//	  NOAHSARK_E2E_SCENARIO=verify-restore NOAHSARK_E2E_MEDIA=dvd+r \
//	  go test ./test/e2e/disc -v
package disc

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// scenarios lists the disc e2e suite's scenario names, matching run.sh.
var scenarios = map[string]bool{
	"verify-restore": true,
	"corrupt-heal":   true,
	"corrupt-parity": true,
	"cli":            true,
	"capacity":       true,
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
	if scenario == "" || media == "" {
		t.Fatal("NOAHSARK_E2E_SCENARIO and NOAHSARK_E2E_MEDIA are both required when NOAHSARK_E2E=1")
	}
	if !scenarios[scenario] {
		t.Fatalf("unknown NOAHSARK_E2E_SCENARIO: %s", scenario)
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

// TestDisc runs the scenario and media preset named by
// NOAHSARK_E2E_SCENARIO and NOAHSARK_E2E_MEDIA through run.sh. run.sh
// carries the scenario logic; this test is the harness's skip guard and
// its entry point from `go test`.
func TestDisc(t *testing.T) {
	scenario, media := requireHarness(t)

	cmd := exec.Command("bash", "./run.sh", scenario, media)
	out, err := cmd.CombinedOutput()
	t.Logf("run.sh %s %s:\n%s", scenario, media, out)
	if err != nil {
		t.Fatalf("scenario %s/%s failed: %v", scenario, media, err)
	}
}
