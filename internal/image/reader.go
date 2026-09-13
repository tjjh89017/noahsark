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
	base, err := FindNoahsark(root)
	if err != nil {
		return nil, err
	}

	discBuf, err := os.ReadFile(filepath.Join(base, "DISC.bin"))
	if err != nil {
		return nil, fmt.Errorf("image: DISC.bin: %w", err)
	}
	var disc format.Disc
	if err := disc.Decode(discBuf); err != nil {
		return nil, fmt.Errorf("image: DISC.bin: %w", err)
	}

	runDir, err := NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		return nil, err
	}

	runBuf, err := os.ReadFile(filepath.Join(runDir, "RUN.bin"))
	if err != nil {
		return nil, fmt.Errorf("image: RUN.bin: %w", err)
	}
	if len(runBuf) != RunFileLen {
		return nil, fmt.Errorf("image: RUN.bin: want %d bytes, got %d", RunFileLen, len(runBuf))
	}
	var run format.Run
	if err := run.Decode(runBuf[:format.RunLen]); err != nil {
		return nil, fmt.Errorf("image: RUN.bin: %w", err)
	}

	run2Buf, err := os.ReadFile(filepath.Join(runDir, "RUN2.bin"))
	if err != nil {
		return nil, fmt.Errorf("image: RUN2.bin: %w", err)
	}
	runCopies := 1
	if !bytes.Equal(runBuf, run2Buf) {
		return nil, fmt.Errorf("image: RUN2.bin does not match RUN.bin")
	}
	runCopies++

	indexBuf, err := os.ReadFile(filepath.Join(runDir, "INDEX.bin"))
	if err != nil {
		return nil, fmt.Errorf("image: INDEX.bin: %w", err)
	}
	var idx format.Index
	if _, err := idx.Decode(indexBuf); err != nil {
		return nil, fmt.Errorf("image: INDEX.bin: %w", err)
	}
	if run.IndexBytes != uint64(len(indexBuf)) || run.IndexHash != sha256sum(indexBuf) {
		return nil, fmt.Errorf("image: RUN.bin index_hash does not match INDEX.bin")
	}

	refsBuf, err := os.ReadFile(filepath.Join(runDir, "catalog", "REFS.bin"))
	if err != nil {
		return nil, fmt.Errorf("image: REFS.bin: %w", err)
	}
	var refs format.RefsTable
	if _, err := refs.Decode(refsBuf); err != nil {
		return nil, fmt.Errorf("image: REFS.bin: %w", err)
	}

	discsBuf, err := os.ReadFile(filepath.Join(runDir, "catalog", "DISCS.bin"))
	if err != nil {
		return nil, fmt.Errorf("image: DISCS.bin: %w", err)
	}
	var discs format.DiscsTable
	if _, err := discs.Decode(discsBuf); err != nil {
		return nil, fmt.Errorf("image: DISCS.bin: %w", err)
	}

	// Every parity file's header block must match RUN.bin exactly. Read
	// only that header, never the file's parity payload. A scheme 0 run
	// (no FEC) carries no parity files, so there is nothing to check
	// here and no checksum column or parity to recompute below: verify
	// checks content ids and file hashes only.
	if run.FECScheme == format.FECSchemeRS255GF8 {
		for j := range fec.M {
			path := filepath.Join(runDir, "parity", fmt.Sprintf("p%04d.bin", fec.K+1+j))
			f, err := os.Open(path)
			if err != nil {
				return nil, fmt.Errorf("image: parity column %d: %w", j, err)
			}
			header := make([]byte, RunFileLen)
			_, err = io.ReadFull(f, header)
			_ = f.Close()
			if err != nil {
				return nil, fmt.Errorf("image: parity column %d: %w", j, err)
			}
			if !bytes.Equal(header, runBuf) {
				return nil, fmt.Errorf("image: parity column %d header does not match RUN.bin", j)
			}
			runCopies++
		}
	}

	if err := verifyObjects(base, &idx); err != nil {
		return nil, err
	}

	if run.FECScheme == format.FECSchemeRS255GF8 {
		if err := verifyFEC(base, runDir); err != nil {
			return nil, err
		}
	}

	return &ReadResult{
		Disc: disc, Run: run, Index: idx, Refs: refs, Discs: discs,
		ObjectsVerified: len(idx.Objects), RunCopies: runCopies,
	}, nil
}

// FindNoahsark returns root if it already holds DISC.bin, or
// root/NOAHSARK otherwise.
func FindNoahsark(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "DISC.bin")); err == nil {
		return root, nil
	}
	nested := filepath.Join(root, "NOAHSARK")
	if _, err := os.Stat(filepath.Join(nested, "DISC.bin")); err == nil {
		return nested, nil
	}
	return "", fmt.Errorf("image: no DISC.bin under %s or %s", root, nested)
}

// NewestRunDir returns the run directory with the highest numeric
// <seq>, comparing the ten-digit names as integers, never as plain
// strings.
func NewestRunDir(runsDir string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("image: runs directory: %w", err)
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
		return "", fmt.Errorf("image: no run directory under %s", runsDir)
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
func verifyObjects(base string, idx *format.Index) error {
	headerLen := int64(format.CommonHeaderLen + format.ObjectHeaderLen)
	for _, row := range idx.Objects {
		id := object.ID(row.ContentID)
		var path string
		if row.Kind == format.ObjectKindSnapshot {
			path = filepath.Join(base, "snapshots", id.TextForm())
		} else {
			path = filepath.Join(base, "objects", id.FanoutByte(), id.TextForm())
		}
		if err := verifyOneObject(path, id, row.Offset, row.StoredLen, row.Compression, headerLen); err != nil {
			return err
		}
	}
	return nil
}

// verifyOneObject streams the object file at path, from headerLen+offset
// for storedLen bytes, decompressing when compression says so, and
// checks the result hashes to id.
func verifyOneObject(path string, id object.ID, offset, storedLen uint64, compression format.Compression, headerLen int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("image: object %s: %w", id.TextForm(), err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(headerLen+int64(offset), io.SeekStart); err != nil {
		return fmt.Errorf("image: object %s: %w", id.TextForm(), err)
	}
	got, err := object.HashStreamed(f, compression, storedLen)
	if err != nil {
		return fmt.Errorf("image: object %s: %w", id.TextForm(), err)
	}
	if object.ID(got) != id {
		return fmt.Errorf("image: object %s: content id does not verify", id.TextForm())
	}
	return nil
}

// StreamFiles resolves the FEC stream's file paths and sizes for one run
// directory, in INDEX's Files table row order: the order Build wrote
// them in and the order the stream concatenates them in.
//
// The Files table stores no file name. A fixed-name row (INDEX, DISC,
// README.txt, FORMAT.txt, decoder.py, REFS, DISCS) is found by its role.
// An object row (role 13) is found through the Objects table's
// file_index field, which names that object's own Files row directly;
// this holds even when the object's bytes are damaged, since it never
// depends on hashing the file's current, possibly-corrupt content. A
// snapobj row (role 9) carries no such field in this version, so it is
// found by sorting the candidate files by their own name, the same
// content id order Build used to order those rows, and matching that
// order position by position against the run's consecutive rows of that
// role.
func StreamFiles(base, runDir string) (paths []string, sizes []uint64, idx *format.Index, err error) {
	indexBuf, err := os.ReadFile(filepath.Join(runDir, "INDEX.bin"))
	if err != nil {
		return nil, nil, nil, err
	}
	var decoded format.Index
	if _, err := decoded.Decode(indexBuf); err != nil {
		return nil, nil, nil, err
	}

	snapobjPaths, err := idSortedFiles(filepath.Join(runDir, "catalog", "snapobj"))
	if err != nil {
		return nil, nil, nil, err
	}

	objectPathByFileIndex := make(map[int]string, len(decoded.Objects))
	for _, row := range decoded.Objects {
		id := object.ID(row.ContentID)
		var p string
		if row.Kind == format.ObjectKindSnapshot {
			p = filepath.Join(base, "snapshots", id.TextForm())
		} else {
			p = filepath.Join(base, "objects", id.FanoutByte(), id.TextForm())
		}
		objectPathByFileIndex[int(row.FileIndex)] = p
	}

	snapIdx := 0
	for i, row := range decoded.Files {
		var path string
		var inStream bool
		switch row.Role {
		case format.FileRoleSnapobj:
			if snapIdx >= len(snapobjPaths) {
				return nil, nil, nil, fmt.Errorf("image: fewer snapobj files than INDEX rows")
			}
			path, inStream = snapobjPaths[snapIdx], true
			snapIdx++
		case format.FileRoleObject:
			p, ok := objectPathByFileIndex[i]
			if !ok {
				return nil, nil, nil, fmt.Errorf("image: no Objects row names file_index %d", i)
			}
			path, inStream = p, true
		default:
			path, inStream = filesRowPath(base, runDir, row.Role)
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
func verifyFEC(base, runDir string) error {
	streamPaths, streamSizes, _, err := StreamFiles(base, runDir)
	if err != nil {
		return err
	}
	for i, p := range streamPaths {
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("image: stream file %d: %w", i, err)
		}
		if uint64(fi.Size()) != streamSizes[i] {
			return fmt.Errorf("image: stream file %d: length changed since INDEX was built", i)
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

	runBuf, err := os.ReadFile(filepath.Join(runDir, "RUN.bin"))
	if err != nil {
		return err
	}

	parityPaths := make([]string, fec.M)
	for j := range fec.M {
		parityPaths[j] = filepath.Join(runDir, "parity", fmt.Sprintf("p%04d.bin", fec.K+1+j))
	}
	return compareFEC(sources, layout, runBuf, filepath.Join(runDir, "checksum.bin"), parityPaths)
}

// compareFEC recomputes the checksum column and the m parity files over
// sources and compares each stripe against the checksum and parity files
// already on disk, one stripe at a time.
func compareFEC(sources []streamSource, layout *fec.StreamLayout, runHeaderCopy []byte, checksumPath string, parityPaths []string) error {
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
		headerBuf := make([]byte, RunFileLen)
		if _, err := io.ReadFull(f, headerBuf); err != nil {
			return fmt.Errorf("image: parity column %d: %w", j, err)
		}
		if !bytes.Equal(headerBuf, runHeaderCopy) {
			return fmt.Errorf("image: parity column %d header does not match RUN.bin", j)
		}
	}

	data := make([][]byte, fec.K)
	for c := range data {
		data[c] = make([]byte, fec.BlockSize)
	}
	wantRec := make([]byte, format.ChecksumRecordLen)
	gotRec := make([]byte, format.ChecksumRecordLen)
	wantParity := make([]byte, fec.BlockSize)
	gotParity := make([]byte, fec.BlockSize)

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
			return fmt.Errorf("image: checksum.bin: stripe %d: %w", i, err)
		}
		if !bytes.Equal(wantRec, gotRec) {
			return fmt.Errorf("image: checksum.bin does not match the recomputed checksum column")
		}

		parityBlocks, err := codec.Encode(data)
		if err != nil {
			return err
		}
		for j := range parityBlocks {
			copy(wantParity, parityBlocks[j])
			if _, err := io.ReadFull(parityFiles[j], gotParity); err != nil {
				return fmt.Errorf("image: parity column %d: stripe %d: %w", j, i, err)
			}
			if !bytes.Equal(wantParity, gotParity) {
				return fmt.Errorf("image: parity column %d does not match the recomputed parity", j)
			}
		}
	}
	return nil
}

// idSortedFiles lists the regular files directly under dir and returns
// their paths sorted by their own file name ascending: for
// catalog/snapobj, that name is the snapshot object's content id text
// form, whose hex digits sort in the same order as the id's own bytes.
// This matches Build and Pack's snapObjOrder, which sorts those rows by
// content id, not by the file's own bytes. A missing dir is not an
// error; it yields no files.
func idSortedFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filepath.Join(dir, name)
	}
	return paths, nil
}

// filesRowPath returns the path of a fixed-name INDEX Files row, given
// its role, and whether that role's rows enter the FEC stream. A role
// this function does not list is either outside the stream (RUN, RUN2,
// checksum, parity) or resolved by the caller instead (snapobj, object).
func filesRowPath(base, runDir string, role uint8) (string, bool) {
	switch role {
	case format.FileRoleIndex:
		return filepath.Join(runDir, "INDEX.bin"), true
	case format.FileRoleDisc:
		return filepath.Join(base, "DISC.bin"), true
	case format.FileRoleReadme:
		return filepath.Join(base, "README.txt"), true
	case format.FileRoleFormat:
		return filepath.Join(base, "FORMAT.txt"), true
	case format.FileRoleReference:
		return filepath.Join(base, "REFERENCE", "decoder.py"), true
	case format.FileRoleRefs:
		return filepath.Join(runDir, "catalog", "REFS.bin"), true
	case format.FileRoleDiscs:
		return filepath.Join(runDir, "catalog", "DISCS.bin"), true
	default:
		return "", false
	}
}
