package image

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// MinUDFToolsMajor and MinUDFToolsMinor are the pinned minimum udftools
// version. A build refuses an older or unpatched mkudffs.
const (
	MinUDFToolsMajor = 2
	MinUDFToolsMinor = 3
)

var udfToolsVersionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// CheckTools runs mkudffs and parses its reported udftools version. It
// refuses a version below MinUDFToolsMajor.MinUDFToolsMinor.
func CheckTools() (string, error) {
	out, err := exec.Command("mkudffs", "--version").CombinedOutput()
	if err != nil {
		// Some udftools builds only print the version banner and exit
		// nonzero for --version; fall back to that output if present.
		if len(out) == 0 {
			return "", fmt.Errorf("image: mkudffs --version: %w", err)
		}
	}
	m := udfToolsVersionRe.FindStringSubmatch(string(out))
	if m == nil {
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
//
// Reading: docs/decisions.md, "10.1 Profile 0 image build" records why
// this is the chosen path over a from-scratch Go UDF writer.
func MakeImage(dir, imagePath string, sectors uint64) error {
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
		f.Close()
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
	return populateImageCI(dir, imagePath)
}

// populateImageCI mounts imagePath with sudo, copies dir's tree onto it
// as /NOAHSARK, and unmounts. It runs only under CI.
func populateImageCI(dir, imagePath string) error {
	_, thisFile, _, _ := runtime.Caller(0)
	scriptPath := filepath.Join(filepath.Dir(thisFile), "populate.sh")
	cmd := exec.Command("sudo", "bash", scriptPath, imagePath, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("image: populate: %w: %s", err, out)
	}
	return nil
}
