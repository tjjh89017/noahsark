package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/object"
)

// configFileName is the config file name inside a repository directory.
const configFileName = "config"

// repoConfig holds the Phase 1 config keys this build honours. Every
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
	// RestatAfterRead is commit.restat_after_read. Defaults true.
	RestatAfterRead bool
	// RetryUnstable is commit.retry_unstable. Defaults to
	// object.defaultRetryUnstable's value.
	RetryUnstable int
	// FECEnabled is fec.scheme != "none": whether pack writes a
	// Reed-Solomon checksum column and parity. Defaults false: burning
	// two identical discs is the primary redundancy; FEC is a reserve
	// feature a repository opts into.
	FECEnabled bool
	// CacheDir is cache.dir: an override for the local cache location.
	// Empty means the default of cache.ResolveDir.
	CacheDir string
	// CacheFormatVersion is cache.format_version. A cache whose stored
	// version differs is deleted and rebuilt, never migrated.
	CacheFormatVersion int
	// PackCapacity is pack.capacity: the capacity pack uses when its own
	// command line names none. It keeps the text the operator wrote, so
	// a preset name still selects the media type it names.
	PackCapacity string
	// RetainAfterClean is staging.retain_after_clean: how long an
	// object stays CLEAN before gc may free its staged file.
	RetainAfterClean time.Duration
	// MinVerifiedCopies is gc.min_verified_copies: how many successful
	// verifies an object needs before gc may delete it. The default of 2
	// keeps the staged bytes until the second identical disc passes
	// verify.
	MinVerifiedCopies int
	// ExcludePatterns is sources.exclude: every exclude pattern the
	// config file names, in the order it names them. The key is
	// repeatable: one line for each pattern.
	ExcludePatterns []object.Pattern
}

// knownConfigKeys names every key this build reads. A key present in the
// file that is not here is unknown.
var knownConfigKeys = map[string]bool{
	"repo.uuid":                  true,
	"staging.dir":                true,
	"sources.root":               true,
	"commit.restat_after_read":   true,
	"commit.retry_unstable":      true,
	"fec.scheme":                 true,
	"pack.capacity":              true,
	"cache.dir":                  true,
	"cache.format_version":       true,
	"staging.retain_after_clean": true,
	"gc.min_verified_copies":     true,
	"sources.exclude":            true,
}

// defaultRetryUnstable is commit.retry_unstable's Phase 1 default, applied
// when the config file does not set the key.
const defaultRetryUnstable = 1

// defaultCacheFormatVersion is cache.format_version's Phase 1 default.
const defaultCacheFormatVersion = 1

// defaultRetainAfterClean is staging.retain_after_clean's default: 7
// days.
const defaultRetainAfterClean = 7 * 24 * time.Hour

// defaultMinVerifiedCopies is gc.min_verified_copies' default: 2, one
// verify for each of the two identical discs.
const defaultMinVerifiedCopies = 2

// parseRetentionDuration parses a duration for staging.retain_after_clean:
// a plain integer with a "d" suffix for whole days, since
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
		RestatAfterRead:    true,
		RetryUnstable:      defaultRetryUnstable,
		CacheFormatVersion: defaultCacheFormatVersion,
		RetainAfterClean:   defaultRetainAfterClean,
		MinVerifiedCopies:  defaultMinVerifiedCopies,
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
				return repoConfig{}, fmt.Errorf("config: sources.root: only one source root is supported in this build")
			}
			c.SourceRoot = value
		case "commit.restat_after_read":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return repoConfig{}, fmt.Errorf("config: commit.restat_after_read: %w", err)
			}
			c.RestatAfterRead = b
		case "commit.retry_unstable":
			n, err := strconv.Atoi(value)
			if err != nil {
				return repoConfig{}, fmt.Errorf("config: commit.retry_unstable: %w", err)
			}
			c.RetryUnstable = n
		case "fec.scheme":
			switch value {
			case "none":
				c.FECEnabled = false
			case "rs255-gf8":
				c.FECEnabled = true
			default:
				return repoConfig{}, fmt.Errorf("config: fec.scheme: unknown value %q, want none or rs255-gf8", value)
			}
		case "pack.capacity":
			if _, err := parseCapacity(value); err != nil {
				return repoConfig{}, fmt.Errorf("config: pack.capacity: %w", err)
			}
			c.PackCapacity = value
		case "cache.dir":
			c.CacheDir = value
		case "cache.format_version":
			n, err := strconv.Atoi(value)
			if err != nil {
				return repoConfig{}, fmt.Errorf("config: cache.format_version: %w", err)
			}
			c.CacheFormatVersion = n
		case "staging.retain_after_clean":
			d, err := parseRetentionDuration(value)
			if err != nil {
				return repoConfig{}, fmt.Errorf("config: staging.retain_after_clean: %w", err)
			}
			c.RetainAfterClean = d
		case "gc.min_verified_copies":
			n, err := strconv.Atoi(value)
			if err != nil {
				return repoConfig{}, fmt.Errorf("config: gc.min_verified_copies: %w", err)
			}
			if n < 1 {
				return repoConfig{}, fmt.Errorf("config: gc.min_verified_copies: must be at least 1")
			}
			c.MinVerifiedCopies = n
		case "sources.exclude":
			pat, err := object.ParsePattern(value)
			if err != nil {
				return repoConfig{}, fmt.Errorf("config: sources.exclude: %s: %w", path, err)
			}
			c.ExcludePatterns = append(c.ExcludePatterns, pat)
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
