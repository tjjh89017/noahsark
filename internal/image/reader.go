package image

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// ReadResult holds every structure Read decoded from one disc tree, and
// the checks it ran. It holds no object content.
type ReadResult struct {
	Disc  format.Disc
	Run   format.Run
	Index format.Index
	Refs  format.RefsTable
	Discs format.DiscsTable

	// RefsIntact and DiscsIntact are false only after a keep-going read
	// found that table damaged. The table field is then empty.
	RefsIntact  bool
	DiscsIntact bool

	// ObjectsVerified counts the objects that passed their check.
	ObjectsVerified int
	RunCopies       int

	// Damaged lists each damaged or unreadable item that a keep-going
	// read found, in the order of the read. It is empty after a read
	// without the keep-going option.
	Damaged []Damage

	// Notices lists each fact of the read that is not damage, such as a
	// fec_scheme that this reader cannot use.
	Notices []string

	damagedIDs map[object.ID]struct{}
}

// ObjectIntact reports whether INDEX lists id and the object passed its
// check.
func (r *ReadResult) ObjectIntact(id object.ID) bool {
	if _, bad := r.damagedIDs[id]; bad {
		return false
	}
	_, found := slices.BinarySearchFunc(r.Index.Objects, id, func(row format.IndexObjectRecord, id object.ID) int {
		return bytes.Compare(row.ContentID[:], id[:])
	})
	return found
}

// Damage is one damaged or unreadable item of a disc tree. An object has
// ID, in the full text form, and Kind. A file that is not an object has
// File and an empty ID.
type Damage struct {
	ID     string
	Kind   format.ObjectKind
	File   string
	Reason string

	objectID object.ID
	err      error
}

// Error gives the damage as the error that a read without the keep-going
// option returns.
func (d Damage) Error() string {
	if d.ID != "" {
		return "object " + d.ID + ": " + d.Reason
	}
	return d.File + ": " + d.Reason
}

// Unwrap gives the cause of the damage.
func (d Damage) Unwrap() error { return d.err }

func objectDamage(row format.IndexObjectRecord, err error) Damage {
	id := object.ID(row.ContentID)
	return Damage{ID: id.TextForm(), Kind: row.Kind, Reason: err.Error(), objectID: id, err: err}
}

func fileDamage(file string, err error) Damage {
	return Damage{File: file, Reason: err.Error(), err: err}
}

// errFileHash is the reason for a file whose bytes do not match the
// length and the file_hash of its INDEX Files row.
var errFileHash = errors.New("does not match its INDEX file_hash")

// ReadOptions changes how ReadWithOptions reads a disc tree. The zero
// value gives the behaviour of Read.
type ReadOptions struct {
	// Progress reports the bytes hashed. A nil Progress reports nothing.
	Progress *progress.Reporter

	// KeepGoing continues the read after damage to an object, REFS.bin,
	// DISCS.bin, README.txt, FORMAT.txt, one of the two run header
	// copies, and lists each damaged item in
	// ReadResult.Damaged. Damage to DISC.bin, to both run header copies
	// or to INDEX.bin stops the read with an error also with KeepGoing.
	KeepGoing bool
}

// damageLog collects the damage of one read. Without keepGoing, add
// gives the damage back as the error of the read.
type damageLog struct {
	keepGoing bool
	list      []Damage
	ids       map[object.ID]struct{}
}

func (l *damageLog) add(d Damage) error {
	if !l.keepGoing {
		return d
	}
	l.list = append(l.list, d)
	if d.ID != "" {
		l.ids[d.objectID] = struct{}{}
	}
	return nil
}

// Read reads a mounted disc, or an unpacked NOAHSARK tree, from root: root
// itself, or root/NOAHSARK when root does not already end in NOAHSARK. It
// decodes every structure, checks every file that INDEX lists with a
// file_hash, verifies every content id, verifies that every run header
// copy is byte-identical. The first damage is an error. A run whose
// fec_scheme is not 0 is read in full, and ReadResult.Notices says that
// this reader cannot use the scheme.
func Read(root string) (*ReadResult, error) {
	return ReadWithOptions(root, ReadOptions{})
}

// ReadWithProgress is Read, reporting bytes hashed while it verifies
// every object, through prog. A nil prog reports nothing.
func ReadWithProgress(root string, prog *progress.Reporter) (*ReadResult, error) {
	return ReadWithOptions(root, ReadOptions{Progress: prog})
}

// ReadWithOptions is Read, changed by opts.
func ReadWithOptions(root string, opts ReadOptions) (*ReadResult, error) {
	prog := opts.Progress
	damage := &damageLog{keepGoing: opts.KeepGoing, ids: make(map[object.ID]struct{})}
	cache := NewNameCache()
	base, err := FindNoahsark(root, cache)
	if err != nil {
		return nil, err
	}
	if err := CheckTree(root, base); err != nil {
		return nil, err
	}

	discBuf, err := os.ReadFile(filepath.Join(base, cache.Resolve(base, "DISC.bin")))
	if err != nil {
		return nil, fmt.Errorf("DISC.bin: %w", err)
	}
	var disc format.Disc
	if err := disc.Decode(discBuf); err != nil {
		return nil, fmt.Errorf("DISC.bin: %w", err)
	}

	runsDir := filepath.Join(base, cache.Resolve(base, "runs"))
	runDir, err := NewestRunDir(runsDir)
	if err != nil {
		return nil, err
	}

	header, err := ReadRunHeader(runDir, cache)
	if err != nil {
		return nil, err
	}
	run := header.Run
	runCopies := 1
	if header.FirstDamage != nil {
		if err := damage.add(fileDamage("RUN.bin", header.FirstDamage)); err != nil {
			return nil, err
		}
	} else {
		run2Buf, err := os.ReadFile(filepath.Join(runDir, cache.Resolve(runDir, "RUN2.bin")))
		if err == nil && !bytes.Equal(header.Raw, run2Buf) {
			err = errors.New("does not match RUN.bin")
		}
		if err != nil {
			if err := damage.add(fileDamage("RUN2.bin", err)); err != nil {
				return nil, err
			}
		} else {
			runCopies++
		}
	}

	indexBuf, err := os.ReadFile(filepath.Join(runDir, cache.Resolve(runDir, "INDEX.bin")))
	if err != nil {
		return nil, fmt.Errorf("INDEX.bin: %w", err)
	}
	var idx format.Index
	if _, err := idx.Decode(indexBuf); err != nil {
		return nil, fmt.Errorf("INDEX.bin: %w", err)
	}
	if run.IndexBytes != uint64(len(indexBuf)) || run.IndexHash != sha256sum(indexBuf) {
		return nil, fmt.Errorf("%s index_hash does not match INDEX.bin", header.File)
	}
	if idx.RunSeq != run.RunSeq {
		return nil, fmt.Errorf("INDEX.bin: run_seq %d differs from the run_seq %d of %s", idx.RunSeq, run.RunSeq, header.File)
	}
	if err := checkFileHash(&idx, format.FileRoleDisc, discBuf, "DISC.bin"); err != nil {
		return nil, err
	}

	catalogDir := cache.Join(runDir, "catalog")
	var refs format.RefsTable
	refsErr := readTable(filepath.Join(catalogDir, cache.Resolve(catalogDir, "REFS.bin")), &idx, format.FileRoleRefs, refs.Decode)
	if refsErr != nil {
		refs = format.RefsTable{}
		if err := damage.add(fileDamage("REFS.bin", refsErr)); err != nil {
			return nil, err
		}
	}

	var discs format.DiscsTable
	discsErr := readTable(filepath.Join(catalogDir, cache.Resolve(catalogDir, "DISCS.bin")), &idx, format.FileRoleDiscs, discs.Decode)
	if discsErr != nil {
		discs = format.DiscsTable{}
		if err := damage.add(fileDamage("DISCS.bin", discsErr)); err != nil {
			return nil, err
		}
	}

	if err := verifyTextFiles(base, &idx, cache, damage); err != nil {
		return nil, err
	}

	if err := verifyObjects(base, &idx, prog, cache, damage); err != nil {
		return nil, err
	}

	var notices []string
	if run.FECScheme != format.FECSchemeNone {
		notices = append(notices, fmt.Sprintf("%s: fec_scheme %d: this reader cannot use the scheme; it read every object without it", header.File, run.FECScheme))
	}

	return &ReadResult{
		Disc: disc, Run: run, Index: idx, Refs: refs, Discs: discs,
		RefsIntact: refsErr == nil, DiscsIntact: discsErr == nil,
		ObjectsVerified: len(idx.Objects) - len(damage.ids), RunCopies: runCopies,
		Damaged: damage.list, Notices: notices, damagedIDs: damage.ids,
	}, nil
}

// readTable reads the catalog table at path, decodes it with decode, and
// checks it against the file_hash of the INDEX Files row of role.
func readTable(path string, idx *format.Index, role uint8, decode func([]byte) (int, error)) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := decode(buf); err != nil {
		return err
	}
	row, ok := filesRow(idx, role)
	if !ok {
		return errors.New("INDEX has no Files row for it")
	}
	if row.ByteLen != uint64(len(buf)) || row.FileHash != sha256sum(buf) {
		return errFileHash
	}
	return nil
}

// verifyTextFiles checks README.txt and FORMAT.txt against the file_hash
// of their INDEX Files rows. It gives each damaged or unreadable file to
// damage.
func verifyTextFiles(base string, idx *format.Index, cache *NameCache, damage *damageLog) error {
	names := map[uint8]string{
		format.FileRoleReadme: "README.txt",
		format.FileRoleFormat: "FORMAT.txt",
	}
	for _, row := range idx.Files {
		name, ok := names[row.Role]
		if !ok {
			continue
		}
		path := filepath.Join(base, cache.Resolve(base, name))
		if err := checkFileHashAt(path, row); err != nil {
			if err := damage.add(fileDamage(name, err)); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkFileHashAt checks the file at path against the length and the
// file_hash of row. It reads the file in pieces.
func checkFileHashAt(path string, row format.IndexFileRecord) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if uint64(n) != row.ByteLen || [32]byte(h.Sum(nil)) != row.FileHash {
		return errFileHash
	}
	return nil
}

// FindNoahsark returns root if it already holds DISC.bin, or
// root/NOAHSARK otherwise, resolving both names case-insensitively
// through cache.
func FindNoahsark(root string, cache *NameCache) (string, error) {
	if _, err := os.Stat(filepath.Join(root, cache.Resolve(root, "DISC.bin"))); err == nil {
		return root, nil
	}
	nested := filepath.Join(root, cache.Resolve(root, "NOAHSARK"))
	if _, err := os.Stat(filepath.Join(nested, cache.Resolve(nested, "DISC.bin"))); err == nil {
		return nested, nil
	}
	return "", fmt.Errorf("no DISC.bin under %s or %s", root, nested)
}

// CheckTree refuses a disc tree that holds an entry that is not a
// regular file or a directory. The tool never writes a symlink or a
// special file on a disc, and a reader must not follow one out of the
// disc root. root is the path that the operator gave and can itself be a
// symlink; base is the NOAHSARK directory that FindNoahsark found under
// it. A directory that cannot be listed is skipped: each file under it
// is damage that the read of the file reports.
func CheckTree(root, base string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, base)
	if err != nil {
		return err
	}
	walkBase := filepath.Join(realRoot, rel)
	return filepath.WalkDir(walkBase, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d == nil {
				return err
			}
			return nil
		}
		if d.IsDir() || d.Type().IsRegular() {
			return nil
		}
		name, rerr := filepath.Rel(walkBase, path)
		if rerr != nil {
			name = path
		}
		return fmt.Errorf("%s is not a regular file or a directory; a disc holds no other kind of entry", name)
	})
}

// RunHeader is the run header of one run, read from one of its two
// copies.
type RunHeader struct {
	Run format.Run
	// File is the name of the copy that gave Run: RUN.bin or RUN2.bin.
	File string
	// Raw is the bytes of that copy.
	Raw []byte
	// FirstDamage is the damage of RUN.bin when Run came from RUN2.bin.
	// It is nil when RUN.bin passed its checks.
	FirstDamage error
}

// ReadRunHeader reads the run header of the run in runDir: RUN.bin, or
// RUN2.bin when RUN.bin cannot be read or fails its checks. It applies
// the same checks to both copies. It returns an error when neither copy
// passes.
func ReadRunHeader(runDir string, cache *NameCache) (*RunHeader, error) {
	run, raw, firstErr := readRunCopy(filepath.Join(runDir, cache.Resolve(runDir, "RUN.bin")))
	if firstErr == nil {
		return &RunHeader{Run: run, File: "RUN.bin", Raw: raw}, nil
	}
	run, raw, secondErr := readRunCopy(filepath.Join(runDir, cache.Resolve(runDir, "RUN2.bin")))
	if secondErr != nil {
		return nil, fmt.Errorf("RUN.bin: %w; RUN2.bin: %w", firstErr, secondErr)
	}
	return &RunHeader{Run: run, File: "RUN2.bin", Raw: raw, FirstDamage: firstErr}, nil
}

// readRunCopy reads and checks one run header copy: its length, magic,
// version, CRC and hash algorithm.
func readRunCopy(path string) (format.Run, []byte, error) {
	var run format.Run
	buf, err := os.ReadFile(path)
	if err != nil {
		return run, nil, err
	}
	if len(buf) != RunFileLen {
		return run, nil, fmt.Errorf("want %d bytes, got %d", RunFileLen, len(buf))
	}
	if err := run.Decode(buf); err != nil {
		return run, nil, err
	}
	if run.HashAlgo != format.HashAlgoSHA256 {
		return run, nil, fmt.Errorf("hash_algo 0x%02x is not sha2-256 (0x%02x)", uint8(run.HashAlgo), uint8(format.HashAlgoSHA256))
	}
	return run, buf, nil
}

// NewestRunDir returns the run directory with the highest numeric
// <seq>, comparing the ten-digit names as integers, never as plain
// strings.
func NewestRunDir(runsDir string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("runs directory: %w", err)
	}
	var seqs []int64
	byName := make(map[int64]string)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			continue
		}
		seqs = append(seqs, n)
		byName[n] = e.Name()
	}
	if len(seqs) == 0 {
		return "", fmt.Errorf("no run directory under %s", runsDir)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] > seqs[j] })
	return filepath.Join(runsDir, byName[seqs[0]]), nil
}

// verifyObjects checks every Objects row's header_crc32c and content id
// against the stored bytes read from disc, the same check
// object.ReadVerified runs for restore, so verify and restore cannot
// silently drift apart on what "a good object" means. It gives each
// damaged or unreadable object to damage. An object file is at most the
// maximum chunk size, so the memory this holds is bounded by chunk size,
// never by how much data the disc carries.
func verifyObjects(base string, idx *format.Index, prog *progress.Reporter, cache *NameCache, damage *damageLog) error {
	paths, err := ObjectPaths(base, idx, cache)
	if err != nil {
		return err
	}
	var total int64
	for _, row := range objectFileRows(idx) {
		total += int64(row.ByteLen)
	}
	prog.Start("verify: objects hashed", total)
	for i, row := range idx.Objects {
		n, err := verifyOneObject(paths[i], object.ID(row.ContentID))
		if err != nil {
			if err := damage.add(objectDamage(row, err)); err != nil {
				return err
			}
			continue
		}
		prog.Add(n)
	}
	prog.Done()
	return nil
}

// objectFileRows returns the role 13 Files rows, in row order. The j-th
// of them describes the file of Objects row j.
func objectFileRows(idx *format.Index) []format.IndexFileRecord {
	rows := make([]format.IndexFileRecord, 0, idx.ObjectCount)
	for _, row := range idx.Files {
		if row.Role == format.FileRoleObject {
			rows = append(rows, row)
		}
	}
	return rows
}

// ObjectPaths returns the path of every object file of a run, in the row
// order of INDEX's Objects table. The id gives the file name and the
// kind gives the directory.
func ObjectPaths(base string, idx *format.Index, cache *NameCache) ([]string, error) {
	if len(objectFileRows(idx)) != len(idx.Objects) {
		return nil, fmt.Errorf("INDEX: %d role 13 rows for %d Objects rows", len(objectFileRows(idx)), len(idx.Objects))
	}
	objectsDir := cache.Join(base, "objects")
	snapshotsDir := cache.Join(base, "snapshots")
	paths := make([]string, len(idx.Objects))
	for i, row := range idx.Objects {
		id := object.ID(row.ContentID)
		if row.Kind == format.ObjectKindSnapshot {
			paths[i] = filepath.Join(snapshotsDir, id.TextForm())
		} else {
			paths[i] = filepath.Join(objectsDir, id.FanoutByte(), id.TextForm())
		}
	}
	return paths, nil
}

// checkFileHash checks buf against the file_hash INDEX's Files table
// records for role: the check restore and recover rely on for a
// structure, such as REFS or DISCS, that carries no CRC of its own. name
// names the file in an error.
func checkFileHash(idx *format.Index, role uint8, buf []byte, name string) error {
	row, ok := filesRow(idx, role)
	if !ok {
		return fmt.Errorf("INDEX: no Files row for %s", name)
	}
	if row.ByteLen != uint64(len(buf)) || row.FileHash != sha256sum(buf) {
		return fmt.Errorf("%s does not match its INDEX file_hash", name)
	}
	return nil
}

// filesRow returns the first INDEX Files row of role.
func filesRow(idx *format.Index, role uint8) (format.IndexFileRecord, bool) {
	i := slices.IndexFunc(idx.Files, func(row format.IndexFileRecord) bool { return row.Role == role })
	if i < 0 {
		return format.IndexFileRecord{}, false
	}
	return idx.Files[i], true
}

// verifyOneObject reads the object file at path through
// object.ReadVerified, the same read-and-check restore runs before it
// trusts an object. It returns the stored byte count read, for progress
// reporting.
func verifyOneObject(path string, id object.ID) (int64, error) {
	raw, _, err := object.ReadVerified(path, id)
	if err != nil {
		return 0, err
	}
	return int64(len(raw)) - int64(format.CommonHeaderLen+format.ObjectHeaderLen), nil
}
