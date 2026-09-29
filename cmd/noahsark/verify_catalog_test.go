package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestVerifyRepairsTheCatalog damages the root tree of the catalog with
// no change of its size. A counted verify of the good disc replaces it.
// When the catalog write fails, verify records nothing, prints no ok line
// and no bad line, and exits with code 1.
func TestVerifyRepairsTheCatalog(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscBurned)
	_, rootID, _ := rootTreeOf(t, fx.repo)
	c, err := catalog.OpenReadOnly(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	path := c.MetaPath(format.ObjectKindTree, rootID)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := bytes.Clone(good)
	bad[len(bad)-1] ^= 0x01
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(fx.repo, "state", "discstate.db")
	logBefore, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	te := newTestEnv(t.TempDir())
	code := run(te.env, []string{"--repo=" + fx.repo, "verify", fx.root})
	if code != 1 || te.out.String() != "" || !strings.Contains(te.errOut.String(), "the check is not recorded") {
		t.Fatalf("verify with a catalog that cannot be written: exit %d, stdout %q, stderr %q", code, te.out.String(), te.errOut.String())
	}
	if logAfter, _ := os.ReadFile(logPath); !bytes.Equal(logAfter, logBefore) {
		t.Fatal("verify wrote the disc state log after the catalog write failed")
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "verify", fx.root)
	if got, _ := os.ReadFile(path); !bytes.Equal(got, good) {
		t.Fatal("verify kept the damaged catalog tree")
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscVerified {
		t.Fatalf("disc state %s, want verified", got)
	}
}
