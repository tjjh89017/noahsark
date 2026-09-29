package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
)

// ErrNoDisc reports a catalog that holds the tables of no disc.
var ErrNoDisc = errors.New("catalog: no disc is in the catalog yet; run pack, or recover, first")

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

// MergedRefs returns the newest record of each ref name over the REFS
// tables of every disc in the catalog, keyed by the name. It returns
// ErrNoDisc when the catalog holds no disc.
func (c *Catalog) MergedRefs() (map[string]format.RefRecord, error) {
	uuids, err := c.catalogDiscs()
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if len(uuids) == 0 {
		return nil, ErrNoDisc
	}
	newest := make(map[string]format.RefRecord)
	for _, uuid := range uuids {
		table, err := c.refsTableOf(uuid)
		if err != nil {
			return nil, err
		}
		for _, r := range table.Records {
			MergeRef(newest, r)
		}
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
