package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tjjh89017/noahsark/internal/object"
)

// ignoreFileName is the exclude file commit reads from the source root.
// It is not read from any other directory: keeping it to the root keeps
// the rule simple.
const ignoreFileName = ".noahsarkignore"

// parseExcludeFlags parses every --exclude flag value in order. A bad
// pattern is a usage error naming the pattern.
func parseExcludeFlags(raws []string) ([]object.Pattern, error) {
	patterns := make([]object.Pattern, 0, len(raws))
	for _, raw := range raws {
		p, err := object.ParsePattern(raw)
		if err != nil {
			return nil, fmt.Errorf("--exclude: %w", err)
		}
		patterns = append(patterns, p)
	}
	return patterns, nil
}

// parseIgnoreFile reads sourceRoot's .noahsarkignore, one pattern on each
// line, "#" starting a comment and a blank line ignored. A missing file
// is not an error: it returns no patterns. A bad pattern names the file,
// the line number and the pattern.
func parseIgnoreFile(sourceRoot string) ([]object.Pattern, error) {
	path := filepath.Join(sourceRoot, ignoreFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var patterns []object.Pattern
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := object.ParsePattern(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		patterns = append(patterns, p)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return patterns, nil
}
