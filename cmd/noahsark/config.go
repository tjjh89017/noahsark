package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// ForceCapacitySectors is disc.force_capacity, stored as a sector
	// count. Zero means unset.
	ForceCapacitySectors uint64
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
}

// laterPhaseConfigKeys names later-phase config keys from OPERATIONS.md's
// configuration reference that a hand-edited config file might carry.
// The phase named is the one OPERATIONS.md tags the key with.
var laterPhaseConfigKeys = map[string]string{
	"fs.append_variant":              "Phase 2",
	"disc.min_spare_ratio":           "Phase 2",
	"disc.allow_raw_append":          "Phase 2",
	"fec.disc_close_parity":          "Phase 3",
	"fec.group_size":                 "Phase 3",
	"commit.copy_first":              "Phase 2",
	"sync.rsync_path":                "Phase 2",
	"sync.rsync_args":                "Phase 2",
	"sync.mirror_dir":                "Phase 2",
	"sync.clear_after_commit":        "Phase 2",
	"metadata.atime":                 "Phase 2",
	"metadata.btime":                 "Phase 2",
	"metadata.xattr":                 "Phase 2",
	"metadata.acl":                   "Phase 2",
	"metadata.windows":               "Phase 2",
	"consolidate.max_plan_discs":     "Phase 3",
	"consolidate.max_spread_ratio":   "Phase 3",
	"consolidate.max_restore_hours":  "Phase 3",
	"consolidate.max_disc_age":       "Phase 3",
	"commitbundle.dir":               "Backlog",
	"commitbundle.keep_after_import": "Backlog",
	"commitbundle.catalog_max_age":   "Backlog",
}

// knownConfigKeys names every key this build reads. A key present in the
// file that is neither here nor in laterPhaseConfigKeys is unknown.
var knownConfigKeys = map[string]bool{
	"repo.uuid":                true,
	"staging.dir":              true,
	"disc.force_capacity":      true,
	"commit.restat_after_read": true,
	"commit.retry_unstable":    true,
	"fec.scheme":               true,
}

// defaultRetryUnstable is commit.retry_unstable's Phase 1 default, applied
// when the config file does not set the key.
const defaultRetryUnstable = 1

// writeConfig writes repository config file with the given keys, one
// key=value pair per line, in a fixed order.
func writeConfig(path string, c repoConfig) error {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "repo.uuid = %s\n", c.RepoUUID)
	_, _ = fmt.Fprintf(&b, "staging.dir = %s\n", c.StagingDir)
	if c.ForceCapacitySectors != 0 {
		_, _ = fmt.Fprintf(&b, "disc.force_capacity = %d\n", c.ForceCapacitySectors)
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
		RestatAfterRead: true,
		RetryUnstable:   defaultRetryUnstable,
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

		if phase, ok := laterPhaseConfigKeys[key]; ok {
			return repoConfig{}, fmt.Errorf("config: %s is a %s key; not available in Phase 1", key, phase)
		}
		if !knownConfigKeys[key] {
			return repoConfig{}, fmt.Errorf("config: unknown key %s", key)
		}

		switch key {
		case "repo.uuid":
			c.RepoUUID = value
		case "staging.dir":
			c.StagingDir = value
		case "disc.force_capacity":
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return repoConfig{}, fmt.Errorf("config: disc.force_capacity: %w", err)
			}
			c.ForceCapacitySectors = n
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
		}
	}
	if err := sc.Err(); err != nil {
		return repoConfig{}, err
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
			return "", fmt.Errorf("no noahsark repository found")
		}
		dir = parent
	}
}
