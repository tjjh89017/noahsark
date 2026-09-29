package main

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// mountVerdict tells whether a disc root is a counted mount, and if not,
// why not.
type mountVerdict int

const (
	mountCounted mountVerdict = iota
	mountNotMountPoint
	mountReadWrite
	mountInsideRepo
	mountInsideStaging
)

func (v mountVerdict) String() string {
	switch v {
	case mountCounted:
		return "counted mount"
	case mountNotMountPoint:
		return "not a mount point"
	case mountReadWrite:
		return "not a read-only mount"
	case mountInsideRepo:
		return "inside the repository"
	case mountInsideStaging:
		return "inside the staging store"
	}
	return fmt.Sprintf("mountVerdict(%d)", int(v))
}

// mountEntry is one line of a mount table in the format of
// /proc/self/mountinfo.
type mountEntry struct {
	mountPoint string
	readOnly   bool
}

// countedMount tells whether root is a counted mount. A counted mount is
// a path that, after symlink resolution, is itself a mount point, is
// mounted read-only, and is outside repoDir and outside stagingDir. The
// source of the mount is not checked, thus a loop mount counts. An empty
// stagingDir skips the staging check. A relative path is relative to the
// working directory of e.
func countedMount(e *env, root, repoDir, stagingDir string) (mountVerdict, error) {
	resolved, err := resolvePath(e, root)
	if err != nil {
		return 0, fmt.Errorf("resolve disc root: %w", err)
	}
	repo, err := resolveDir(e, repoDir)
	if err != nil {
		return 0, err
	}
	if isWithin(resolved, repo) {
		return mountInsideRepo, nil
	}
	if stagingDir != "" {
		staging, err := resolveDir(e, stagingDir)
		if err != nil {
			return 0, err
		}
		if isWithin(resolved, staging) {
			return mountInsideStaging, nil
		}
	}
	entry, found, err := findMount(e, resolved)
	if err != nil {
		return 0, err
	}
	if !found {
		return mountNotMountPoint, nil
	}
	if !entry.readOnly {
		return mountReadWrite, nil
	}
	return mountCounted, nil
}

// resolvePath makes path absolute and resolves every symlink in it.
func resolvePath(e *env, path string) (string, error) {
	if !filepath.IsAbs(path) {
		wd, err := e.getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(wd, path)
	}
	return filepath.EvalSymlinks(path)
}

// resolveDir resolves dir like resolvePath. A directory that does not
// exist keeps its clean absolute form.
func resolveDir(e *env, dir string) (string, error) {
	resolved, err := resolvePath(e, dir)
	if err == nil {
		return resolved, nil
	}
	if !filepath.IsAbs(dir) {
		wd, werr := e.getwd()
		if werr != nil {
			return "", werr
		}
		dir = filepath.Join(wd, dir)
	}
	return filepath.Clean(dir), nil
}

// isWithin tells whether path is dir or a path under dir. Both paths are
// absolute and clean.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// findMount reads the mount table of e and returns the entry of the mount
// point path. When several entries have the same mount point, the last
// entry is the mount in effect.
func findMount(e *env, path string) (mountEntry, bool, error) {
	r, err := e.mountinfo()
	if err != nil {
		return mountEntry{}, false, fmt.Errorf("read mount table: %w", err)
	}
	defer func() { _ = r.Close() }()

	var match mountEntry
	found := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		entry, err := parseMountLine(text)
		if err != nil {
			return mountEntry{}, false, fmt.Errorf("read mount table: line %d: %w", line, err)
		}
		if entry.mountPoint == path {
			match = entry
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return mountEntry{}, false, fmt.Errorf("read mount table: %w", err)
	}
	return match, found, nil
}

// parseMountLine decodes one line of a mount table in the format of
// /proc/self/mountinfo. The fields are: mount id, parent id, major:minor,
// root, mount point, mount options, zero or more optional fields, the
// separator "-", filesystem type, source, super block options.
func parseMountLine(text string) (mountEntry, error) {
	fields := strings.Fields(text)
	if len(fields) < 6 {
		return mountEntry{}, errors.New("too few fields")
	}
	sep := slices.Index(fields[6:], "-")
	if sep < 0 {
		return mountEntry{}, errors.New("no separator")
	}
	after := fields[6+sep+1:]
	if len(after) < 3 {
		return mountEntry{}, errors.New("too few fields after the separator")
	}
	mountPoint, err := unescapeMountField(fields[4])
	if err != nil {
		return mountEntry{}, err
	}
	readOnly := hasOption(fields[5], "ro") || hasOption(after[2], "ro")
	return mountEntry{mountPoint: mountPoint, readOnly: readOnly}, nil
}

// hasOption tells whether the comma-separated list opts holds name.
func hasOption(opts, name string) bool {
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == name {
			return true
		}
	}
	return false
}

// unescapeMountField decodes the octal escapes of a mount table field.
// The kernel writes a space, a tab, a newline and a backslash as a
// backslash and three octal digits.
func unescapeMountField(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+3 >= len(s) {
			return "", fmt.Errorf("bad escape in %q", s)
		}
		var v byte
		for _, c := range []byte(s[i+1 : i+4]) {
			if c < '0' || c > '7' {
				return "", fmt.Errorf("bad escape in %q", s)
			}
			v = v<<3 | (c - '0')
		}
		b.WriteByte(v)
		i += 3
	}
	return b.String(), nil
}
