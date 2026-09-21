package object

import (
	"fmt"
	"path"
	"strings"
)

// Pattern is one parsed exclude pattern. A commit builds a Matcher from a
// list of these, drawn from --exclude flags, the sources.exclude config
// key, and a .noahsarkignore file in the source root.
//
// The pattern language is small and gitignore-style:
//   - one pattern on each line; "#" starts a comment; empty lines are
//     ignored;
//   - a pattern with no "/" matches a name at every depth;
//   - a pattern with a "/" is anchored at the source root;
//   - a trailing "/" matches a directory only;
//   - "*" and "?" do not cross "/"; "**" crosses directories; "[abc]"
//     classes work as in path.Match;
//   - negation ("!") is not supported.
type Pattern struct {
	raw      string
	dirOnly  bool
	anchored bool
	segments []string
}

// ParsePattern parses one exclude pattern line. It rejects a pattern that
// starts with "!" (negation is not supported), an empty pattern, and a
// pattern segment path.Match cannot parse.
func ParsePattern(raw string) (Pattern, error) {
	if strings.HasPrefix(raw, "!") {
		return Pattern{}, fmt.Errorf("%q: negation is not supported", raw)
	}

	p := raw
	dirOnly := false
	if strings.HasSuffix(p, "/") {
		dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	if p == "" {
		return Pattern{}, fmt.Errorf("%q: empty pattern", raw)
	}

	anchored := strings.Contains(p, "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return Pattern{}, fmt.Errorf("%q: empty pattern", raw)
	}

	var segments []string
	if anchored {
		for seg := range strings.SplitSeq(p, "/") {
			if seg == "" {
				continue
			}
			segments = append(segments, seg)
		}
		if len(segments) == 0 {
			return Pattern{}, fmt.Errorf("%q: empty pattern", raw)
		}
	} else {
		segments = []string{p}
	}

	for _, seg := range segments {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, "probe"); err != nil {
			return Pattern{}, fmt.Errorf("%q: %w", raw, err)
		}
	}

	return Pattern{raw: raw, dirOnly: dirOnly, anchored: anchored, segments: segments}, nil
}

// Matcher tests a path, relative to a commit's source root, against a set
// of exclude patterns.
type Matcher struct {
	patterns []Pattern
}

// NewMatcher builds a Matcher over patterns. A nil or empty Matcher is
// valid and matches nothing.
func NewMatcher(patterns []Pattern) *Matcher {
	return &Matcher{patterns: append([]Pattern(nil), patterns...)}
}

// Match reports whether relPath (slash-separated, relative to the source
// root, never empty) is excluded. isDir tells a directory-only pattern
// whether relPath is a directory. Order among patterns never matters: a
// path is excluded when any one pattern matches it.
func (m *Matcher) Match(relPath string, isDir bool) bool {
	if m == nil || relPath == "" {
		return false
	}
	segs := strings.Split(relPath, "/")
	base := segs[len(segs)-1]
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if !p.anchored {
			if ok, _ := path.Match(p.segments[0], base); ok {
				return true
			}
			continue
		}
		if matchSegments(p.segments, segs) {
			return true
		}
	}
	return false
}

// matchSegments matches pattern segments against path segments. "**"
// matches zero or more whole path segments; every other segment is
// matched against exactly one path segment with path.Match, so "*" and
// "?" never cross a "/".
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		if len(pat) == 1 {
			return true
		}
		for i := 0; i <= len(path); i++ {
			if matchSegments(pat[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	if ok, _ := goPathMatch(pat[0], path[0]); !ok {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

// goPathMatch is path.Match under a short name, since path is also this
// function's own parameter name in matchSegments.
func goPathMatch(pattern, name string) (bool, error) {
	return path.Match(pattern, name)
}
