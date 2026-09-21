package object

import "testing"

func TestParsePatternRejectsNegation(t *testing.T) {
	if _, err := ParsePattern("!keep.txt"); err == nil {
		t.Fatal("expected an error for a negated pattern")
	}
}

func TestParsePatternRejectsBadGlob(t *testing.T) {
	if _, err := ParsePattern("[unterminated"); err == nil {
		t.Fatal("expected an error for a malformed pattern")
	}
}

func TestMatcherOrderIndependent(t *testing.T) {
	p1, err := ParsePattern("*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := ParsePattern("/build")
	if err != nil {
		t.Fatal(err)
	}
	a := NewMatcher([]Pattern{p1, p2})
	b := NewMatcher([]Pattern{p2, p1})
	for _, path := range []string{"a.tmp", "build", "keep.txt"} {
		if a.Match(path, false) != b.Match(path, false) {
			t.Errorf("pattern order changed the result for %q", path)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		isDir   bool
		want    bool
	}{
		{"unanchored name at top level", "*.tmp", "a.tmp", false, true},
		{"unanchored name at depth", "*.tmp", "sub/dir/a.tmp", false, true},
		{"unanchored name no match", "*.tmp", "a.txt", false, false},
		{"unanchored plain name at depth", "node_modules", "a/b/node_modules", true, true},
		{"unanchored plain name is a file too", "node_modules", "node_modules", false, true},
		{"anchored leading slash matches root child", "/cache", "cache", true, true},
		{"anchored leading slash does not match nested", "/cache", "sub/cache", true, false},
		{"anchored without leading slash", "build/out", "build/out", false, true},
		{"anchored without leading slash, no match elsewhere", "build/out", "sub/build/out", false, false},
		{"anchored partial prefix does not match", "build/out", "build", true, false},
		{"trailing slash matches directory", "build/", "build", true, true},
		{"trailing slash does not match file", "build/", "build", false, false},
		{"trailing slash unanchored matches nested directory", "cache/", "a/cache", true, true},
		{"trailing slash unanchored does not match nested file", "cache/", "a/cache", false, false},
		{"star does not cross slash", "a/*/c", "a/b/c", false, true},
		{"star does not cross slash, deeper path fails", "a/*/c", "a/b/x/c", false, false},
		{"question mark", "a?c", "abc", false, true},
		{"question mark no match", "a?c", "ac", false, false},
		{"bracket class", "[abc].txt", "a.txt", false, true},
		{"bracket class no match", "[abc].txt", "d.txt", false, false},
		{"double star at start", "**/out", "a/b/out", false, true},
		{"double star at start, zero segments", "**/out", "out", false, true},
		{"double star in middle", "a/**/z", "a/b/c/z", false, true},
		{"double star in middle, zero segments", "a/**/z", "a/z", false, true},
		{"double star at end", "build/**", "build/out/x", false, true},
		{"double star at end matches the directory itself", "build/**", "build", true, true},
		{"double star at end no match outside", "build/**", "other/x", false, false},
		{"name with a space", "my file.txt", "my file.txt", false, true},
		{"name with a space no match", "my file.txt", "myfile.txt", false, false},
		{"leading ./ is stripped, still anchored", "./cache", "cache", true, true},
		{"leading ./ does not match nested", "./cache", "sub/cache", true, false},
		{"root itself never matches", "**", "", false, false},
		{"empty relPath with plain pattern never matches", "cache", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ParsePattern(c.pattern)
			if err != nil {
				t.Fatalf("ParsePattern(%q): %v", c.pattern, err)
			}
			m := NewMatcher([]Pattern{p})
			if got := m.Match(c.path, c.isDir); got != c.want {
				t.Errorf("Match(%q, isDir=%v) with pattern %q = %v, want %v", c.path, c.isDir, c.pattern, got, c.want)
			}
		})
	}
}

func TestNilMatcherMatchesNothing(t *testing.T) {
	var m *Matcher
	if m.Match("anything", false) {
		t.Fatal("a nil matcher must match nothing")
	}
}
