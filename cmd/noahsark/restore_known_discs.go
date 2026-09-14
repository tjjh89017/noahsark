package main

import (
	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// knownDiscsForRepo names every disc repoFlag's repository has ever
// recorded, uuid to label, so a missing-disc hint can list a candidate
// disc packed after every provided disc, not only one an earlier disc's
// own DISCS table happens to mention. It tries the local disc ledger
// first, since that is the authoritative, always up to date copy, and
// falls back to the cache when the repository carries no ledger yet. A
// repository that cannot be found or read at all yields no candidates,
// never an error: the hint is best-effort.
func knownDiscsForRepo(repoFlag string) map[[16]byte]string {
	repoDir, err := discoverRepo(repoFlag)
	if err != nil {
		return nil
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		return nil
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return nil
	}

	if ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID); err == nil && len(ledger.Rows) > 0 {
		return discLabelsByUUID(ledger.Rows)
	}

	dir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		return nil
	}
	c, err := cache.Open(dir)
	if err != nil {
		return nil
	}
	discs, err := c.Discs()
	if err != nil || discs == nil {
		return nil
	}
	return discLabelsByUUID(discs.Rows)
}

// discLabelsByUUID reduces DISCS ledger rows to one label per disc
// uuid.
func discLabelsByUUID(rows []format.DiscsRow) map[[16]byte]string {
	labels := make(map[[16]byte]string, len(rows))
	for _, row := range rows {
		labels[row.DiscUUID] = labelText(row.Label[:row.LabelLen])
	}
	return labels
}
