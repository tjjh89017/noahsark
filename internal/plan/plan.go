// Package plan plans a restore from the catalog alone. Select resolves
// the PATH arguments of restore to the entries that they name. New
// counts, for each disc, the items that restore must read from it. The
// plan keeps counts only, never a list of chunks.
package plan

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// Disc is one disc of the repository, as restore names it.
type Disc struct {
	DiscUUID [16]byte
	DiscSeq  uint64
	Label    string
	Lost     bool
}

// table is the Objects table of one disc INDEX in the catalog: the
// content ids in ascending order, and the byte length of each object
// file.
type table struct {
	ids   []object.ID
	bytes []uint64
}

// find returns the row of id, or false.
func (t *table) find(id object.ID) (int, bool) {
	return slices.BinarySearchFunc(t.ids, id, func(a, b object.ID) int { return bytes.Compare(a[:], b[:]) })
}

// Plan counts the items that a restore reads from each disc. For each
// item it takes the first disc in this order that holds it: a disc that
// is not lost before a lost disc, then the lowest disc_seq, then the
// lowest uuid. It counts an item one time for each disc.
type Plan struct {
	sel     *Selection
	discs   []Disc
	tables  []table
	counted [][]bool
	items   []int
	bytes   []uint64
	byUUID  map[[16]byte]int
	// noDisc holds the items that no catalog INDEX lists.
	noDisc map[object.ID]bool
}

// New prepares an empty plan for sel over discs. It reads the catalog
// INDEX of each disc. A disc whose INDEX is not in the catalog holds no
// item of the plan.
func New(c *catalog.Catalog, sel *Selection, discs []Disc) (*Plan, error) {
	ordered := slices.Clone(discs)
	slices.SortStableFunc(ordered, func(a, b Disc) int {
		if a.Lost != b.Lost {
			if a.Lost {
				return 1
			}
			return -1
		}
		if a.DiscSeq != b.DiscSeq {
			if a.DiscSeq < b.DiscSeq {
				return -1
			}
			return 1
		}
		return bytes.Compare(a.DiscUUID[:], b.DiscUUID[:])
	})
	p := &Plan{
		sel:    sel,
		discs:  ordered,
		byUUID: make(map[[16]byte]int, len(ordered)),
		noDisc: make(map[object.ID]bool),
	}
	for i, d := range ordered {
		t, err := readTable(c, d.DiscUUID)
		if err != nil {
			return nil, err
		}
		p.tables = append(p.tables, t)
		p.counted = append(p.counted, make([]bool, len(t.ids)))
		p.items = append(p.items, 0)
		p.bytes = append(p.bytes, 0)
		p.byUUID[d.DiscUUID] = i
	}
	return p, nil
}

// readTable reads the Objects table of the catalog INDEX of one disc.
func readTable(c *catalog.Catalog, uuid [16]byte) (table, error) {
	idx, err := c.IndexForDisc(uuid)
	if errors.Is(err, fs.ErrNotExist) {
		return table{}, nil
	}
	if err != nil {
		return table{}, err
	}
	var lens []uint64
	for _, f := range idx.Files {
		if f.Role == format.FileRoleObject {
			lens = append(lens, f.ByteLen)
		}
	}
	t := table{ids: make([]object.ID, len(idx.Objects)), bytes: make([]uint64, len(idx.Objects))}
	for i, row := range idx.Objects {
		t.ids[i] = object.ID(row.ContentID)
		if i < len(lens) {
			t.bytes[i] = lens[i]
		}
	}
	if !slices.IsSortedFunc(t.ids, func(a, b object.ID) int { return bytes.Compare(a[:], b[:]) }) {
		order := make([]int, len(t.ids))
		for i := range order {
			order[i] = i
		}
		slices.SortFunc(order, func(a, b int) int { return bytes.Compare(t.ids[a][:], t.ids[b][:]) })
		sorted := table{ids: make([]object.ID, len(order)), bytes: make([]uint64, len(order))}
		for i, j := range order {
			sorted.ids[i], sorted.bytes[i] = t.ids[j], t.bytes[j]
		}
		t = sorted
	}
	return t, nil
}

// owner returns the disc that supplies id and the row of id in the table
// of that disc.
func (p *Plan) owner(id object.ID) (disc, row int, ok bool) {
	for i := range p.tables {
		if row, found := p.tables[i].find(id); found {
			return i, row, true
		}
	}
	return 0, 0, false
}

// Add counts one item that the restore needs.
func (p *Plan) Add(id object.ID) {
	i, row, ok := p.owner(id)
	if !ok {
		p.noDisc[id] = true
		return
	}
	if p.counted[i][row] {
		return
	}
	p.counted[i][row] = true
	p.items[i]++
	p.bytes[i] += p.tables[i].bytes[row]
}

// Holds reports whether the catalog INDEX of the disc uuid lists id.
func (p *Plan) Holds(uuid [16]byte, id object.ID) bool {
	i, ok := p.byUUID[uuid]
	if !ok {
		return false
	}
	_, found := p.tables[i].find(id)
	return found
}

// NoDisc is the number of needed items that no catalog INDEX lists.
func (p *Plan) NoDisc() int { return len(p.noDisc) }

// DiscEntry is the share of one disc in a plan.
type DiscEntry struct {
	Disc
	Items int
	Bytes uint64
	plan  *Plan
	index int
}

// Discs returns each disc that supplies at least one item, in disc_seq
// order, then in uuid order.
func (p *Plan) Discs() []DiscEntry {
	var out []DiscEntry
	for i, d := range p.discs {
		if p.items[i] == 0 {
			continue
		}
		out = append(out, DiscEntry{Disc: d, Items: p.items[i], Bytes: p.bytes[i], plan: p, index: i})
	}
	slices.SortFunc(out, func(a, b DiscEntry) int {
		if a.DiscSeq != b.DiscSeq {
			if a.DiscSeq < b.DiscSeq {
				return -1
			}
			return 1
		}
		return bytes.Compare(a.DiscUUID[:], b.DiscUUID[:])
	})
	return out
}

// ObjectEntry is one item that a disc supplies.
type ObjectEntry struct {
	ID    object.ID
	Kind  format.ObjectKind
	Bytes uint64
}

// errStopWalk stops a walk when the consumer of Objects stops.
var errStopWalk = errors.New("walk stopped")

// Objects walks the selection of the plan again, and yields each item
// that this disc supplies, one time. It does not look at the
// destination: it yields the items of the whole selection. A catalog
// read error ends the sequence early.
func (d DiscEntry) Objects(yield func(int, ObjectEntry) bool) {
	p := d.plan
	if p == nil || p.sel == nil {
		return
	}
	seen := make([]bool, len(p.tables[d.index].ids))
	n := 0
	_ = p.sel.Walk(".", &chunkWalk{c: p.sel.c, chunk: func(id object.ID) error {
		i, row, ok := p.owner(id)
		if !ok || i != d.index || seen[row] {
			return nil
		}
		seen[row] = true
		if !yield(n, ObjectEntry{ID: id, Kind: format.ObjectKindChunk, Bytes: p.tables[i].bytes[row]}) {
			return errStopWalk
		}
		n++
		return nil
	}})
}

// Result is a plan of the discs in the catalog.
type Result struct {
	Discs []DiscEntry
}

// Build plans the chunks of paths in snap over every disc that the
// newest DISCS table of the catalog names, with no disc lost. It ignores
// what a destination holds. paths are relative to the source root, as
// for Select.
func Build(c *catalog.Catalog, snap *format.Snapshot, _ object.ID, paths []string) (*Result, error) {
	sel, err := Select(c, snap, paths)
	if err != nil {
		return nil, err
	}
	// A catalog with no disc has no DISCS table, and the plan then names
	// no disc.
	var rows []format.DiscsRow
	if table, err := c.Discs(); err == nil {
		rows = table.Rows
	}
	var discs []Disc
	seen := make(map[[16]byte]bool)
	for _, row := range rows {
		if seen[row.DiscUUID] {
			continue
		}
		seen[row.DiscUUID] = true
		discs = append(discs, Disc{DiscUUID: row.DiscUUID, DiscSeq: row.DiscSeq, Label: labelOf(row)})
	}
	p, err := New(c, sel, discs)
	if err != nil {
		return nil, err
	}
	if err := sel.Walk(".", &chunkWalk{c: c, chunk: func(id object.ID) error { p.Add(id); return nil }}); err != nil {
		return nil, err
	}
	return &Result{Discs: p.Discs()}, nil
}

// labelOf trims the fixed-width label field of a DISCS row.
func labelOf(row format.DiscsRow) string {
	n := min(int(row.LabelLen), len(row.Label))
	return string(row.Label[:n])
}

// chunkWalk is a Visitor that calls chunk for each chunk of each file of
// a selection. It changes no file. A file whose blob is not in the
// catalog adds no chunk.
type chunkWalk struct {
	c     *catalog.Catalog
	chunk func(object.ID) error
}

func (w *chunkWalk) Dir(parent string, names []string) (string, bool, error) {
	path, err := JoinAll(parent, names)
	return path, err == nil, err
}

func (w *chunkWalk) DirDone(string, format.TreeEntry) {}

func (w *chunkWalk) File(_, _ string, e format.TreeEntry) error {
	blob, err := w.c.ReadBlob(object.ID(e.ContentID))
	if err != nil {
		return nil
	}
	for _, be := range blob.Entries {
		if err := w.chunk(object.ID(be.ContentID)); err != nil {
			return err
		}
	}
	return nil
}

func (w *chunkWalk) Other(string, format.TreeEntry) error { return nil }
