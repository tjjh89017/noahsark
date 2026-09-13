package image

import (
	"bytes"
	"fmt"
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
	base, err := findNoahsark(root)
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

	runDir, err := newestRunDir(filepath.Join(base, "runs"))
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

	// Every parity file's header block must match RUN.bin exactly.
	for j := 0; j < fec.M; j++ {
		p, err := os.ReadFile(filepath.Join(runDir, "parity", fmt.Sprintf("p%04d.bin", fec.K+1+j)))
		if err != nil {
			return nil, fmt.Errorf("image: parity column %d: %w", j, err)
		}
		if len(p) < RunFileLen || !bytes.Equal(p[:RunFileLen], runBuf) {
			return nil, fmt.Errorf("image: parity column %d header does not match RUN.bin", j)
		}
		runCopies++
	}

	if err := verifyObjects(base, &idx); err != nil {
		return nil, err
	}

	if err := verifyFEC(base, runDir); err != nil {
		return nil, err
	}

	return &ReadResult{
		Disc: disc, Run: run, Index: idx, Refs: refs, Discs: discs,
		ObjectsVerified: len(idx.Objects), RunCopies: runCopies,
	}, nil
}

// findNoahsark returns root if it already holds DISC.bin, or
// root/NOAHSARK otherwise.
func findNoahsark(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "DISC.bin")); err == nil {
		return root, nil
	}
	nested := filepath.Join(root, "NOAHSARK")
	if _, err := os.Stat(filepath.Join(nested, "DISC.bin")); err == nil {
		return nested, nil
	}
	return "", fmt.Errorf("image: no DISC.bin under %s or %s", root, nested)
}

// newestRunDir returns the run directory with the highest numeric
// <seq>, comparing the ten-digit names as integers, never as plain
// strings.
func newestRunDir(runsDir string) (string, error) {
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

// verifyObjects checks every Objects row's content id against the payload
// bytes read from disc, decompressing when the row says compression was
// used.
func verifyObjects(base string, idx *format.Index) error {
	for _, row := range idx.Objects {
		id := object.ID(row.ContentID)
		var path string
		if row.Kind == format.ObjectKindSnapshot {
			path = filepath.Join(base, "snapshots", id.TextForm())
		} else {
			path = filepath.Join(base, "objects", id.FanoutByte(), id.TextForm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("image: object %s: %w", id.TextForm(), err)
		}
		headerLen := format.CommonHeaderLen + format.ObjectHeaderLen
		if uint64(len(data)) < uint64(headerLen)+row.Offset+row.StoredLen {
			return fmt.Errorf("image: object %s: file too short", id.TextForm())
		}
		stored := data[uint64(headerLen)+row.Offset : uint64(headerLen)+row.Offset+row.StoredLen]
		payload, err := object.Decompress(stored, row.Compression, row.PayloadLen)
		if err != nil {
			return fmt.Errorf("image: object %s: %w", id.TextForm(), err)
		}
		if object.ComputeID(payload) != id {
			return fmt.Errorf("image: object %s: content id does not verify", id.TextForm())
		}
	}
	return nil
}

// verifyFEC rebuilds the checksum column and the parity from the run's
// own files, in INDEX's Files table order, and compares them to what is
// on disc.
//
// The Files table stores no file name. A fixed-name row (INDEX, DISC,
// decoder.py, REFS, DISCS) is found by its role. A snapobj row (role 9)
// or an object row (role 13) is found by sorting the candidate files by
// their own file_hash, the same rule Build used to order those rows, and
// matching that order position by position against the run's consecutive
// rows of that role.
func verifyFEC(base, runDir string) error {
	indexBuf, err := os.ReadFile(filepath.Join(runDir, "INDEX.bin"))
	if err != nil {
		return err
	}
	var idx format.Index
	if _, err := idx.Decode(indexBuf); err != nil {
		return err
	}

	snapobjPaths, err := hashSortedFiles(filepath.Join(runDir, "catalog", "snapobj"))
	if err != nil {
		return err
	}
	var objectDirs []string
	objRoot := filepath.Join(base, "objects")
	fanouts, err := os.ReadDir(objRoot)
	if err == nil {
		for _, f := range fanouts {
			if f.IsDir() {
				objectDirs = append(objectDirs, filepath.Join(objRoot, f.Name()))
			}
		}
	}
	objectDirs = append(objectDirs, filepath.Join(base, "snapshots"))
	var objectPaths []string
	for _, d := range objectDirs {
		paths, err := hashSortedFiles(d)
		if err != nil {
			return err
		}
		objectPaths = append(objectPaths, paths...)
	}
	sort.Slice(objectPaths, func(i, j int) bool {
		return compareBytes(fileHashBytes(objectPaths[i]), fileHashBytes(objectPaths[j])) < 0
	})

	var streamSizes []uint64
	var streamPaths []string
	snapIdx, objIdx := 0, 0
	for _, row := range idx.Files {
		var path string
		var inStream bool
		switch row.Role {
		case format.FileRoleSnapobj:
			if snapIdx >= len(snapobjPaths) {
				return fmt.Errorf("image: fewer snapobj files than INDEX rows")
			}
			path, inStream = snapobjPaths[snapIdx], true
			snapIdx++
		case format.FileRoleObject:
			if objIdx >= len(objectPaths) {
				return fmt.Errorf("image: fewer object files than INDEX rows")
			}
			path, inStream = objectPaths[objIdx], true
			objIdx++
		default:
			path, inStream = filesRowPath(base, runDir, row.Role)
		}
		if !inStream {
			continue
		}
		streamSizes = append(streamSizes, row.ByteLen)
		streamPaths = append(streamPaths, path)
	}

	layout, err := fec.NewStreamLayout(streamSizes, fec.K)
	if err != nil {
		return err
	}
	var stream []byte
	for i, p := range streamPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("image: stream file %d: %w", i, err)
		}
		if uint64(len(data)) != streamSizes[i] {
			return fmt.Errorf("image: stream file %d: length changed since INDEX was built", i)
		}
		stream = append(stream, data...)
		if pad := padLen(len(data)); pad > 0 {
			stream = append(stream, make([]byte, pad)...)
		}
	}

	runBuf, err := os.ReadFile(filepath.Join(runDir, "RUN.bin"))
	if err != nil {
		return err
	}
	checksum, parity, err := buildFEC(stream, layout, runBuf)
	if err != nil {
		return err
	}

	diskChecksum, err := os.ReadFile(filepath.Join(runDir, "checksum.bin"))
	if err != nil {
		return err
	}
	if !bytes.Equal(checksum, diskChecksum) {
		return fmt.Errorf("image: checksum.bin does not match the recomputed checksum column")
	}
	for j := 0; j < fec.M; j++ {
		diskParity, err := os.ReadFile(filepath.Join(runDir, "parity", fmt.Sprintf("p%04d.bin", fec.K+1+j)))
		if err != nil {
			return err
		}
		if !bytes.Equal(parity[j], diskParity) {
			return fmt.Errorf("image: parity column %d does not match the recomputed parity", j)
		}
	}
	return nil
}

// hashSortedFiles lists the regular files directly under dir and returns
// their paths sorted by the sha256 of their own whole-file bytes
// ascending. A missing dir is not an error; it yields no files.
func hashSortedFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Slice(paths, func(i, j int) bool {
		return compareBytes(fileHashBytes(paths[i]), fileHashBytes(paths[j])) < 0
	})
	return paths, nil
}

// fileHashBytes returns sha256 of path's whole bytes, or nil on a read
// error; the caller's later os.ReadFile of the same path reports the
// real error.
func fileHashBytes(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	sum := sha256sum(data)
	return sum[:]
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
