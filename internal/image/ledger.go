package image

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/durable"
	"github.com/tjjh89017/noahsark/internal/format"
)

// The disc ledger holds one DISCS row for each disc that pack wrote or
// that recover read. It uses the container of the on-disc DISCS table,
// with run_hash filled in as soon as it is known, so each later run
// copies the rows into its own DISCS table. See docs/decisions.md,
// "Burning and disc lifecycle".
//
// The ref ledger holds every ref record that a disc already carries. It
// uses the container of the on-disc REFS table. A pack loads it, merges
// the refs of its own call, and saves the merged set, so the REFS table
// of each run carries every ref that the repository knows.

// LoadDiscsLedger reads the disc ledger at path. A missing file gives an
// empty ledger: no disc is packed yet.
func LoadDiscsLedger(path string, repoUUID [16]byte) (format.DiscsTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return format.DiscsTable{RepoUUID: repoUUID}, nil
		}
		return format.DiscsTable{}, fmt.Errorf("disc ledger: %w", err)
	}
	var t format.DiscsTable
	if _, err := t.Decode(data); err != nil {
		return format.DiscsTable{}, fmt.Errorf("disc ledger %s: %w", path, err)
	}
	return t, nil
}

// SaveDiscsLedger writes the disc ledger at path with an atomic replace.
func SaveDiscsLedger(path string, repoUUID [16]byte, rows []format.DiscsRow) error {
	t := format.DiscsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs,
			VersionMajor: 1, HeaderLen: format.DiscsHeaderLen,
		},
		RepoUUID: repoUUID, RecordCount: uint64(len(rows)), Rows: rows,
	}
	buf := make([]byte, t.EncodedLen())
	if _, err := t.Encode(buf); err != nil {
		return err
	}
	return durable.WriteFile(path, buf, 0o644, durable.Replace)
}

// LoadRefsLedger reads the ref ledger at path. A missing file gives an
// empty ledger: no ref is packed yet.
func LoadRefsLedger(path string, repoUUID [16]byte) (format.RefsTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return format.RefsTable{RepoUUID: repoUUID}, nil
		}
		return format.RefsTable{}, fmt.Errorf("ref ledger: %w", err)
	}
	var t format.RefsTable
	if _, err := t.Decode(data); err != nil {
		return format.RefsTable{}, fmt.Errorf("ref ledger %s: %w", path, err)
	}
	return t, nil
}

// SaveRefsLedger writes the ref ledger at path with an atomic replace.
func SaveRefsLedger(path string, repoUUID [16]byte, recs []format.RefRecord) error {
	buf, _, err := encodeRefsTable(repoUUID, append([]format.RefRecord(nil), recs...))
	if err != nil {
		return err
	}
	return durable.WriteFile(path, buf, 0o644, durable.Replace)
}
