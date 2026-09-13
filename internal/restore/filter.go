package restore

import (
	"fmt"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
)

// tail is one --include path's still-unconsumed segments, tagged with
// its position in the original --include list so a match can be
// recorded back against the right path.
type tail struct {
	idx  int
	segs []string
}

// filterState is the include restriction in force at one node of the
// walk. A nil *filterState means the node and everything under it is
// restored, with no restriction at all: the walk never allocates one
// when no --include was given.
type filterState struct {
	// matched is shared by every filterState derived from the same
	// initial call to newFilterState, so a match recorded deep in the
	// walk is visible once the walk returns to the top.
	matched []bool
	tails   []tail
}

// newFilterState splits every include path into segments and returns
// the root filterState a walk starts from. It returns nil, nil when
// includes is empty, so a caller with no --include flags can skip
// filtering entirely.
func newFilterState(includes []string) (*filterState, error) {
	if len(includes) == 0 {
		return nil, nil
	}
	tails := make([]tail, len(includes))
	for i, inc := range includes {
		segs := splitPath(inc)
		if len(segs) == 0 {
			return nil, fmt.Errorf("restore: --include %q: empty path", inc)
		}
		tails[i] = tail{idx: i, segs: segs}
	}
	return &filterState{matched: make([]bool, len(includes)), tails: tails}, nil
}

// stepInto advances fs by one node whose full path, relative to fs's own
// node, is names (one segment for an ordinary tree entry; a root entry
// consumes its whole root-path segment list at once). It reports whether
// the node is restored at all, and the filterState its children should
// use: nil for "restore everything below, unrestricted", or a
// filterState still narrowing toward one or more deeper includes.
//
// A nil fs (no restriction) always reports (nil, true).
func stepInto(fs *filterState, names []string) (*filterState, bool) {
	if fs == nil {
		return nil, true
	}
	var newTails []tail
	fullMatch := false
	for _, t := range fs.tails {
		n := min(len(names), len(t.segs))
		mismatch := false
		for i := range n {
			if t.segs[i] != names[i] {
				mismatch = true
				break
			}
		}
		if mismatch {
			continue
		}
		if len(t.segs) <= len(names) {
			// The include path ends at or above this node: this node
			// and everything under it is included.
			fs.matched[t.idx] = true
			fullMatch = true
			continue
		}
		newTails = append(newTails, tail{idx: t.idx, segs: t.segs[len(names):]})
	}
	if fullMatch {
		return nil, true
	}
	if len(newTails) > 0 {
		return &filterState{matched: fs.matched, tails: newTails}, true
	}
	return nil, false
}

// unmatchedIncludes returns the original --include text of every path
// stepInto never fully matched.
func unmatchedIncludes(fs *filterState, includes []string) []string {
	if fs == nil {
		return nil
	}
	var out []string
	for i, ok := range fs.matched {
		if !ok {
			out = append(out, includes[i])
		}
	}
	return out
}

// UnmatchedIncludeError reports that one or more --include paths matched
// no entry in the snapshot. It is returned before any file is written.
type UnmatchedIncludeError struct {
	Paths []string
}

func (e *UnmatchedIncludeError) Error() string {
	return fmt.Sprintf("restore: --include path(s) matched nothing: %s", strings.Join(e.Paths, ", "))
}

// splitPath splits a forward-slash snapshot-relative path into segments,
// stripping one leading slash and dropping any empty segment a doubled
// or trailing slash would otherwise produce.
func splitPath(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// rootPathOf returns e's ROOT_PATH TLV payload, or "" when absent.
func rootPathOf(e format.TreeEntry) string {
	for _, t := range e.TLVs {
		if t.Type == format.TLVTypeRootPath {
			return string(t.Payload)
		}
	}
	return ""
}
