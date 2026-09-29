package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// The defaults of the config keys that have one.
const (
	defaultStagingDir = "staging"
	defaultPackDevice = "/dev/sr0"
)

// configFile holds the keys of config.yaml as the file writes them. The
// field order is the key order of a written file.
type configFile struct {
	Repo struct {
		UUID string `yaml:"uuid"`
	} `yaml:"repo"`
	Staging struct {
		Dir string `yaml:"dir"`
	} `yaml:"staging"`
	Sources struct {
		Root string `yaml:"root,omitempty"`
	} `yaml:"sources,omitempty"`
	Pack struct {
		Device string `yaml:"device"`
	} `yaml:"pack"`
}

// newConfigFile returns a configFile with repoUUID and the default of
// every key that has one.
func newConfigFile(repoUUID [16]byte, sourceRoot string) configFile {
	var f configFile
	f.Repo.UUID = fmt.Sprintf("%x", repoUUID)
	f.Staging.Dir = defaultStagingDir
	f.Sources.Root = sourceRoot
	f.Pack.Device = defaultPackDevice
	return f
}

// repoConfig is the config of a repository as the commands use it.
// StagingDir is absolute.
type repoConfig struct {
	RepoUUID   string
	StagingDir string
	SourceRoot string
	PackDevice string
}

// configError is a fault in the config file. A command that meets one
// exits with code 2.
type configError struct {
	path string
	err  error
}

func (e *configError) Error() string {
	return fmt.Sprintf("%s: %v", e.path, e.err)
}

func (e *configError) Unwrap() error { return e.err }

// isConfigError reports whether err is a fault in the config file.
func isConfigError(err error) bool {
	_, ok := errors.AsType[*configError](err)
	return ok
}

// retainAfterClean is how long an object stays CLEAN before gc may
// free its staged file: a fixed 7 days. gc --force-after shortens this
// for one run only.
const retainAfterClean = 7 * 24 * time.Hour

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

// encodeConfig returns the text of f: the keys in a fixed order, an
// indent of two spaces, and no line wrap. The same f always gives the
// same text, so a diff of the file shows only the changed keys.
func encodeConfig(f configFile) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&f); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeConfig replaces the config file at path with f. It writes a
// temporary file in the same directory, syncs it, and renames it over
// path, so a crash leaves the old file or the new file, never a part.
func writeConfig(path string, f configFile) error {
	data, err := encodeConfig(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.yaml.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	if err := d.Close(); err != nil {
		return err
	}
	return syncErr
}

// decodeConfig parses the text of a config file. Each key must be a
// known key, and the text must hold exactly one YAML document. It sets
// the default of each key that the text does not give, and checks
// repo.uuid.
func decodeConfig(data []byte) (configFile, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return configFile{}, errors.New("the file is empty")
		}
		return configFile{}, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("the file holds more than one YAML document")
		}
		return configFile{}, err
	}
	if err := checkConfigKeys(&doc); err != nil {
		return configFile{}, err
	}
	var f configFile
	known := yaml.NewDecoder(bytes.NewReader(data))
	known.KnownFields(true)
	if err := known.Decode(&f); err != nil {
		return configFile{}, err
	}
	if f.Staging.Dir == "" {
		f.Staging.Dir = defaultStagingDir
	}
	if f.Pack.Device == "" {
		f.Pack.Device = defaultPackDevice
	}
	if _, err := parseRepoUUID(f.Repo.UUID); err != nil {
		return configFile{}, err
	}
	return f, nil
}

// checkConfigKeys returns an error that names the first unknown key of
// doc in the dotted form, such as repo.name. A value of the wrong
// kind is left to the decode into configFile.
func checkConfigKeys(doc *yaml.Node) error {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil
	}
	known := knownConfigKeys()
	for i := 0; i+1 < len(top.Content); i += 2 {
		section, value := top.Content[i], top.Content[i+1]
		if value.Kind != yaml.MappingNode {
			if !known[section.Value] {
				return fmt.Errorf("line %d: unknown key %s", section.Line, section.Value)
			}
			continue
		}
		for j := 0; j+1 < len(value.Content); j += 2 {
			key := value.Content[j]
			name := section.Value + "." + key.Value
			if !known[name] {
				return fmt.Errorf("line %d: unknown key %s", key.Line, name)
			}
		}
	}
	return nil
}

// knownConfigKeys returns the name of each section of configFile, and
// the dotted name of each key in a section, from the yaml struct tags.
func knownConfigKeys() map[string]bool {
	known := make(map[string]bool)
	top := reflect.TypeFor[configFile]()
	for sectionField := range top.Fields() {
		section := yamlTagName(sectionField)
		known[section] = true
		for keyField := range sectionField.Type.Fields() {
			known[section+"."+yamlTagName(keyField)] = true
		}
	}
	return known
}

// yamlTagName returns the key name that the yaml struct tag of f gives.
func yamlTagName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	return name
}

// parseRepoUUID parses the value of repo.uuid: 32 hex digits.
func parseRepoUUID(s string) ([16]byte, error) {
	if s == "" {
		return [16]byte{}, errors.New("repo.uuid: missing")
	}
	if len(s) != 32 {
		return [16]byte{}, fmt.Errorf("repo.uuid: %q is not 32 hex digits", s)
	}
	u, err := decodeUUID(s)
	if err != nil {
		return [16]byte{}, fmt.Errorf("repo.uuid: %q is not 32 hex digits", s)
	}
	return u, nil
}

// readConfig reads the config file at path. A relative staging.dir is
// made absolute against the directory of path.
func readConfig(path string) (repoConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return repoConfig{}, err
	}
	f, err := decodeConfig(data)
	if err != nil {
		return repoConfig{}, &configError{path: path, err: err}
	}
	stagingDir := f.Staging.Dir
	if !filepath.IsAbs(stagingDir) {
		stagingDir = filepath.Join(filepath.Dir(path), stagingDir)
	}
	return repoConfig{
		RepoUUID:   f.Repo.UUID,
		StagingDir: stagingDir,
		SourceRoot: f.Sources.Root,
		PackDevice: f.Pack.Device,
	}, nil
}
