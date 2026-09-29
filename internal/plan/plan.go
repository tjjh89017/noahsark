// Package plan plans a restore from the catalog alone. Select resolves
// the PATH arguments of restore to the entries that they name. New and
// Count count, for each disc, the items that restore must read from it.
//
// The plan holds one catalog INDEX in memory at a time. It keeps the ids
// of the needed items in temporary files, never in memory.
package plan

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
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

// ownerLen is the size of one record of the owners file: the content id,
// the disc index as a little-endian uint32, and the byte length as a
// little-endian uint64.
const ownerLen = idLen + 4 + 8

// Plan counts the items that a restore reads from each disc. For each
// item it takes the first disc in this order that holds it: a disc that
// is not lost before a lost disc, then the lowest disc_seq, then the
// lowest uuid. It counts an item one time for each disc.
//
// Add collects the ids in a sorted temporary file. Count then reads the
// catalog INDEX of each disc in that order, one at a time, and takes out
// of the file each id that the disc holds.
type Plan struct {
	c       *catalog.Catalog
	sel     *Selection
	discs   []Disc
	items   []int
	bytes   []uint64
	needed  idSorter
	counted bool
	err     error
	noDisc  int64
	// owners holds one ownerLen record for each counted item.
	owners  *os.File
	ownersN int64
	// owned is the ids that the plan gives to the disc ownedUUID, in
	// ascending order, for Owns.
	owned     []object.ID
	ownedUUID [16]byte
}

// New prepares an empty plan for sel over discs. It reads nothing yet.
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
	return &Plan{
		c:     c,
		sel:   sel,
		discs: ordered,
		items: make([]int, len(ordered)),
		bytes: make([]uint64, len(ordered)),
	}, nil
}

// readTable reads the Objects table of the catalog INDEX of one disc. A
// disc whose INDEX is not in the catalog holds no item.
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
	if !slices.IsSortedFunc(t.ids, compareID) {
		order := make([]int, len(t.ids))
		for i := range order {
			order[i] = i
		}
		slices.SortFunc(order, func(a, b int) int { return compareID(t.ids[a], t.ids[b]) })
		sorted := table{ids: make([]object.ID, len(order)), bytes: make([]uint64, len(order))}
		for i, j := range order {
			sorted.ids[i], sorted.bytes[i] = t.ids[j], t.bytes[j]
		}
		t = sorted
	}
	return t, nil
}

// Add takes one item that the restore needs. An item can come more than
// one time. A write error of the temporary file is returned by Count.
func (p *Plan) Add(id object.ID) { p.needed.add(id) }

// Count counts the items of each disc. It runs one time; a later call
// returns the result of the first call.
func (p *Plan) Count() error {
	if !p.counted {
		p.counted = true
		p.err = p.count()
	}
	return p.err
}

func (p *Plan) count() error {
	left, err := p.needed.sorted()
	if err != nil {
		return err
	}
	defer func() { left.close() }()
	owners, err := tempFile()
	if err != nil {
		return err
	}
	ow := bufio.NewWriterSize(owners, readBufLen)
	for i, d := range p.discs {
		t, err := readTable(p.c, d.DiscUUID)
		if err != nil {
			_ = owners.Close()
			return err
		}
		next, err := p.countDisc(i, &t, left, ow)
		if err != nil {
			_ = owners.Close()
			return err
		}
		left.close()
		left = next
	}
	if err := ow.Flush(); err != nil {
		_ = owners.Close()
		return err
	}
	p.owners = owners
	p.noDisc = left.n
	return nil
}

// countDisc counts each id of in that the disc i holds, and writes its
// owner record. It returns the ids that the disc does not hold. in and
// t are both in ascending order, so one pass over each is enough.
func (p *Plan) countDisc(i int, t *table, in idFile, owners *bufio.Writer) (idFile, error) {
	w, err := newIDWriter()
	if err != nil {
		return idFile{}, err
	}
	r := in.reader()
	row := 0
	var rec [ownerLen]byte
	for {
		id, ok, err := readID(r)
		if err != nil {
			w.discard()
			return idFile{}, err
		}
		if !ok {
			return w.done()
		}
		for row < len(t.ids) && compareID(t.ids[row], id) < 0 {
			row++
		}
		if row == len(t.ids) || t.ids[row] != id {
			if err := w.write(id); err != nil {
				w.discard()
				return idFile{}, err
			}
			continue
		}
		p.items[i]++
		p.bytes[i] += t.bytes[row]
		copy(rec[:idLen], id[:])
		binary.LittleEndian.PutUint32(rec[idLen:idLen+4], uint32(i))
		binary.LittleEndian.PutUint64(rec[idLen+4:], t.bytes[row])
		if _, err := owners.Write(rec[:]); err != nil {
			w.discard()
			return idFile{}, err
		}
		p.ownersN++
	}
}

// Close frees the temporary file of the plan.
func (p *Plan) Close() {
	if p.owners != nil {
		_ = p.owners.Close()
		p.owners = nil
	}
}

// Owns reports whether the plan gives the item id to the disc uuid. The
// plan gives each item to one disc only, also when several discs hold
// it. Thus a restore that reads an item only from the disc that owns it
// writes each item one time. Owns keeps the owned ids of the last disc
// that it was asked about, and reads the owners file again when the
// question moves to another disc. It counts the plan first. A plan that
// does not count gives no item to any disc.
func (p *Plan) Owns(uuid [16]byte, id object.ID) bool {
	if p.Count() != nil {
		return false
	}
	if p.owned == nil || p.ownedUUID != uuid {
		i := slices.IndexFunc(p.discs, func(d Disc) bool { return d.DiscUUID == uuid })
		if i < 0 {
			return false
		}
		owned := []object.ID{}
		for _, o := range (DiscEntry{plan: p, index: i}).Objects {
			owned = append(owned, o.ID)
		}
		p.owned, p.ownedUUID = owned, uuid
	}
	_, found := slices.BinarySearchFunc(p.owned, id, compareID)
	return found
}

// NoDisc is the number of needed items that no catalog INDEX lists. It
// counts the plan first.
func (p *Plan) NoDisc() int {
	_ = p.Count()
	return int(p.noDisc)
}

// DiscEntry is the share of one disc in a plan.
type DiscEntry struct {
	Disc
	Items int
	Bytes uint64
	plan  *Plan
	index int
}

// Discs returns each disc that supplies at least one item, in disc_seq
// order, then in uuid order. It counts the plan first; call Count to
// get its error.
func (p *Plan) Discs() []DiscEntry {
	_ = p.Count()
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

// Objects yields each item that this disc supplies, one time, in the
// order of the content ids. It reads the owners file of the plan. A read
// error ends the sequence early.
func (d DiscEntry) Objects(yield func(int, ObjectEntry) bool) {
	p := d.plan
	if p == nil || p.owners == nil {
		return
	}
	r := bufio.NewReaderSize(io.NewSectionReader(p.owners, 0, p.ownersN*int64(ownerLen)), readBufLen)
	var rec [ownerLen]byte
	n := 0
	for {
		if _, err := io.ReadFull(r, rec[:]); err != nil {
			return
		}
		if binary.LittleEndian.Uint32(rec[idLen:idLen+4]) != uint32(d.index) {
			continue
		}
		var id object.ID
		copy(id[:], rec[:idLen])
		if !yield(n, ObjectEntry{ID: id, Kind: format.ObjectKindChunk, Bytes: binary.LittleEndian.Uint64(rec[idLen+4:])}) {
			return
		}
		n++
	}
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
	if err := p.Count(); err != nil {
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
