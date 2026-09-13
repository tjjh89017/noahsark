package restore

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// StripeReport describes the repair Heal made to one stripe of the FEC
// stream. A stripe Heal did not have to touch produces no report.
type StripeReport struct {
	// Stripe is the stripe index within the run's checksum column.
	Stripe uint64
	// DataColumns lists the data columns Heal rewrote.
	DataColumns []int
	// ParityColumns lists the parity columns Heal rewrote.
	ParityColumns []int
}

// Heal verifies every data block of the newest run's FEC stream against
// the checksum column, and repairs every stripe that needs it using
// Reed-Solomon parity.
//
// It follows the decode and retry rule: for each stripe, it decodes from
// the k data blocks the checksum column marks good plus enough parity
// blocks to reach k, using the lowest-indexed surviving blocks first.
// When the reconstructed data does not match the checksum column, it
// retries holding out one more parity block at a time, one at a time
// only, up to m minus the known erasure count attempts. A stripe that
// still does not verify after every attempt is a hard error naming that
// stripe, and Heal writes nothing for it; every other stripe's repair
// already written stands.
//
// When outDir is empty, Heal repairs discRoot in place: discRoot must be
// a writable unpacked tree of ordinary files. When outDir is set, Heal
// first copies the whole disc tree from discRoot into outDir and repairs
// the copy there, leaving discRoot untouched — the path to use when
// discRoot is a read-only mount.
func Heal(discRoot, outDir string) ([]StripeReport, error) {
	return HealWithProgress(discRoot, outDir, nil)
}

// HealWithProgress is Heal, reporting stripes checked and repaired
// through prog. A nil prog reports nothing.
func HealWithProgress(discRoot, outDir string, prog *progress.Reporter) ([]StripeReport, error) {
	work := discRoot
	if outDir != "" {
		if err := copyTree(discRoot, outDir); err != nil {
			return nil, err
		}
		work = outDir
	}

	base, err := image.FindNoahsark(work)
	if err != nil {
		return nil, err
	}
	runDir, err := image.NewestRunDir(filepath.Join(base, "runs"))
	if err != nil {
		return nil, err
	}

	runBuf, err := os.ReadFile(filepath.Join(runDir, "RUN.bin"))
	if err != nil {
		return nil, err
	}
	var run format.Run
	if err := run.Decode(runBuf[:format.RunLen]); err != nil {
		return nil, err
	}
	if run.FECScheme != format.FECSchemeRS255GF8 {
		return nil, fmt.Errorf("restore: heal: run %d has no FEC", run.RunSeq)
	}

	paths, sizes, _, err := image.StreamFiles(base, runDir)
	if err != nil {
		return nil, err
	}
	layout, err := fec.NewStreamLayout(sizes, fec.K)
	if err != nil {
		return nil, err
	}
	L := layout.StripeCount()

	streamFiles := make([]*os.File, len(paths))
	for i, p := range paths {
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			return nil, fmt.Errorf("restore: heal: %s: %w", p, err)
		}
		defer func() { _ = f.Close() }()
		streamFiles[i] = f
	}

	checksumFile, err := os.OpenFile(filepath.Join(runDir, "checksum.bin"), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = checksumFile.Close() }()

	parityFiles := make([]*os.File, fec.M)
	for j := range fec.M {
		p := filepath.Join(runDir, "parity", fmt.Sprintf("p%04d.bin", fec.K+1+j))
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		parityFiles[j] = f
	}

	codec, err := fec.NewCodec(fec.K, fec.M)
	if err != nil {
		return nil, err
	}

	var reports []StripeReport
	prog.Start("heal: stripes checked", int64(L))
	for stripe := range L {
		rep, err := healStripe(streamFiles, sizes, layout, checksumFile, parityFiles, codec, stripe, L)
		if err != nil {
			return reports, err
		}
		if rep != nil {
			reports = append(reports, *rep)
		}
		prog.Add(1)
	}
	prog.Done()
	return reports, nil
}

// healStripe repairs one stripe, or returns nil, nil when the stripe was
// already correct.
func healStripe(
	streamFiles []*os.File, sizes []uint64, layout *fec.StreamLayout,
	checksumFile *os.File, parityFiles []*os.File, codec *fec.Codec,
	stripe, L uint64,
) (*StripeReport, error) {
	recBuf := make([]byte, format.ChecksumRecordLen)
	if _, err := checksumFile.ReadAt(recBuf, int64(stripe)*format.ChecksumRecordLen); err != nil {
		return nil, fmt.Errorf("restore: heal: stripe %d: checksum record: %w", stripe, err)
	}
	var rec format.ChecksumRecord
	if err := rec.Decode(recBuf); err != nil {
		return nil, fmt.Errorf("restore: heal: stripe %d: checksum record unusable: %w", stripe, err)
	}

	dataBlocks := make([][]byte, fec.K)
	for c := range fec.K {
		b, err := readStreamBlock(streamFiles, sizes, layout, uint64(c)*L+stripe)
		if err != nil {
			return nil, fmt.Errorf("restore: heal: stripe %d: data column %d: %w", stripe, c, err)
		}
		dataBlocks[c] = b
	}
	parityBlocks := make([][]byte, fec.M)
	for j := range fec.M {
		buf := make([]byte, fec.BlockSize)
		if _, err := parityFiles[j].ReadAt(buf, int64(1+stripe)*fec.BlockSize); err != nil {
			return nil, fmt.Errorf("restore: heal: stripe %d: parity column %d: %w", stripe, j, err)
		}
		parityBlocks[j] = buf
	}

	badData, err := fec.VerifyBlocks(&rec, dataBlocks)
	if err != nil {
		return nil, fmt.Errorf("restore: heal: stripe %d: %w", stripe, err)
	}
	if len(badData) > fec.M {
		return nil, fmt.Errorf("restore: heal: stripe %d: %d bad data blocks, more than the %d parity blocks can recover", stripe, len(badData), fec.M)
	}
	inBad := make(map[int]bool, len(badData))
	for _, c := range badData {
		inBad[c] = true
	}

	attempt := func(excludeParity int) (data, parity [][]byte, ok bool) {
		shards := make(map[int][]byte, fec.K+fec.M)
		for c := range fec.K {
			if !inBad[c] {
				shards[c] = dataBlocks[c]
			}
		}
		for j := range fec.M {
			if j == excludeParity {
				continue
			}
			shards[fec.K+j] = parityBlocks[j]
		}
		if len(shards) < fec.K {
			return nil, nil, false
		}
		d, p, err := codec.Decode(shards)
		if err != nil {
			return nil, nil, false
		}
		bad2, err := fec.VerifyBlocks(&rec, d)
		if err != nil || len(bad2) != 0 {
			return nil, nil, false
		}
		return d, p, true
	}

	recData, recParity, ok := attempt(-1)
	if !ok && len(badData)+1 <= fec.M {
		for j := 0; j < fec.M && !ok; j++ {
			recData, recParity, ok = attempt(j)
		}
	}
	if !ok {
		return nil, fmt.Errorf("restore: heal: stripe %d is not decodable", stripe)
	}

	report := &StripeReport{Stripe: stripe}
	for _, c := range badData {
		if err := writeStreamBlock(streamFiles, sizes, layout, uint64(c)*L+stripe, recData[c]); err != nil {
			return nil, fmt.Errorf("restore: heal: stripe %d: writing data column %d: %w", stripe, c, err)
		}
		report.DataColumns = append(report.DataColumns, c)
	}
	for j := range fec.M {
		if bytes.Equal(parityBlocks[j], recParity[j]) {
			continue
		}
		if _, err := parityFiles[j].WriteAt(recParity[j], int64(1+stripe)*fec.BlockSize); err != nil {
			return nil, fmt.Errorf("restore: heal: stripe %d: writing parity column %d: %w", stripe, j, err)
		}
		report.ParityColumns = append(report.ParityColumns, j)
	}

	if len(report.DataColumns) == 0 && len(report.ParityColumns) == 0 {
		return nil, nil
	}
	return report, nil
}

// readStreamBlock reads one 2048-byte FEC stream block from the file
// layout maps it to, zero-padding past that file's real length the same
// way the stream itself is zero-padded to a block boundary.
func readStreamBlock(files []*os.File, sizes []uint64, layout *fec.StreamLayout, block uint64) ([]byte, error) {
	buf := make([]byte, fec.BlockSize)
	if block >= layout.BlockCount() {
		// Past the last stream file entirely: the whole k-column stripe
		// grid can run past the real stream length, and every block out
		// there is virtual zero, the same as buildFEC treats it.
		return buf, nil
	}
	idx, off, err := layout.Locate(block)
	if err != nil {
		return nil, err
	}
	size := sizes[idx]
	if off >= size {
		return buf, nil
	}
	n := fec.BlockSize
	if off+uint64(n) > size {
		n = int(size - off)
	}
	if _, err := files[idx].ReadAt(buf[:n], int64(off)); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

// writeStreamBlock writes a repaired block back to the file layout maps
// it to. A block that falls entirely in a file's virtual zero padding is
// not written; there is nothing on disk to correct.
func writeStreamBlock(files []*os.File, sizes []uint64, layout *fec.StreamLayout, block uint64, data []byte) error {
	if block >= layout.BlockCount() {
		return nil
	}
	idx, off, err := layout.Locate(block)
	if err != nil {
		return err
	}
	size := sizes[idx]
	if off >= size {
		return nil
	}
	n := fec.BlockSize
	if off+uint64(n) > size {
		n = int(size - off)
	}
	_, err = files[idx].WriteAt(data[:n], int64(off))
	return err
}

// copyTree copies the disc tree found under src (as image.FindNoahsark
// resolves it) into dst/NOAHSARK, so Heal can repair a copy of a
// read-only mount. Regular files are copied with their own mode bits;
// directories are created 0o755 and symlinks are recreated verbatim.
func copyTree(src, dst string) error {
	base, err := image.FindNoahsark(src)
	if err != nil {
		return err
	}
	dstBase := filepath.Join(dst, "NOAHSARK")
	return filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstBase, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(path, target, info.Mode())
		}
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
