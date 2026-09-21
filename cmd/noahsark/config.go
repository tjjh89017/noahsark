package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// configFileName is the config file name inside a repository directory.
const configFileName = "config"

// repoConfig holds the config keys this build honours. Every
// other key OPERATIONS.md's configuration reference names needs behaviour
// this build does not implement, so the loader refuses it by name rather
// than silently ignoring it.
type repoConfig struct {
	// RepoUUID identifies the repository. Generated once at init.
	RepoUUID string
	// StagingDir is staging.dir: the staging store location.
	StagingDir string
	// SourceRoot is sources.root: the source directory commit reads
	// from when its own command line names none. OPERATIONS.md makes
	// this key repeatable, but this build stores at most one, matching
	// Writer.Commit's single source directory. Empty means init was
	// never given --source.
	SourceRoot string
	// FECEnabled is fec.scheme != "none": whether pack writes a
	// Reed-Solomon checksum column and parity. Defaults false: burning
	// two identical discs is the primary redundancy; FEC is a reserve
	// feature a repository opts into.
	FECEnabled bool
	// PackCapacity is pack.capacity: the capacity pack uses when its own
	// command line names none. It keeps the text the operator wrote, so
	// a preset name still selects the media type it names.
	PackCapacity string
	// MinVerifiedCopies is gc.min_verified_copies: how many successful
	// verifies an object needs before gc may delete it. The default of 2
	// keeps the staged bytes until the second identical disc passes
	// verify.
	MinVerifiedCopies int
	// badKeys holds the error of every key whose value did not parse.
	// readConfig keeps the default for such a key and reports nothing;
	// the command that reads the key calls checkKeys and refuses there.
	// A command that never reads the key runs as usual.
	badKeys map[string]error
}

// checkKeys returns the first error of the named keys, in the order
// given, or nil when every one of them parsed.
func (c repoConfig) checkKeys(keys ...string) error {
	for _, k := range keys {
		if err, ok := c.badKeys[k]; ok {
			return err
		}
	}
	return nil
}

// configKeysForCommit, configKeysForPack, configKeysForGC and
// configKeysForVerify name the keys each command reads. A command
// refuses a bad value of one of its own keys and runs with a bad value
// of every other key, so a fault in one key stops one command only.
var (
	configKeysForCommit = []string{"sources.root"}
	configKeysForPack   = []string{"pack.capacity", "fec.scheme"}
	configKeysForGC     = []string{"gc.min_verified_copies"}
	configKeysForVerify = []string{"gc.min_verified_copies"}
)

// knownConfigKeys names every key this build reads. A key present in the
// file that is not here is unknown.
var knownConfigKeys = map[string]bool{
	"repo.uuid":              true,
	"staging.dir":            true,
	"sources.root":           true,
	"fec.scheme":             true,
	"pack.capacity":          true,
	"gc.min_verified_copies": true,
}

// retainAfterClean is how long an object stays CLEAN before gc may
// free its staged file: a fixed 7 days. gc --force-after shortens this
// for one run only.
const retainAfterClean = 7 * 24 * time.Hour

// defaultMinVerifiedCopies is gc.min_verified_copies' default: 2, one
// verify for each of the two identical discs.
const defaultMinVerifiedCopies = 2

// parseRetentionDuration parses a duration for gc --force-after: a
// plain integer with a "d" suffix for whole days, since
// time.ParseDuration has no day unit and a retention period is
// ordinarily counted in days, or any duration string time.ParseDuration
// itself accepts.
func parseRetentionDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseUint(days, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("%q: not a whole number of days", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// writeConfig writes repository config file with the given keys, one
// key=value pair per line, in a fixed order.
func writeConfig(path string, c repoConfig) error {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "repo.uuid = %s\n", c.RepoUUID)
	_, _ = fmt.Fprintf(&b, "staging.dir = %s\n", c.StagingDir)
	if c.SourceRoot != "" {
		_, _ = fmt.Fprintf(&b, "sources.root = %s\n", c.SourceRoot)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// readConfig reads and parses a repository's config file.
func readConfig(path string) (repoConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return repoConfig{}, err
	}
	defer func() { _ = f.Close() }()

	c := repoConfig{
		MinVerifiedCopies: defaultMinVerifiedCopies,
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rawKey, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return repoConfig{}, fmt.Errorf("config: malformed line %q", line)
		}
		key := strings.TrimSpace(rawKey)
		value := strings.TrimSpace(rawValue)

		if !knownConfigKeys[key] {
			return repoConfig{}, fmt.Errorf("config: unknown key %s in %s", key, path)
		}

		switch key {
		case "repo.uuid":
			c.RepoUUID = value
		case "staging.dir":
			c.StagingDir = value
		case "sources.root":
			if c.SourceRoot != "" {
				c.bad(key, fmt.Errorf("config: sources.root: only one source root is supported in this build"))
				break
			}
			c.SourceRoot = value
		case "fec.scheme":
			switch value {
			case "none":
				c.FECEnabled = false
			case "rs255-gf8":
				c.FECEnabled = true
			default:
				c.bad(key, fmt.Errorf("config: fec.scheme: unknown value %q, want none or rs255-gf8", value))
			}
		case "pack.capacity":
			if _, err := parseCapacity(value); err != nil {
				c.bad(key, fmt.Errorf("config: pack.capacity: %w", err))
				break
			}
			c.PackCapacity = value
		case "gc.min_verified_copies":
			n, err := strconv.Atoi(value)
			if err != nil {
				c.bad(key, fmt.Errorf("config: gc.min_verified_copies: %w", err))
				break
			}
			if n < 1 {
				c.bad(key, fmt.Errorf("config: gc.min_verified_copies: must be at least 1"))
				break
			}
			c.MinVerifiedCopies = n
		}
	}
	if err := sc.Err(); err != nil {
		return repoConfig{}, err
	}
	// staging.dir defaults to, and init and recover both write,
	// a bare "staging" relative to the repository directory, so the
	// staging store follows the repository if its directory is ever
	// renamed or moved. Resolve it here, against path's own directory,
	// so every caller of readConfig sees an absolute StagingDir without
	// needing to know the repository directory separately. An absolute
	// value some other tool wrote is left exactly as given.
	if c.StagingDir != "" && !filepath.IsAbs(c.StagingDir) {
		c.StagingDir = filepath.Join(filepath.Dir(path), c.StagingDir)
	}
	return c, nil
}

// refuseBadConfig prints the fault of the first named key whose value
// did not parse, and reports whether cmdName must stop. A key cmdName
// never reads is not named here, so one bad value stops only the
// commands that need that key.
func refuseBadConfig(cmdName string, cfg repoConfig, stderr io.Writer, keys ...string) bool {
	err := cfg.checkKeys(keys...)
	if err == nil {
		return false
	}
	_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmdName, err)
	return true
}

// bad records that key's value did not parse. The first error for a key
// wins, so a repeated key reports the fault the operator meets first.
func (c *repoConfig) bad(key string, err error) {
	if c.badKeys == nil {
		c.badKeys = make(map[string]error)
	}
	if _, ok := c.badKeys[key]; !ok {
		c.badKeys[key] = err
	}
}

// configPath returns the config file path inside a repository directory.
func configPath(repoDir string) string {
	return filepath.Join(repoDir, configFileName)
}

// isRepoDir reports whether dir holds a readable config file.
func isRepoDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, configFileName))
	return err == nil
}

// discoverRepo resolves the repository root: explicitRepo when set,
// else NOAHSARK_REPO, else the nearest ancestor of the working directory
// that holds a config file. It matches OPERATIONS.md's repository
// discovery order.
func discoverRepo(explicitRepo string) (string, error) {
	if explicitRepo != "" {
		if !isRepoDir(explicitRepo) {
			return "", fmt.Errorf("%s is not a noahsark repository", explicitRepo)
		}
		return explicitRepo, nil
	}
	if env := os.Getenv("NOAHSARK_REPO"); env != "" {
		if !isRepoDir(env) {
			return "", fmt.Errorf("NOAHSARK_REPO=%s is not a noahsark repository", env)
		}
		return env, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if isRepoDir(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no noahsark repository found; pass --repo=DIR, set NOAHSARK_REPO, or run from inside the repository")
		}
		dir = parent
	}
}
