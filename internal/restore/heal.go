package restore

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
	"strings"
	"syscall"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
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
	// Checksum is true when Heal wrote the checksum block of the stripe
	// again from the repaired data.
	Checksum bool
}

// HealResult is the repair that Heal made.
type HealResult struct {
	// Stripes holds one report for each stripe that Heal repaired, in
	// stripe order.
	Stripes []StripeReport
	// Files lists each file that Heal wrote, relative to the NOAHSARK
	// directory, in sorted order.
	Files []string
}

// HealOptions changes how HealWithOptions works. The zero value gives
// the behaviour of Heal.
type HealOptions struct {
	// Progress reports the stripes checked. A nil Progress reports
	// nothing.
	Progress *progress.Reporter
	// ReadAt reads len(p) bytes of f at off, as f.ReadAt does. Heal reads
	// every byte of the disc tree through it. A nil ReadAt is f.ReadAt.
	ReadAt func(f *os.File, p []byte, off int64) (int, error)
}

// errUnreadable marks a block that Heal could not read. The block is an
// erasure.
var errUnreadable = errors.New("block cannot be read")

// Heal verifies every data block of the newest run's FEC stream, and
// repairs every stripe that needs it using Reed-Solomon parity.
//
// A block that cannot be read, or that a short read ends inside, is an
// erasure from the start. A data block that does not match its digest in
// the checksum column is an erasure too. When the checksum block of a
// stripe cannot be read or fails its header check, Heal checks each file
// that the stripe holds a block of against its content id, or its
// file_hash, and each block of a file that fails is an erasure.
//
// For each stripe, Heal decodes from the k lowest-indexed blocks that
// are not erasures. When the reconstructed data does not verify, it
// retries holding out one more parity block at a time, one at a time
// only. A parity block has no digest: Heal encodes the parity again from
// the verified data and rewrites each parity block that differs from it
// or that is an erasure. A stripe that still does not verify after every
// attempt is a hard error naming that stripe, and Heal writes nothing for
// it; every other stripe's repair already written stands.
//
// When outDir is empty, Heal repairs discRoot in place: discRoot must be
// a writable unpacked tree of ordinary files. When outDir is set, Heal
// first copies the whole disc tree from discRoot into outDir and repairs
// the copy there, leaving discRoot untouched — the path to use when
// discRoot is a read-only mount. The copy goes on after a read error: the
// bytes that it cannot read are zeros in the copy, and each block of them
// is an erasure.
func Heal(discRoot, outDir string) (*HealResult, error) {
	return HealWithOptions(discRoot, outDir, HealOptions{})
}

// HealWithOptions is Heal, changed by opts.
func HealWithOptions(discRoot, outDir string, opts HealOptions) (*HealResult, error) {
	readAt := opts.ReadAt
	if readAt == nil {
		readAt = func(f *os.File, p []byte, off int64) (int, error) { return f.ReadAt(p, off) }
	}
	res := &HealResult{}

	work := discRoot
	unreadable := map[string]blockSet{}
	if outDir != "" {
		var err error
		if unreadable, err = copyTree(discRoot, outDir, readAt); err != nil {
			return res, err
		}
		work = outDir
	}

	cache := image.NewNameCache()
	base, err := image.FindNoahsark(work, cache)
	if err != nil {
		return res, err
	}
	if outDir == "" {
		if err := image.CheckTree(work, base); err != nil {
			return res, err
		}
	}
	runDir, err := image.NewestRunDir(cache.Join(base, "runs"))
	if err != nil {
		return res, err
	}
	header, err := image.ReadRunHeader(runDir, cache)
	if err != nil {
		return res, err
	}
	run := header.Run
	if run.FECScheme != format.FECSchemeRS255GF8 {
		// Name the disc, not the run sequence number: that number
		// repeats across discs of different lineages.
		uuid, uerr := ReadDiscUUID(work)
		if uerr != nil {
			return res, fmt.Errorf("the disc at %s has no FEC", discRoot)
		}
		return res, fmt.Errorf("disc %s has no FEC", uuidText(uuid))
	}
	if run.FECK != fec.K || run.FECM != fec.M {
		return res, fmt.Errorf("the run has fec_k %d and fec_m %d; heal repairs only %d and %d", run.FECK, run.FECM, fec.K, fec.M)
	}

	h, err := openHealer(base, runDir, cache, run, outDir != "", unreadable, readAt)
	if err != nil {
		return res, err
	}
	defer h.close()

	if err := h.healStripes(opts.Progress, res); err != nil {
		return res.finish(h), err
	}
	if err := h.fixLengths(); err != nil {
		return res.finish(h), err
	}
	if err := h.fixRunHeaderCopies(runDir, cache, header); err != nil {
		return res.finish(h), err
	}
	if err := h.checkNothingLeft(unreadable); err != nil {
		return res.finish(h), err
	}
	return res.finish(h), nil
}

// finish sorts the stripe reports and fills Files from h.
func (r *HealResult) finish(h *healer) *HealResult {
	slices.SortFunc(r.Stripes, func(a, b StripeReport) int {
		switch {
		case a.Stripe < b.Stripe:
			return -1
		case a.Stripe > b.Stripe:
			return 1
		}
		return 0
	})
	r.Files = r.Files[:0]
	for rel := range h.written {
		r.Files = append(r.Files, rel)
	}
	slices.Sort(r.Files)
	return r
}

// healFile is one file of the work tree that Heal reads and repairs.
type healFile struct {
	f    *os.File
	rel  string
	size int64
	// idx is the index of a stream file, and -1 for every other file.
	idx int
	// bad holds the blocks that the copy could not read. Heal clears a
	// block when it writes the block again.
	bad blockSet
}

// healer holds the open files of one run of the work tree.
type healer struct {
	base   string
	readAt func(f *os.File, p []byte, off int64) (int, error)

	stream   []image.StreamFile
	files    []*healFile
	layout   *fec.StreamLayout
	L        uint64
	checksum *healFile
	parity   []*healFile
	codec    *fec.Codec

	indexBytes uint64
	indexHash  [32]byte

	// fileState keeps the result of fileGood for each stream file.
	fileState map[int]bool
	written   map[string]bool
}

func openHealer(base, runDir string, cache *image.NameCache, run format.Run, outMode bool, unreadable map[string]blockSet, readAt func(*os.File, []byte, int64) (int, error)) (*healer, error) {
	indexBuf, err := os.ReadFile(filepath.Join(runDir, cache.Resolve(runDir, "INDEX.bin")))
	if err != nil {
		return nil, fmt.Errorf("INDEX.bin: %w", err)
	}
	if uint64(len(indexBuf)) != run.IndexBytes || sha256.Sum256(indexBuf) != run.IndexHash {
		return nil, errors.New("INDEX.bin does not match index_hash of the run header; heal needs INDEX.bin to find the stream")
	}
	stream, _, err := image.StreamFileList(base, runDir, cache)
	if err != nil {
		return nil, err
	}
	sizes := make([]uint64, len(stream))
	for i, sf := range stream {
		sizes[i] = sf.Row.ByteLen
	}
	layout, err := fec.NewStreamLayout(sizes, fec.K)
	if err != nil {
		return nil, err
	}
	if layout.BlockCount()*fec.BlockSize != run.StreamBytes {
		return nil, fmt.Errorf("INDEX gives a stream of %d bytes, the run header gives stream_bytes %d", layout.BlockCount()*fec.BlockSize, run.StreamBytes)
	}
	codec, err := fec.NewCodec(fec.K, fec.M)
	if err != nil {
		return nil, err
	}

	h := &healer{
		base: base, readAt: readAt, stream: stream, layout: layout,
		L: layout.StripeCount(), codec: codec,
		indexBytes: run.IndexBytes, indexHash: run.IndexHash,
		fileState: map[int]bool{}, written: map[string]bool{},
	}
	open := func(path string, size int64, idx int) (*healFile, error) {
		hf, err := openWorkFile(base, path, size, outMode, unreadable)
		if err != nil {
			h.close()
			return nil, err
		}
		hf.idx = idx
		return hf, nil
	}
	for i, sf := range stream {
		hf, err := open(sf.Path, int64(sf.Row.ByteLen), i)
		if err != nil {
			return nil, err
		}
		h.files = append(h.files, hf)
	}
	columnBytes := int64(h.L) * fec.BlockSize
	if h.checksum, err = open(filepath.Join(runDir, cache.Resolve(runDir, "checksum.bin")), columnBytes, -1); err != nil {
		return nil, err
	}
	parityDir := cache.Join(runDir, "parity")
	for j := range fec.M {
		name := fmt.Sprintf("p%04d.bin", fec.K+1+j)
		hf, err := open(filepath.Join(parityDir, cache.Resolve(parityDir, name)), columnBytes, -1)
		if err != nil {
			return nil, err
		}
		h.parity = append(h.parity, hf)
	}
	return h, nil
}

// openWorkFile opens a file of the work tree for read and write. It never
// follows a symlink. In a copy, a file that the copy could not list is
// absent: openWorkFile makes it, size zero bytes long, with every block
// unreadable.
func openWorkFile(base, path string, size int64, outMode bool, unreadable map[string]blockSet) (*healFile, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return nil, err
	}
	hf := &healFile{rel: rel, size: size, bad: unreadable[rel]}
	hf.f, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) && outMode {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		hf.f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
		if err != nil {
			return nil, err
		}
		if err := hf.f.Truncate(size); err != nil {
			_ = hf.f.Close()
			return nil, err
		}
		hf.bad = fullBlockSet(size)
		unreadable[rel] = hf.bad
		return hf, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return hf, nil
}

func (h *healer) close() {
	for _, hf := range h.files {
		if hf != nil {
			_ = hf.f.Close()
		}
	}
	if h.checksum != nil {
		_ = h.checksum.f.Close()
	}
	for _, hf := range h.parity {
		_ = hf.f.Close()
	}
}

// read reads len(p) bytes of hf at off. A block that the copy could not
// read, a read error and a short read all give errUnreadable.
func (h *healer) read(hf *healFile, p []byte, off int64) error {
	for b := off / fec.BlockSize; b*fec.BlockSize < off+int64(len(p)); b++ {
		if hf.bad.has(b) {
			return errUnreadable
		}
	}
	n, err := h.readAt(hf.f, p, off)
	if n < len(p) || (err != nil && !errors.Is(err, io.EOF)) {
		return errUnreadable
	}
	return nil
}

// write writes p to hf at off, marks the blocks as good and records the
// file as written.
func (h *healer) write(hf *healFile, p []byte, off int64) error {
	if _, err := hf.f.WriteAt(p, off); err != nil {
		return fmt.Errorf("%s: %w", hf.rel, err)
	}
	for b := off / fec.BlockSize; b*fec.BlockSize < off+int64(len(p)); b++ {
		hf.bad.clear(b)
	}
	if hf.idx >= 0 {
		delete(h.fileState, hf.idx)
	}
	h.written[hf.rel] = true
	return nil
}

// streamBlock reads one 2048-byte FEC stream block, zero-padding past the
// real length of its file the same way the stream itself is zero-padded
// to a block boundary. inFile is false for a block that holds no byte of
// a file: such a block is always zero.
func (h *healer) streamBlock(block uint64) (buf []byte, inFile bool, err error) {
	buf = make([]byte, fec.BlockSize)
	idx, off, n, ok := h.locate(block)
	if !ok {
		return buf, false, nil
	}
	return buf, true, h.read(h.files[idx], buf[:n], int64(off))
}

// locate gives the file index, the byte offset in the file and the number
// of file bytes of a stream block. ok is false for a block past the end
// of the stream or inside the zero padding of a file.
func (h *healer) locate(block uint64) (idx int, off uint64, n int, ok bool) {
	if block >= h.layout.BlockCount() {
		return 0, 0, 0, false
	}
	idx, off, err := h.layout.Locate(block)
	if err != nil {
		return 0, 0, 0, false
	}
	size := h.stream[idx].Row.ByteLen
	if off >= size {
		return 0, 0, 0, false
	}
	n = fec.BlockSize
	if off+uint64(n) > size {
		n = int(size - off)
	}
	return idx, off, n, true
}

// checksumRecord reads the checksum block of stripe. ok is false when the
// block cannot be read or fails its checks.
func (h *healer) checksumRecord(stripe uint64) (rec *format.ChecksumRecord, ok bool) {
	buf := make([]byte, format.ChecksumRecordLen)
	if h.read(h.checksum, buf, int64(stripe)*format.ChecksumRecordLen) != nil {
		return nil, false
	}
	rec = &format.ChecksumRecord{}
	if rec.Decode(buf) != nil || uint64(rec.StripeIndex) != stripe || int(rec.DigestCount) != fec.K {
		return nil, false
	}
	return rec, true
}

// healStripes repairs every stripe. It first repairs the stripes whose
// checksum block is usable, and then the others: the content id check of
// a file needs the blocks of the file in the other stripes repaired
// first.
func (h *healer) healStripes(prog *progress.Reporter, res *HealResult) error {
	prog.Start("heal: stripes checked", int64(h.L))
	defer prog.Done()
	var noDigest []uint64
	for stripe := range h.L {
		rec, ok := h.checksumRecord(stripe)
		if !ok {
			noDigest = append(noDigest, stripe)
			continue
		}
		if err := h.healStripe(stripe, rec, res); err != nil {
			return err
		}
		prog.Add(1)
	}
	for _, stripe := range noDigest {
		if err := h.healStripe(stripe, nil, res); err != nil {
			return err
		}
		prog.Add(1)
	}
	return nil
}

// healStripe repairs one stripe. rec is the checksum block of the stripe,
// or nil when that block is not usable.
func (h *healer) healStripe(stripe uint64, rec *format.ChecksumRecord, res *HealResult) error {
	data := make([][]byte, fec.K)
	dataErased := make([]bool, fec.K)
	dataUnread := make([]bool, fec.K)
	inFile := make([]bool, fec.K)
	for c := range fec.K {
		b, holdsFile, err := h.streamBlock(uint64(c)*h.L + stripe)
		data[c], inFile[c] = b, holdsFile
		if err != nil {
			dataErased[c], dataUnread[c] = true, true
		}
	}
	parity := make([][]byte, fec.M)
	parityErased := make([]bool, fec.M)
	for j := range fec.M {
		parity[j] = make([]byte, fec.BlockSize)
		if h.read(h.parity[j], parity[j], int64(stripe)*fec.BlockSize) != nil {
			parityErased[j] = true
		}
	}

	// checkFiles lists the files that hold an erased block of the stripe,
	// for the content id check when rec is nil.
	var checkFiles []int
	if rec != nil {
		for c := range fec.K {
			if !dataErased[c] && fec.BlockDigest(data[c]) != rec.Digests[c] {
				dataErased[c] = true
			}
		}
	} else {
		for c := range fec.K {
			if !inFile[c] {
				continue
			}
			idx, _, _, _ := h.locate(uint64(c)*h.L + stripe)
			if h.fileGood(idx) {
				continue
			}
			dataErased[c] = true
			if !slices.Contains(checkFiles, idx) {
				checkFiles = append(checkFiles, idx)
			}
		}
	}

	erasures := 0
	for _, e := range slices.Concat(dataErased, parityErased) {
		if e {
			erasures++
		}
	}
	if erasures > fec.M {
		return fmt.Errorf("stripe %d has %d blocks that are damaged or cannot be read, more than the %d parity blocks can recover", stripe, erasures, fec.M)
	}

	verified := func(d [][]byte) bool {
		if rec != nil {
			bad, err := fec.VerifyBlocks(rec, d)
			return err == nil && len(bad) == 0
		}
		overrides := map[uint64][]byte{}
		for c := range fec.K {
			if dataErased[c] && inFile[c] {
				overrides[uint64(c)*h.L+stripe] = d[c]
			}
		}
		for _, idx := range checkFiles {
			if !h.fileOK(idx, overrides) {
				return false
			}
		}
		return true
	}
	attempt := func(excludeParity int) (d, p [][]byte, ok bool) {
		shards := make(map[int][]byte, fec.K+fec.M)
		for c := range fec.K {
			if !dataErased[c] {
				shards[c] = data[c]
			}
		}
		for j := range fec.M {
			if !parityErased[j] && j != excludeParity {
				shards[fec.K+j] = parity[j]
			}
		}
		if len(shards) < fec.K {
			return nil, nil, false
		}
		d, p, err := h.codec.Decode(shards)
		if err != nil || !verified(d) {
			return nil, nil, false
		}
		return d, p, true
	}

	recData, recParity, ok := attempt(-1)
	if !ok && erasures+1 <= fec.M {
		// Only a parity block that the decode used can have made it
		// wrong: the decode uses the lowest-indexed blocks that are
		// present.
		used := 0
		for c := range fec.K {
			if dataErased[c] {
				used++
			}
		}
		for j := 0; j < fec.M && used > 0 && !ok; j++ {
			if parityErased[j] {
				continue
			}
			used--
			recData, recParity, ok = attempt(j)
		}
	}
	if !ok {
		return fmt.Errorf("stripe %d is not decodable", stripe)
	}

	report := StripeReport{Stripe: stripe}
	for c := range fec.K {
		// A block of a file that failed its content id check can hold
		// the right bytes: the damage is in another block of the file.
		if !dataErased[c] || !inFile[c] || (!dataUnread[c] && bytes.Equal(data[c], recData[c])) {
			continue
		}
		idx, off, n, _ := h.locate(uint64(c)*h.L + stripe)
		if err := h.write(h.files[idx], recData[c][:n], int64(off)); err != nil {
			return fmt.Errorf("stripe %d: data column %d: %w", stripe, c, err)
		}
		report.DataColumns = append(report.DataColumns, c)
	}
	for j := range fec.M {
		if !parityErased[j] && bytes.Equal(parity[j], recParity[j]) {
			continue
		}
		if err := h.write(h.parity[j], recParity[j], int64(stripe)*fec.BlockSize); err != nil {
			return fmt.Errorf("stripe %d: parity column %d: %w", stripe, j, err)
		}
		report.ParityColumns = append(report.ParityColumns, j)
	}
	if rec == nil {
		buf := make([]byte, format.ChecksumRecordLen)
		if err := fec.BuildChecksumRecord(uint32(stripe), recData).Encode(buf); err != nil {
			return fmt.Errorf("stripe %d: checksum block: %w", stripe, err)
		}
		if err := h.write(h.checksum, buf, int64(stripe)*format.ChecksumRecordLen); err != nil {
			return fmt.Errorf("stripe %d: checksum block: %w", stripe, err)
		}
		report.Checksum = true
	}

	if len(report.DataColumns) > 0 || len(report.ParityColumns) > 0 || report.Checksum {
		res.Stripes = append(res.Stripes, report)
	}
	return nil
}

// fileGood is fileOK with no overrides. It keeps the result until Heal
// writes the file.
func (h *healer) fileGood(idx int) bool {
	ok, seen := h.fileState[idx]
	if !seen {
		ok = h.fileOK(idx, nil)
		h.fileState[idx] = ok
	}
	return ok
}

// fileOK checks stream file idx against its content id, or against its
// file_hash, or, for INDEX.bin, against index_hash of the run header. It
// reads the file through its stream blocks. overrides gives the bytes of
// a stream block in place of the bytes in the file.
func (h *healer) fileOK(idx int, overrides map[uint64][]byte) bool {
	sf := h.stream[idx]
	r := &streamFileReader{h: h, idx: idx, first: h.layout.FileStart(idx), size: sf.Row.ByteLen, overrides: overrides}
	switch sf.Row.Role {
	case format.FileRoleObject:
		head := make([]byte, format.CommonHeaderLen+format.ObjectHeaderLen)
		if _, err := io.ReadFull(r, head); err != nil {
			return false
		}
		_, oh, err := format.DecodeObjectFileHeader(head)
		if err != nil || uint64(len(head))+oh.StoredLen != sf.Row.ByteLen {
			return false
		}
		id, err := object.HashStreamed(oh.Kind, r, oh.Compression, oh.StoredLen)
		return err == nil && id == sf.ObjectID
	case format.FileRoleIndex:
		sum := sha256.New()
		if _, err := io.Copy(sum, r); err != nil {
			return false
		}
		return sf.Row.ByteLen == h.indexBytes && [32]byte(sum.Sum(nil)) == h.indexHash
	default:
		sum := sha256.New()
		if _, err := io.Copy(sum, r); err != nil {
			return false
		}
		return [32]byte(sum.Sum(nil)) == sf.Row.FileHash
	}
}

// streamFileReader reads the bytes of one stream file block by block,
// with the overrides in place of the blocks they name.
type streamFileReader struct {
	h         *healer
	idx       int
	first     uint64
	size      uint64
	pos       uint64
	overrides map[uint64][]byte

	block    []byte
	blockNum uint64
	loaded   bool
}

func (r *streamFileReader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	num := r.first + r.pos/fec.BlockSize
	if !r.loaded || r.blockNum != num {
		if b, ok := r.overrides[num]; ok {
			r.block = b
		} else {
			b, _, err := r.h.streamBlock(num)
			if err != nil {
				return 0, err
			}
			r.block = b
		}
		r.blockNum, r.loaded = num, true
	}
	within := r.pos % fec.BlockSize
	end := min(uint64(fec.BlockSize), within+(r.size-r.pos))
	n := copy(p, r.block[within:end])
	r.pos += uint64(n)
	return n, nil
}

// fixLengths cuts each stream, checksum and parity file of the work tree
// to its length. A file of a copy can hold bytes after its end.
func (h *healer) fixLengths() error {
	all := slices.Concat(h.files, []*healFile{h.checksum}, h.parity)
	for _, hf := range all {
		fi, err := hf.f.Stat()
		if err != nil {
			return fmt.Errorf("%s: %w", hf.rel, err)
		}
		if fi.Size() == hf.size {
			continue
		}
		if err := hf.f.Truncate(hf.size); err != nil {
			return fmt.Errorf("%s: %w", hf.rel, err)
		}
		h.written[hf.rel] = true
	}
	return nil
}

// fixRunHeaderCopies writes a damaged run header copy again from the copy
// that passed its checks. The two copies are byte-identical.
func (h *healer) fixRunHeaderCopies(runDir string, cache *image.NameCache, header *image.RunHeader) error {
	other := "RUN2.bin"
	if header.FirstDamage != nil {
		other = "RUN.bin"
	}
	path := filepath.Join(runDir, cache.Resolve(runDir, other))
	if header.FirstDamage == nil {
		if got, err := os.ReadFile(path); err == nil && bytes.Equal(got, header.Raw) {
			return nil
		}
	}
	rel, err := filepath.Rel(h.base, path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if _, err := f.Write(header.Raw); err != nil {
		_ = f.Close()
		return fmt.Errorf("%s: %w", rel, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	h.written[rel] = true
	return nil
}

// checkNothingLeft refuses a copy that holds bytes that Heal could not
// read and did not write again: they are zeros in the copy.
func (h *healer) checkNothingLeft(unreadable map[string]blockSet) error {
	covered := map[string]bool{h.checksum.rel: true}
	for _, hf := range slices.Concat(h.files, h.parity) {
		covered[hf.rel] = true
	}
	var left []string
	for rel, bad := range unreadable {
		if covered[rel] || h.written[rel] || bad.count() == 0 {
			continue
		}
		left = append(left, rel)
	}
	if len(left) == 0 {
		return nil
	}
	slices.Sort(left)
	return fmt.Errorf("%s: bytes cannot be read, and no parity covers the file", strings.Join(left, ", "))
}

// copyTree copies the disc tree found under src (as image.FindNoahsark
// resolves it) into dst/NOAHSARK, so Heal can repair a copy of a
// read-only mount. It refuses an entry that is not a regular file or a
// directory. It goes on after a read error: the copy holds zeros in place
// of the bytes that it cannot read, and the result gives those blocks for
// each file, by its path relative to the NOAHSARK directory. A directory
// that cannot be listed is skipped: Heal makes each file of the run under
// it again from the parity.
func copyTree(src, dst string, readAt func(*os.File, []byte, int64) (int, error)) (map[string]blockSet, error) {
	base, err := image.FindNoahsark(src, image.NewNameCache())
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(src)
	if err != nil {
		return nil, err
	}
	baseRel, err := filepath.Rel(src, base)
	if err != nil {
		return nil, err
	}
	walkBase := filepath.Join(realRoot, baseRel)
	dstBase := filepath.Join(dst, "NOAHSARK")
	unreadable := map[string]blockSet{}
	err = filepath.WalkDir(walkBase, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d == nil {
				return err
			}
			return nil
		}
		rel, err := filepath.Rel(walkBase, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstBase, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				// Heal makes a file of the run that the copy lacks
				// again from the parity.
				return nil
			}
			bad, err := copyFile(path, target, info.Size(), readAt)
			if err != nil {
				return err
			}
			if bad.count() > 0 {
				unreadable[rel] = bad
			}
			return nil
		default:
			return fmt.Errorf("%s is not a regular file or a directory; a disc holds no other kind of entry", rel)
		}
	})
	return unreadable, err
}

// copyBufLen is the size of one read of copyFile. After a read error,
// copyFile reads the same range again one block at a time.
const copyBufLen = 1 << 20

// copyFile copies size bytes of src to the new file dst, and returns the
// blocks that it could not read. A file that cannot be opened gives every
// block.
func copyFile(src, dst string, size int64, readAt func(*os.File, []byte, int64) (int, error)) (blockSet, error) {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Close() }()
	if err := out.Truncate(size); err != nil {
		return nil, err
	}
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fullBlockSet(size), out.Close()
	}
	defer func() { _ = in.Close() }()

	bad := newBlockSet(size)
	buf := make([]byte, copyBufLen)
	for off := int64(0); off < size; off += copyBufLen {
		chunk := buf[:min(int64(copyBufLen), size-off)]
		if n, err := readAt(in, chunk, off); n == len(chunk) && (err == nil || errors.Is(err, io.EOF)) {
			if _, err := out.WriteAt(chunk, off); err != nil {
				return nil, err
			}
			continue
		}
		for boff := off; boff < off+int64(len(chunk)); boff += fec.BlockSize {
			block := buf[boff-off : min(boff-off+fec.BlockSize, int64(len(chunk)))]
			n, err := readAt(in, block, boff)
			if n < len(block) || (err != nil && !errors.Is(err, io.EOF)) {
				bad.set(boff / fec.BlockSize)
				continue
			}
			if _, err := out.WriteAt(block, boff); err != nil {
				return nil, err
			}
		}
	}
	return bad, out.Close()
}

// blockSet is a set of block numbers of one file, one bit per block.
type blockSet []uint64

func newBlockSet(size int64) blockSet {
	blocks := (size + fec.BlockSize - 1) / fec.BlockSize
	return make(blockSet, (blocks+63)/64)
}

// fullBlockSet gives the set of every block of a file of size bytes.
func fullBlockSet(size int64) blockSet {
	s := newBlockSet(size)
	for b := range (size + fec.BlockSize - 1) / fec.BlockSize {
		s.set(b)
	}
	return s
}

func (s blockSet) has(b int64) bool {
	i := b / 64
	return i < int64(len(s)) && s[i]&(1<<(b%64)) != 0
}

func (s blockSet) set(b int64) { s[b/64] |= 1 << (b % 64) }

func (s blockSet) clear(b int64) {
	if i := b / 64; i < int64(len(s)) {
		s[i] &^= 1 << (b % 64)
	}
}

func (s blockSet) count() int {
	n := 0
	for _, w := range s {
		for ; w != 0; w &= w - 1 {
			n++
		}
	}
	return n
}

// uuidText gives the text form of a disc uuid, in five groups.
func uuidText(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
