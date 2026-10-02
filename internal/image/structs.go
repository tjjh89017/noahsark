package image

import (
	"crypto/sha256"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
)

// RunFileLen is the on-disc length of RUN.bin and RUN2.bin. Each file is
// exactly the run header.
const RunFileLen = format.RunLen

func sha256sum(b []byte) [32]byte { return sha256.Sum256(b) }

func buildDisc(opts BuildOptions, packTime time.Time, discSeq uint64) ([]byte, [32]byte, error) {
	_, tzOffset := packTime.Zone()
	var label [64]byte
	n := copy(label[:], opts.Label)

	d := format.Disc{
		Common: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicDisc,
			VersionMajor: 1, HeaderLen: format.DiscLen,
		},
		DiscUUID: opts.DiscUUID, RepoUUID: opts.RepoUUID, DiscSeq: discSeq,
		CapacitySectors: opts.TargetCapacitySectors,
		CreatedSec:      packTime.Unix(), CreatedNsec: uint32(packTime.Nanosecond()), TzOffsetSec: int32(tzOffset),
		LabelLen: uint32(n), Label: label, ToolVersion: toolVersion,
	}
	buf := make([]byte, format.DiscLen)
	if err := d.Encode(buf); err != nil {
		return nil, [32]byte{}, err
	}
	return buf, sha256sum(buf), nil
}

// buildRun encodes the run header. fec_scheme, fec_k, fec_m and every
// reserved field stay zero.
func buildRun(opts BuildOptions, packTime time.Time, indexBuf []byte, indexHash [32]byte, runSeq, discSeq uint64) ([]byte, error) {
	r := format.Run{
		Common: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicRun,
			VersionMajor: 1, HeaderLen: format.RunLen,
		},
		DiscUUID: opts.DiscUUID, RepoUUID: opts.RepoUUID, RunSeq: runSeq, DiscSeq: discSeq,
		FECScheme:  format.FECSchemeNone,
		HashAlgo:   format.HashAlgoSHA256,
		IndexBytes: uint64(len(indexBuf)), IndexHash: indexHash,
		CreatedSec: packTime.Unix(), CreatedNsec: uint32(packTime.Nanosecond()), ToolVersion: toolVersion,
	}
	buf := make([]byte, format.RunLen)
	if err := r.Encode(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// refRecordsFromSnapshots turns each named snapshot into one REFS
// record. A record carries no run number.
func refRecordsFromSnapshots(snapshots []SnapshotRef) []format.RefRecord {
	recs := make([]format.RefRecord, len(snapshots))
	for i, s := range snapshots {
		var name [format.RefNameLen]byte
		n := copy(name[:], s.Name)
		recs[i] = format.RefRecord{
			SnapshotID: s.ID, TimeSec: s.Time.Unix(), TimeNsec: uint32(s.Time.Nanosecond()),
			NameLen: uint16(n), Name: name,
		}
	}
	return recs
}

// encodeRefsTable sorts recs and encodes REFS from them. Every caller
// that writes a run's refs.bin goes through this, whether the records
// come straight from this run's own snapshots or from a merge with
// refs carried forward from earlier runs.
func encodeRefsTable(repoUUID [16]byte, recs []format.RefRecord) ([]byte, [32]byte, error) {
	sortRefRecords(recs)
	t := format.RefsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs,
			VersionMajor: 1, HeaderLen: format.RefsHeaderLen,
		},
		RepoUUID: repoUUID, RecordCount: uint64(len(recs)), Records: recs,
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return nil, [32]byte{}, err
	}
	return buf, sha256sum(buf), nil
}

// buildRefs builds REFS from opts.Snapshots alone. Build uses this: a
// one-shot build has no earlier run to carry refs forward from.
func buildRefs(opts BuildOptions) ([]byte, [32]byte, error) {
	recs := refRecordsFromSnapshots(opts.Snapshots)
	return encodeRefsTable(opts.RepoUUID, recs)
}

// sortRefRecords orders records the way REFS requires: name bytes
// ascending, then time_sec, then time_nsec, then snapshot_id bytes
// ascending.
func sortRefRecords(recs []format.RefRecord) {
	less := func(i, j int) bool {
		a, b := recs[i], recs[j]
		if c := compareBytes(a.Name[:a.NameLen], b.Name[:b.NameLen]); c != 0 {
			return c < 0
		}
		if a.TimeSec != b.TimeSec {
			return a.TimeSec < b.TimeSec
		}
		if a.TimeNsec != b.TimeNsec {
			return a.TimeNsec < b.TimeNsec
		}
		return compareBytes(a.SnapshotID[:], b.SnapshotID[:]) < 0
	}
	insertionSortRefs(recs, less)
}

func insertionSortRefs(recs []format.RefRecord, less func(i, j int) bool) {
	for i := 1; i < len(recs); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			recs[j], recs[j-1] = recs[j-1], recs[j]
		}
	}
}

func compareBytes(a, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// newDiscsRow builds this run's own DISCS row: its RunHash is zero,
// since DISCS is carried by the run it describes and that run's own
// header hash is not yet known when the table is built. The next disc's
// copy of DISCS fills it in.
func newDiscsRow(opts BuildOptions, packTime time.Time, runSeq, discSeq uint64) format.DiscsRow {
	var label [format.DiscsLabelLen]byte
	n := copy(label[:], opts.Label)

	return format.DiscsRow{
		RunSeq: runSeq, DiscSeq: discSeq, DiscUUID: opts.DiscUUID,
		CreatedSec:      packTime.Unix(),
		CapacitySectors: opts.TargetCapacitySectors,
		LabelLen:        uint16(n), Label: label,
	}
}

// buildDiscs encodes DISCS: every prior disc's row, exactly as the
// repository's disc ledger carries them, followed by this run's own row.
func buildDiscs(opts BuildOptions, packTime time.Time, runSeq, discSeq uint64, priorRows []format.DiscsRow) ([]byte, [32]byte, error) {
	rows := make([]format.DiscsRow, 0, len(priorRows)+1)
	rows = append(rows, priorRows...)
	rows = append(rows, newDiscsRow(opts, packTime, runSeq, discSeq))

	t := format.DiscsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs,
			VersionMajor: 1, HeaderLen: format.DiscsHeaderLen,
		},
		RepoUUID: opts.RepoUUID, RecordCount: uint64(len(rows)), Rows: rows,
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return nil, [32]byte{}, err
	}
	return buf, sha256sum(buf), nil
}
