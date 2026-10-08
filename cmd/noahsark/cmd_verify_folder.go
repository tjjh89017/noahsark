package main

import (
	"fmt"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/image"
)

// verifyWithoutRepo checks root with no repository. It records nothing
// and names the disc by its uuid.
func (o *verifyOptions) verifyWithoutRepo(e *env, root string, ident discIdentity) int {
	c := verifyCheck{
		e:     e,
		name:  fmt.Sprintf("disc %s %q", uuidText(ident.DiscUUID), ident.Label),
		short: "disc " + uuidText(ident.DiscUUID),
	}
	c.note = notCountedNoRepo
	c.reason = reasonDiscRootDamaged
	rr, checkErr := image.ReadWithProgress(root, e.progress())
	printNotices(e.stderr, "verify", rr)
	return c.report(rr, checkErr)
}

// verifyCheck prints the lines of a check that records nothing.
type verifyCheck struct {
	e *env
	// name is the disc name of the ok line and the bad line. short is
	// the disc name of a refusal.
	name  string
	short string
	// note is the line after the ok line or the bad line.
	note string
	// reason is the text after "bad; " of a failed check.
	reason string
}

// report prints the lines of the check result rr and checkErr, and
// returns the exit code.
func (c verifyCheck) report(rr *image.ReadResult, checkErr error) int {
	stdout := c.e.stdout
	if checkErr != nil {
		_, _ = fmt.Fprintf(stdout, "%s: bad; %s\n", c.name, c.reason)
		_, _ = fmt.Fprintln(stdout, c.note)
		_, _ = fmt.Fprintf(c.e.stderr, "noahsark: verify: %v\n", checkErr)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: %d items, ok\n", c.name, rr.ObjectsVerified)
	_, _ = fmt.Fprintln(stdout, c.note)
	return 0
}

// isPackedTree tells whether root is the disc root that pack wrote for
// the disc discUUID: the tree under staging, or the target of its
// symlink for a pack --out disc.
func isPackedTree(e *env, layout repoLayout, root string, discUUID [16]byte) bool {
	resolved, err := resolvePath(e, root)
	if err != nil {
		return false
	}
	tree, err := filepath.EvalSymlinks(layout.planTree(discUUID))
	if err != nil {
		return false
	}
	return resolved == tree
}
