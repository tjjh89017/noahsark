package image

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/tjjh89017/noahsark/internal/progress"
)

// MinUDFToolsMajor and MinUDFToolsMinor are the pinned minimum udftools
// version. A build refuses an older or unpatched mkudffs.
const (
	MinUDFToolsMajor = 2
	MinUDFToolsMinor = 3
)

// udfToolsVersionRe matches the "mkudffs from udftools X.Y" banner
// mkudffs prints to stderr on every invocation, including an invalid one.
// mkudffs has no --version option, so CheckTools reads this banner
// instead.
var udfToolsVersionRe = regexp.MustCompile(`udftools (\d+)\.(\d+)`)

// CheckTools runs mkudffs and parses its reported udftools version from
// its startup banner. It refuses a version below
// MinUDFToolsMajor.MinUDFToolsMinor.
func CheckTools() (string, error) {
	// mkudffs prints its banner and a usage message, then exits nonzero,
	// when it is run with no device argument. That is the only reliable
	// way to read its version: it has no --version option.
	out, runErr := exec.Command("mkudffs").CombinedOutput()
	m := udfToolsVersionRe.FindStringSubmatch(string(out))
	if m == nil {
		if runErr != nil && len(out) == 0 {
			return "", fmt.Errorf("image: mkudffs: %w", runErr)
		}
		return "", fmt.Errorf("image: could not parse mkudffs version from: %s", strings.TrimSpace(string(out)))
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < MinUDFToolsMajor || (major == MinUDFToolsMajor && minor < MinUDFToolsMinor) {
		return m[0], fmt.Errorf("image: udftools %s is older than the required %d.%d", m[0], MinUDFToolsMajor, MinUDFToolsMinor)
	}
	return m[0], nil
}

// ciEnvVar, when set to a nonempty value, tells MakeImage it is running
// under CI: it may shell out to sudo for the loop-mount populate step.
// Outside CI, MakeImage builds the empty UDF image and returns, so a
// developer machine with no root never blocks on this step.
const ciEnvVar = "NOAHSARK_CI"

// MakeImage builds a UDF 2.01 image of length sectors at imagePath and
// populates it with the tree at dir. mkudffs makes only an empty
// filesystem; populating it needs a loop mount, which needs root. Under
// CI (ciEnvVar set) MakeImage shells out to populate.sh, which uses sudo
// to mount, copy and unmount. Outside CI it builds the empty image and
// returns, so a build with no root still exercises the mkudffs step.
// prog reports bytes copied during populate; a nil prog reports nothing.
//
// Reading: docs/decisions.md, "Profile 0 image build: how the volume is
// populated" records why this is the chosen path over a from-scratch Go
// UDF writer.
func MakeImage(dir, imagePath string, sectors uint64, prog *progress.Reporter) error {
	if _, err := CheckTools(); err != nil {
		return err
	}
	if sectors == 0 {
		return fmt.Errorf("image: sectors must not be zero")
	}
	size := sectors * SectorSize
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(imagePath)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(size)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	cmd := exec.Command("mkudffs",
		"--utf8",
		"--media-type=hd",
		"--blocksize=2048",
		"--udfrev=2.01",
		"--uid=0",
		"--gid=0",
		"--mode=0555",
		"--bootarea=erase",
		imagePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("image: mkudffs: %w: %s", err, out)
	}

	if os.Getenv(ciEnvVar) == "" {
		return nil
	}
	return populateImageCI(dir, imagePath, prog)
}

// populateImageCI mounts imagePath with sudo, copies dir's tree onto it
// as /NOAHSARK file by file, and unmounts. It runs only under CI. prog
// reports bytes copied, using populate.sh's "COPIED <bytes>" line for
// each regular file it finishes; the total is dir's own regular-file
// byte sum, computed before the script runs.
func populateImageCI(dir, imagePath string, prog *progress.Reporter) error {
	total := regularFileBytesUnder(filepath.Join(dir, "NOAHSARK"))

	_, thisFile, _, _ := runtime.Caller(0)
	scriptPath := filepath.Join(filepath.Dir(thisFile), "populate.sh")
	cmd := exec.Command("sudo", "bash", scriptPath, imagePath, dir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("image: populate: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("image: populate: %w", err)
	}

	prog.Start("image build: populate", total)
	var captured bytes.Buffer
	sc := bufio.NewScanner(io.TeeReader(stdout, &captured))
	for sc.Scan() {
		var n int64
		if _, err := fmt.Sscanf(sc.Text(), "COPIED %d", &n); err == nil {
			prog.Add(n)
		}
	}
	prog.Done()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("image: populate: %w: %s%s", err, captured.String(), stderr.String())
	}
	return nil
}

// regularFileBytesUnder sums the size of every regular file under root.
// A missing or unreadable root sums to zero rather than failing the
// caller: it feeds a progress total, not a correctness check.
func regularFileBytesUnder(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
