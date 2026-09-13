package image

import (
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
)

// RunFileLen is the on-disc length of RUN.bin and RUN2.bin: the 512-byte
// header padded to one whole sector.
const RunFileLen = SectorSize

func buildDisc(opts BuildOptions, packTime time.Time) ([]byte, [32]byte, error) {
	_, tzOffset := packTime.Zone()
	var label [64]byte
	n := copy(label[:], opts.Label)

	forced := uint8(0)
	if opts.TargetCapacitySectors < opts.PhysicalCapacitySectors {
		forced = 1
	}

	d := format.Disc{
		Common: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicDisc,
			VersionMajor: 1, VersionMinor: 0, HeaderLen: format.CommonHeaderLen + 2012,
		},
		DiscUUID: opts.DiscUUID, RepoUUID: opts.RepoUUID, DiscSeq: buildDiscSeq,
		CapacitySectors: opts.PhysicalCapacitySectors, CapacityForcedSectors: opts.TargetCapacitySectors,
		CreatedSec: packTime.Unix(), CreatedNsec: uint32(packTime.Nanosecond()), TzOffsetSec: int32(tzOffset),
		MediaType: opts.MediaType, FSProfile: format.DiscFSProfileOneshot, FanoutLevels: 1,
		CapacityIsForced: forced, Sealed: 0, LabelLen: uint32(n), Label: label, ToolVersion: toolVersion,
	}
	buf := make([]byte, format.DiscLen)
	if err := d.Encode(buf); err != nil {
		return nil, [32]byte{}, err
	}
	return buf, sha256sum(buf), nil
}

func buildRun(opts BuildOptions, packTime time.Time, indexBuf []byte, indexHash [32]byte, streamBytes uint64, objectCount uint64) ([]byte, error) {
	r := format.Run{
		Common: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicRun,
			VersionMajor: 1, VersionMinor: 0, HeaderLen: format.CommonHeaderLen + 480,
		},
		DiscUUID: opts.DiscUUID, RepoUUID: opts.RepoUUID, RunSeq: buildRunSeq, DiscSeq: buildDiscSeq,
		FECK: uint16(231), FECM: uint16(23), FECScheme: format.FECSchemeRS255GF8,
		HashAlgo: format.HashAlgoSHA256, ChunkerProfile: format.ChunkerProfileP4,
		Compression: format.CompressionZstd, FSProfile: format.DiscFSProfileOneshot,
		RunKind: format.RunKindData, RunFlags: 0,
		IndexBytes: uint64(len(indexBuf)), IndexHash: indexHash,
		StreamBytes: streamBytes,
		CreatedSec:  packTime.Unix(), CreatedNsec: uint32(packTime.Nanosecond()), ToolVersion: toolVersion,
		DiscObjectCount: objectCount, DiscRunIndex: 0,
	}
	buf := make([]byte, format.RunLen)
	if err := r.Encode(buf); err != nil {
		return nil, err
	}
	out := make([]byte, RunFileLen)
	copy(out, buf)
	return out, nil
}

func buildRefs(opts BuildOptions) ([]byte, [32]byte, error) {
	recs := make([]format.RefRecord, len(opts.Snapshots))
	for i, s := range opts.Snapshots {
		var name [format.RefNameLen]byte
		n := copy(name[:], s.Name)
		recs[i] = format.RefRecord{
			SnapshotID: s.ID, TimeSec: s.Time.Unix(), TimeNsec: uint32(s.Time.Nanosecond()),
			NameLen: uint16(n), HashAlgo: format.HashAlgoSHA256, Name: name, RunSeq: buildRunSeq,
		}
	}
	sortRefRecords(recs)
	t := format.RefsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs,
			VersionMajor: 1, VersionMinor: 0, HeaderLen: format.RefsHeaderLen,
		},
		RepoUUID: opts.RepoUUID, RecordCount: uint64(len(recs)), RecordSize: format.RefRecordLen,
		HashAlgo: format.HashAlgoSHA256, DigestLen: 32, Records: recs,
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return nil, [32]byte{}, err
	}
	return buf, sha256sum(buf), nil
}

// sortRefRecords orders records the way REFS requires: name bytes
// ascending, then time_sec, then time_nsec, then run_seq, then
// snapshot_id bytes ascending.
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
		if a.RunSeq != b.RunSeq {
			return a.RunSeq < b.RunSeq
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
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
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

func buildDiscs(opts BuildOptions, packTime time.Time) ([]byte, [32]byte, error) {
	var label [format.DiscsLabelLen]byte
	n := copy(label[:], opts.Label)

	var stateFlags uint8
	if opts.TargetCapacitySectors < opts.PhysicalCapacitySectors {
		stateFlags |= format.DiscsStateCapacityForced
	}

	row := format.DiscsRow{
		RunSeq: buildRunSeq, DiscSeq: buildDiscSeq, DiscUUID: opts.DiscUUID,
		// RunHash is zero: this table is carried by the run it describes,
		// section 11.3.
		CreatedSec: packTime.Unix(), LastVerifySec: 0,
		CapacitySectors: opts.PhysicalCapacitySectors, UsedSectors: 0,
		RunStatus: 2, Health: 6, RsMarginPercent: 100,
		LabelLen: uint16(n), Label: label, StateFlags: stateFlags,
		CapacityForcedSectors: opts.TargetCapacitySectors,
	}
	t := format.DiscsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs,
			VersionMajor: 1, VersionMinor: 0, HeaderLen: format.DiscsHeaderLen,
		},
		RepoUUID: opts.RepoUUID, RecordCount: 1, RecordSize: format.DiscsRowLen,
		HashAlgo: format.HashAlgoSHA256, DigestLen: 32, Rows: []format.DiscsRow{row},
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return nil, [32]byte{}, err
	}
	return buf, sha256sum(buf), nil
}
