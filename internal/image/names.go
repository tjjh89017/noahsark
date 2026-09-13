package image

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// NameCache resolves a fixed on-disc file or directory name inside a
// directory case-insensitively. Some burners, such as plain ISO 9660
// level 4 with no Rock Ridge, fold every fixed name to lowercase; a
// NameCache lets a reader accept that output alongside the exact case
// FORMAT.md defines.
//
// A caller creates one NameCache per Read, list, restore or heal call
// and reuses it for every fixed name that call resolves, so a run with
// many lookups scans each directory at most once.
type NameCache struct {
	mu   sync.Mutex
	dirs map[string]map[string]string
}

// NewNameCache returns an empty NameCache.
func NewNameCache() *NameCache {
	return &NameCache{dirs: make(map[string]map[string]string)}
}

// Resolve returns the actual entry name inside dir that matches want:
// want itself when that exact name is present, the entry that matches
// want under strings.EqualFold otherwise, or want unchanged when
// neither is found, so the caller's own open reports the real error.
func (c *NameCache) Resolve(dir, want string) string {
	if _, err := os.Lstat(filepath.Join(dir, want)); err == nil {
		return want
	}
	names := c.listing(dir)
	if actual, ok := names[strings.ToLower(want)]; ok {
		return actual
	}
	return want
}

// Join resolves each of names in turn as one path level under dir,
// case-insensitively, and returns the path built from the actual
// on-disc names it found.
func (c *NameCache) Join(dir string, names ...string) string {
	cur := dir
	for _, name := range names {
		cur = filepath.Join(cur, c.Resolve(cur, name))
	}
	return cur
}

// listing returns dir's entry names, keyed by their lowercase form,
// reading the directory at most once per NameCache.
func (c *NameCache) listing(dir string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if names, ok := c.dirs[dir]; ok {
		return names
	}
	names := make(map[string]string)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			names[strings.ToLower(e.Name())] = e.Name()
		}
	}
	c.dirs[dir] = names
	return names
}
