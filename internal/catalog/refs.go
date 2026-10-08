package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
)

// noDiscText is the text of the error of a catalog that holds the tables
// of no disc.
const noDiscText = "catalog: no disc is in the catalog yet; run pack, or recover, first"

// ErrNoDisc reports a catalog that holds the tables of no disc.
var ErrNoDisc = errors.New(noDiscText)

// RefName returns the name of a ref record.
func RefName(r format.RefRecord) string {
	return string(r.Name[:min(int(r.NameLen), format.RefNameLen)])
}

// MergeRef keeps in newest the newest record of the name of r. The rule
// of format.NewerRef selects the newest record.
func MergeRef(newest map[string]format.RefRecord, r format.RefRecord) {
	name := RefName(r)
	if old, ok := newest[name]; ok && !format.NewerRef(r, old) {
		return
	}
	newest[name] = r
}

// DamagedRefsError reports REFS tables of the catalog that cannot be
// read or decoded. Discs and Errs have one entry for each such table, in
// uuid order. recover or a counted verify of the disc writes the table
// again.
type DamagedRefsError struct {
	Discs [][16]byte
	Errs  []error
}

func (e *DamagedRefsError) Error() string {
	texts := make([]string, len(e.Errs))
	for i, err := range e.Errs {
		texts[i] = err.Error()
	}
	return strings.Join(texts, "; ")
}

// DiscText gives the uuid of each damaged table in text form, separated
// by ", ".
func (e *DamagedRefsError) DiscText() string {
	texts := make([]string, len(e.Discs))
	for i, uuid := range e.Discs {
		texts[i] = uuidText(uuid)
	}
	return strings.Join(texts, ", ")
}

// MergedRefs returns the newest record of each ref name over the REFS
// tables of every disc in the catalog, keyed by the name. It returns
// ErrNoDisc when the catalog holds no disc. When one or more REFS tables
// cannot be read or decoded, it returns the merge of the other tables
// and a *DamagedRefsError.
func (c *Catalog) MergedRefs() (map[string]format.RefRecord, error) {
	uuids, err := c.catalogDiscs()
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if len(uuids) == 0 {
		return nil, ErrNoDisc
	}
	newest := make(map[string]format.RefRecord)
	var damaged *DamagedRefsError
	for _, uuid := range uuids {
		table, err := c.refsTableOf(uuid)
		if err != nil {
			if damaged == nil {
				damaged = &DamagedRefsError{}
			}
			damaged.Discs = append(damaged.Discs, uuid)
			damaged.Errs = append(damaged.Errs, err)
			continue
		}
		for _, r := range table.Records {
			MergeRef(newest, r)
		}
	}
	if damaged != nil {
		return newest, damaged
	}
	return newest, nil
}

// refsTableOf reads and decodes the REFS.bin of one catalog disc.
func (c *Catalog) refsTableOf(uuid [16]byte) (*format.RefsTable, error) {
	buf, err := os.ReadFile(filepath.Join(c.discDir(uuid), RefsFileName))
	if err != nil {
		return nil, fmt.Errorf("catalog: disc %s: %w", uuidText(uuid), err)
	}
	var refs format.RefsTable
	if _, err := refs.Decode(buf); err != nil {
		return nil, fmt.Errorf("catalog: disc %s: REFS.bin: %w", uuidText(uuid), err)
	}
	return &refs, nil
}
