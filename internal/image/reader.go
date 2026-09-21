package image

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// ReadResult holds every structure Read decoded from one disc tree, and
// the checks it ran.
type ReadResult struct {
	Disc  format.Disc
	Run   format.Run
	Index format.Index
	Refs  format.RefsTable
	Discs format.DiscsTable

	ObjectsVerified int
	RunCopies       int
}

// Read reads a mounted disc, or an unpacked NOAHSARK tree, from root: root
// itself, or root/NOAHSARK when root does not already end in NOAHSARK. It
// decodes every structure, verifies every content id, verifies that every
// run header copy is byte-identical, and recomputes the checksum column
// and the parity to verify them against the run's actual FEC stream.
func Read(root string) (*ReadResult, error) {
	return ReadWithProgress(root, nil)
}

// ReadWithProgress is Read, reporting bytes hashed while it verifies
// every object and, when the run carries FEC, the checksum column and
// parity, through prog. A nil prog reports nothing.
func ReadWithProgress(root string, prog *progress.Reporter) (*ReadResult, error) {
	cache := NewNameCache()
	base, err := FindNoahsark(root, cache)
	if err != nil {
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

	runBuf, err := os.ReadFile(filepath.Join(runDir, cache.Resolve(runDir, "RUN.bin")))
	if err != nil {
		return nil, fmt.Errorf("RUN.bin: %w", err)
	}
	if len(runBuf) != RunFileLen {
		return nil, fmt.Errorf("RUN.bin: want %d bytes, got %d", RunFileLen, len(runBuf))
	}
	var run format.Run
	if err := run.Decode(runBuf); err != nil {
		return nil, fmt.Errorf("RUN.bin: %w", err)
	}

	run2Buf, err := os.ReadFile(filepath.Join(runDir, cache.Resolve(runDir, "RUN2.bin")))
	if err != nil {
		return nil, fmt.Errorf("RUN2.bin: %w", err)
	}
	runCopies := 1
	if !bytes.Equal(runBuf, run2Buf) {
		return nil, fmt.Errorf("RUN2.bin does not match RUN.bin")
	}
	runCopies++

	indexBuf, err := os.ReadFile(filepath.Join(runDir, cache.Resolve(runDir, "INDEX.bin")))
	if err != nil {
		return nil, fmt.Errorf("INDEX.bin: %w", err)
	}
	var idx format.Index
	if _, err := idx.Decode(indexBuf); err != nil {
		return nil, fmt.Errorf("INDEX.bin: %w", err)
	}
	if run.IndexBytes != uint64(len(indexBuf)) || run.IndexHash != sha256sum(indexBuf) {
		return nil, fmt.Errorf("RUN.bin index_hash does not match INDEX.bin")
	}

	catalogDir := cache.Join(runDir, "catalog")
	refsBuf, err := os.ReadFile(filepath.Join(catalogDir, cache.Resolve(catalogDir, "REFS.bin")))
	if err != nil {
		return nil, fmt.Errorf("REFS.bin: %w", err)
	}
	var refs format.RefsTable
	if _, err := refs.Decode(refsBuf); err != nil {
		return nil, fmt.Errorf("REFS.bin: %w", err)
	}

	discsBuf, err := os.ReadFile(filepath.Join(catalogDir, cache.Resolve(catalogDir, "DISCS.bin")))
	if err != nil {
		return nil, fmt.Errorf("DISCS.bin: %w", err)
	}
	var discs format.DiscsTable
	if _, err := discs.Decode(discsBuf); err != nil {
		return nil, fmt.Errorf("DISCS.bin: %w", err)
	}

	if err := verifyObjects(base, &idx, prog, cache); err != nil {
		return nil, err
	}

	if run.FECScheme == format.FECSchemeRS255GF8 {
		if err := verifyFEC(base, runDir, prog, cache); err != nil {
			return nil, err
		}
	}

	return &ReadResult{
		Disc: disc, Run: run, Index: idx, Refs: refs, Discs: discs,
		ObjectsVerified: len(idx.Objects), RunCopies: runCopies,
	}, nil
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

// verifyObjects checks every Objects row's content id against the
// stored bytes read from disc, decompressing when the row says
// compression was used. It streams each object's stored bytes straight
// into the hash, never holding a whole object file, or a decompressed
// payload, in memory: an object can be as large as the maximum chunk
// size, so this bound must hold whatever the object's own size is.
func verifyObjects(base string, idx *format.Index, prog *progress.Reporter, cache *NameCache) error {
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
		id := object.ID(row.ContentID)
		n, err := verifyOneObject(paths[i], id)
		if err != nil {
			return err
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

// verifyOneObject reads the object file at path, takes its stored
// length and compression from its own object header, and checks the
// payload hashes to id. It returns the stored byte count it read.
func verifyOneObject(path string, id object.ID) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("object %s: %w", id.TextForm(), err)
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, format.CommonHeaderLen+format.ObjectHeaderLen)
	if _, err := io.ReadFull(f, head); err != nil {
		return 0, fmt.Errorf("object %s: %w", id.TextForm(), err)
	}
	var ch format.CommonHeader
	if err := ch.Decode(head); err != nil {
		return 0, fmt.Errorf("object %s: %w", id.TextForm(), err)
	}
	var oh format.ObjectHeader
	if err := oh.Decode(head[format.CommonHeaderLen:]); err != nil {
		return 0, fmt.Errorf("object %s: %w", id.TextForm(), err)
	}
	got, err := object.HashStreamed(f, oh.Compression, oh.StoredLen)
	if err != nil {
		return 0, fmt.Errorf("object %s: %w", id.TextForm(), err)
	}
	if object.ID(got) != id {
		return 0, fmt.Errorf("object %s: content id does not verify", id.TextForm())
	}
	return int64(oh.StoredLen), nil
}

// StreamFiles resolves the FEC stream's file paths and sizes for one run
// directory, in INDEX's Files table row order: the order Build wrote
// them in and the order the stream concatenates them in.
//
// The Files table stores no file name. A fixed-name row (INDEX, DISC,
// README.txt, FORMAT.txt, decoder.py, REFS, DISCS) is found by its role.
// An object row (role 13) pairs by position with the Objects table: the
// j-th role 13 row describes the file of Objects row j, whose id and
// kind give the path.
func StreamFiles(base, runDir string) (paths []string, sizes []uint64, idx *format.Index, err error) {
	return StreamFilesWithCache(base, runDir, NewNameCache())
}

// StreamFilesWithCache is StreamFiles, resolving every fixed name
// through cache instead of a fresh, single-use one.
func StreamFilesWithCache(base, runDir string, cache *NameCache) (paths []string, sizes []uint64, idx *format.Index, err error) {
	indexBuf, err := os.ReadFile(filepath.Join(runDir, cache.Resolve(runDir, "INDEX.bin")))
	if err != nil {
		return nil, nil, nil, err
	}
	var decoded format.Index
	if _, err := decoded.Decode(indexBuf); err != nil {
		return nil, nil, nil, err
	}

	objectPaths, err := ObjectPaths(base, &decoded, cache)
	if err != nil {
		return nil, nil, nil, err
	}

	objIdx := 0
	for _, row := range decoded.Files {
		var path string
		var inStream bool
		switch row.Role {
		case format.FileRoleObject:
			path, inStream = objectPaths[objIdx], true
			objIdx++
		default:
			path, inStream = filesRowPath(base, runDir, row.Role, cache)
		}
		if !inStream {
			continue
		}
		sizes = append(sizes, row.ByteLen)
		paths = append(paths, path)
	}
	return paths, sizes, &decoded, nil
}

// verifyFEC rebuilds the checksum column and the parity from the run's
// own files, in INDEX's Files table order, and compares them to what is
// on disc, one stripe at a time. It never holds the stream, the whole
// checksum column or a whole parity file in memory.
func verifyFEC(base, runDir string, prog *progress.Reporter, cache *NameCache) error {
	streamPaths, streamSizes, _, err := StreamFilesWithCache(base, runDir, cache)
	if err != nil {
		return err
	}
	for i, p := range streamPaths {
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("stream file %d: %w", i, err)
		}
		if uint64(fi.Size()) != streamSizes[i] {
			return fmt.Errorf("stream file %d: length changed since INDEX was built", i)
		}
	}
	sources := make([]streamSource, len(streamPaths))
	for i, p := range streamPaths {
		sources[i] = streamSource{path: p, size: streamSizes[i]}
	}

	layout, err := fec.NewStreamLayout(streamSizes, fec.K)
	if err != nil {
		return err
	}

	parityDir := cache.Join(runDir, "parity")
	parityPaths := make([]string, fec.M)
	for j := range fec.M {
		name := fmt.Sprintf("p%04d.bin", fec.K+1+j)
		parityPaths[j] = filepath.Join(parityDir, cache.Resolve(parityDir, name))
	}
	checksumPath := filepath.Join(runDir, cache.Resolve(runDir, "checksum.bin"))
	return compareFEC(sources, layout, checksumPath, parityPaths, prog)
}

// compareFEC recomputes the checksum column and the m parity files over
// sources and compares each stripe against the checksum and parity files
// already on disk, one stripe at a time.
func compareFEC(sources []streamSource, layout *fec.StreamLayout, checksumPath string, parityPaths []string, prog *progress.Reporter) error {
	codec, err := fec.NewCodec(fec.K, fec.M)
	if err != nil {
		return err
	}
	L := layout.StripeCount()

	cols := make([]*columnCursor, fec.K)
	for c := range fec.K {
		cols[c] = newColumnCursor(sources, uint64(c)*L)
	}
	defer func() {
		for _, c := range cols {
			_ = c.closeCurrent()
		}
	}()

	checksumFile, err := os.Open(checksumPath)
	if err != nil {
		return err
	}
	defer func() { _ = checksumFile.Close() }()

	parityFiles := make([]*os.File, fec.M)
	for j, p := range parityPaths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		parityFiles[j] = f
	}

	data := make([][]byte, fec.K)
	for c := range data {
		data[c] = make([]byte, fec.BlockSize)
	}
	wantRec := make([]byte, format.ChecksumRecordLen)
	gotRec := make([]byte, format.ChecksumRecordLen)
	wantParity := make([]byte, fec.BlockSize)
	gotParity := make([]byte, fec.BlockSize)

	prog.Start("verify: fec stripes checked", int64(L))
	for i := range L {
		for c := range fec.K {
			if err := cols[c].readBlock(data[c]); err != nil {
				return err
			}
		}
		rec := fec.BuildChecksumRecord(uint32(i), data)
		if err := rec.Encode(wantRec); err != nil {
			return err
		}
		if _, err := io.ReadFull(checksumFile, gotRec); err != nil {
			return fmt.Errorf("checksum.bin: stripe %d: %w", i, err)
		}
		if !bytes.Equal(wantRec, gotRec) {
			return fmt.Errorf("checksum.bin does not match the recomputed checksum column")
		}

		parityBlocks, err := codec.Encode(data)
		if err != nil {
			return err
		}
		for j := range parityBlocks {
			copy(wantParity, parityBlocks[j])
			if _, err := io.ReadFull(parityFiles[j], gotParity); err != nil {
				return fmt.Errorf("parity column %d: stripe %d: %w", j, i, err)
			}
			if !bytes.Equal(wantParity, gotParity) {
				return fmt.Errorf("parity column %d does not match the recomputed parity", j)
			}
		}
		prog.Add(1)
	}
	prog.Done()
	return nil
}

// filesRowPath returns the path of a fixed-name INDEX Files row, given
// its role, and whether that role's rows enter the FEC stream. A role
// this function does not list is either outside the stream (RUN, RUN2,
// checksum, parity) or resolved by the caller instead (an object row).
func filesRowPath(base, runDir string, role uint8, cache *NameCache) (string, bool) {
	switch role {
	case format.FileRoleIndex:
		return filepath.Join(runDir, cache.Resolve(runDir, "INDEX.bin")), true
	case format.FileRoleDisc:
		return filepath.Join(base, cache.Resolve(base, "DISC.bin")), true
	case format.FileRoleReadme:
		return filepath.Join(base, cache.Resolve(base, "README.txt")), true
	case format.FileRoleFormat:
		return filepath.Join(base, cache.Resolve(base, "FORMAT.txt")), true
	case format.FileRoleReference:
		return cache.Join(base, "REFERENCE", "decoder.py"), true
	case format.FileRoleRefs:
		return cache.Join(runDir, "catalog", "REFS.bin"), true
	case format.FileRoleDiscs:
		return cache.Join(runDir, "catalog", "DISCS.bin"), true
	default:
		return "", false
	}
}
