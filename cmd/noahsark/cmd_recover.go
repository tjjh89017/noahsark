package main

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

const recoverUsage = "recover --source=PATH --disc=DIR"

func init() {
	register(&command{
		name:    "recover",
		usage:   recoverUsage,
		summary: "Read one disc into the repository, and create the repository when it is absent.",
		flags:   recoverFlags,
	})
}

// recoverOptions holds the command options of recover.
type recoverOptions struct {
	source string
	disc   onceValue
}

func recoverFlags(fs *flag.FlagSet) runFunc {
	o := &recoverOptions{}
	fs.StringVar(&o.source, "source", "", "the source root; recover stores it as sources.root")
	fs.Var(&o.disc, "disc", "the mount point of the disc to read; one value")
	return o.run
}

// onceValue is a string option that takes one value. A second value is
// a usage error.
type onceValue struct {
	value string
	set   bool
}

func (v *onceValue) String() string { return v.value }

func (v *onceValue) Set(s string) error {
	if v.set {
		return errors.New("takes one value; give it one time")
	}
	v.value, v.set = s, true
	return nil
}

// run implements "noahsark recover". docs/states.md, rows 67 to 70d,
// gives the lines.
func (o *recoverOptions) run(e *env, args []string) int {
	stderr := e.stderr
	const cmd = "recover"
	if len(args) != 0 || o.source == "" || o.disc.value == "" {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark "+recoverUsage)
		return 2
	}
	source, err := e.abs(o.source)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	repoDir, err := e.recoverRepoDir()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 2
	}
	stagingDir := filepath.Join(repoDir, defaultStagingDir)
	if isRepoDir(repoDir) {
		cfg, err := readConfig(configPath(repoDir))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
			return configExitCode(err)
		}
		stagingDir = cfg.StagingDir
	}

	root := o.disc.value
	verdict, err := countedMount(e, root, repoDir, stagingDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	if verdict != mountCounted {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s is not counted: %s; recover reads only a read-only mount point outside the repository\n", cmd, root, verdict)
		return 1
	}
	rr, err := image.ReadWithOptions(root, image.ReadOptions{Progress: e.progress(), KeepGoing: true})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s: cannot read the disc: %v\n", cmd, root, err)
		return 1
	}
	if refusal := foreignDiscRefusal(repoDir, rr.Disc); refusal != "" {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, refusal)
		return 1
	}

	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	lk, code, ok := lockRepo(cmd, repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)
	return recoverLocked(e, repoDir, source, root, rr)
}

// configExitCode is the exit code of a config file that cannot be read:
// 2 for a fault in the file, else 1.
func configExitCode(err error) int {
	if isConfigError(err) {
		return 2
	}
	return 1
}

// foreignDiscRefusal returns the refusal for a disc of another
// repository, or an empty string when repoDir is not a repository yet
// or the repository uuids match.
func foreignDiscRefusal(repoDir string, disc format.Disc) string {
	if !isRepoDir(repoDir) {
		return ""
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		return err.Error()
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return err.Error()
	}
	if repoUUID == disc.RepoUUID {
		return ""
	}
	return fmt.Sprintf("disc %s belongs to repository %s, not to this repository", uuidText(disc.DiscUUID), uuidText(disc.RepoUUID))
}

// recoverLocked does the work of recover under the repository lock: it
// makes or updates the repository, writes the catalog, and, for a disc
// that the repository does not know or that is missing, the ledgers, the
// refs, the item records and the disc events.
func recoverLocked(e *env, repoDir, source, root string, rr *image.ReadResult) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "recover"
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}

	cfg, err := ensureRecoverRepo(repoDir, rr.Disc.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return configExitCode(err)
	}
	layout := layoutOf(repoDir, cfg)
	if err := makeRepoLayout(layout); err != nil {
		return fail(err)
	}
	logs, err := openLogs(cmd, layout, true, stderr)
	if err != nil {
		return fail(err)
	}
	discUUID := rr.Disc.DiscUUID
	disc, known := logs.Discs.Disc(discUUID)
	if known && disc.State == stage.DiscUndone {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: disc %s was undone by pack --undo; it is not in this repository\n", cmd, uuidText(discUUID))
		return 1
	}
	if cfg.SourceRoot != source {
		if err := storeSourceRoot(repoDir, source); err != nil {
			return fail(err)
		}
	}

	if err := catalogFromDisc(repoDir, root, rr); err != nil {
		return fail(err)
	}

	isNew := !known || disc.State == stage.DiscMissing
	var rows []format.DiscsRow
	if isNew {
		rows, err = recordRecoveredDisc(e, layout, logs, rr)
		if err != nil {
			return fail(err)
		}
	}

	name := discNameShort(rr.Disc.DiscSeq, discOwnLabel(rr.Disc))
	if len(rr.Damaged) > 0 {
		damagedItems := 0
		for _, d := range rr.Damaged {
			if d.ID == "" {
				_, _ = fmt.Fprintf(stderr, "noahsark: %s: damaged file: %s\n", cmd, d.Error())
				continue
			}
			damagedItems++
			_, _ = fmt.Fprintf(stdout, "recover: damaged: %s\n", d.ID)
			_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, d.Error())
		}
		_, _ = fmt.Fprintf(stdout, "recover: %d item(s) damaged on %s\n", damagedItems, name)
		_, _ = fmt.Fprintln(stdout, nextStatusLine)
		return 1
	}
	if !isNew {
		_, _ = fmt.Fprintf(stdout, "recover: ok; %s already known\n", name)
		_, _ = fmt.Fprintln(stdout, nextStatusLine)
		return 0
	}
	missing := logs.Discs.InState(stage.DiscMissing)
	if len(missing) == 0 {
		_, _ = fmt.Fprintln(stdout, "recover: ok")
		_, _ = fmt.Fprintln(stdout, nextStatusLine)
		return 0
	}
	for _, row := range missingRows(missing, rows) {
		_, _ = fmt.Fprintf(stdout, "recover: %s named by another disc, not yet given\n", discName(row.DiscSeq, discsRowLabel(row), row.DiscUUID))
	}
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	return 1
}

// discOwnLabel is the label of a DISC.bin.
func discOwnLabel(d format.Disc) string {
	return string(d.Label[:min(int(d.LabelLen), len(d.Label))])
}

// discsRowLabel is the label of a DISCS row.
func discsRowLabel(row format.DiscsRow) string {
	return string(row.Label[:min(int(row.LabelLen), len(row.Label))])
}

// missingRows returns the ledger row of each missing disc, sorted by the
// disc number and then by the uuid. A missing disc with no ledger row
// gets a row with its uuid only.
func missingRows(missing []stage.DiscInfo, ledger []format.DiscsRow) []format.DiscsRow {
	out := make([]format.DiscsRow, 0, len(missing))
	for _, d := range missing {
		row := format.DiscsRow{DiscUUID: d.UUID}
		if i := slices.IndexFunc(ledger, func(r format.DiscsRow) bool { return r.DiscUUID == d.UUID }); i >= 0 {
			row = ledger[i]
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b format.DiscsRow) int {
		return cmp.Or(cmp.Compare(a.DiscSeq, b.DiscSeq), bytes.Compare(a.DiscUUID[:], b.DiscUUID[:]))
	})
	return out
}

// recordRecoveredDisc writes the records of a disc that the repository
// does not know, or that is missing. The ledgers and the refs come
// first. They merge, thus a crash before the events leaves a disc that
// the next recover reads again. Then it records each intact item that
// the item log does not know, or that is Lost, as OnDisc, and appends
// Recovered, CheckFailed for a damaged disc, and NamedMissing for each
// disc that the DISCS table names and that the disc state log does not
// know. It returns the rows of the disc ledger.
func recordRecoveredDisc(e *env, layout repoLayout, logs *stage.Logs, rr *image.ReadResult) ([]format.DiscsRow, error) {
	repoUUID := rr.Disc.RepoUUID
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		return nil, err
	}
	rows := mergeDiscsRows(ledger.Rows, rr.Discs.Rows)
	if err := image.SaveDiscsLedger(layout.discsLedgerFile(), repoUUID, rows); err != nil {
		return nil, err
	}
	if err := recoverRefs(layout, repoUUID, rr.Refs.Records); err != nil {
		return nil, err
	}

	discUUID := rr.Disc.DiscUUID
	ids := make([]object.ID, 0, len(rr.Index.Objects))
	for _, row := range rr.Index.Objects {
		if id := object.ID(row.ContentID); rr.ObjectIntact(id) {
			ids = append(ids, id)
		}
	}
	if err := logs.Items.EnsureOnDisc(rr.Run.RunSeq, discUUID, ids...); err != nil {
		return nil, err
	}

	now := e.now()
	recovered := discEvent(now, discUUID, stage.EventRecovered)
	if rr.Run.FECScheme != format.FECSchemeNone {
		recovered.Flags |= stage.FlagFEC
	}
	events := []stage.DiscRecord{recovered}
	if len(rr.Damaged) > 0 {
		events = append(events, discEvent(now, discUUID, stage.EventCheckFailed))
	}
	named := map[[16]byte]bool{discUUID: true}
	for _, row := range rr.Discs.Rows {
		if named[row.DiscUUID] {
			continue
		}
		named[row.DiscUUID] = true
		if _, ok := logs.Discs.Disc(row.DiscUUID); !ok {
			events = append(events, discEvent(now, row.DiscUUID, stage.EventNamedMissing))
		}
	}
	if err := logs.Discs.Append(events...); err != nil {
		return nil, err
	}
	return rows, nil
}

// recoverRefs writes the ref records of a disc into the ref ledger and
// refs.txt. A name that the disc carries takes the value of the newest
// record of the ledger. A name of refs.txt that no record carries stays.
func recoverRefs(layout repoLayout, repoUUID [16]byte, records []format.RefRecord) error {
	ledger, err := image.LoadRefsLedger(layout.refsLedgerFile(), repoUUID)
	if err != nil {
		return err
	}
	all := mergeRefRecords(ledger.Records, records)
	if err := image.SaveRefsLedger(layout.refsLedgerFile(), repoUUID, all); err != nil {
		return err
	}
	refs, err := readRefs(layout.refsFile())
	if err != nil {
		return err
	}
	if refs == nil {
		refs = make(map[string]string)
	}
	maps.Copy(refs, newestRefs(all))
	return writeRefs(layout.refsFile(), refs)
}

// ensureRecoverRepo reads the config of repoDir when it is a
// repository, or writes a new config with repoUUID. An existing config
// must have repoUUID.
func ensureRecoverRepo(repoDir string, repoUUID [16]byte) (repoConfig, error) {
	if !isRepoDir(repoDir) {
		if err := writeConfig(configPath(repoDir), newConfigFile(repoUUID, "")); err != nil {
			return repoConfig{}, err
		}
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		return repoConfig{}, err
	}
	existing, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		return repoConfig{}, err
	}
	if existing != repoUUID {
		return repoConfig{}, fmt.Errorf("the repository uuid changed to %s during recover", uuidText(existing))
	}
	return cfg, nil
}

// storeSourceRoot sets sources.root of the config of repoDir to source.
// It keeps every other key.
func storeSourceRoot(repoDir, source string) error {
	path := configPath(repoDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := decodeConfig(data)
	if err != nil {
		return &configError{path: path, err: err}
	}
	f.Sources.Root = source
	return writeConfig(path, f)
}

// catalogFromDisc writes into the catalog each snapshot, tree and blob
// object of the disc that passed its check, and the INDEX, REFS and
// DISCS tables of the disc when REFS and DISCS passed their check. Then
// it computes again the completeness of each snapshot that the disc
// names or that the catalog holds.
func catalogFromDisc(repoDir, root string, rr *image.ReadResult) error {
	c, err := catalog.Open(repoDir)
	if err != nil {
		return err
	}
	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		return err
	}

	snapshots := map[object.ID]bool{}
	for _, row := range rr.Index.Objects {
		id := object.ID(row.ContentID)
		if row.Kind == format.ObjectKindSnapshot {
			snapshots[id] = true
		}
		if !rr.ObjectIntact(id) {
			continue
		}
		var dir string
		switch row.Kind {
		case format.ObjectKindSnapshot:
			dir = names.Join(base, "snapshots")
		case format.ObjectKindTree, format.ObjectKindBlob:
			dir = names.Join(names.Join(base, "objects"), id.FanoutByte())
		default:
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, names.Resolve(dir, id.TextForm())))
		if err != nil {
			return fmt.Errorf("object %s: %w", id.TextForm(), err)
		}
		if err := c.WriteObject(row.Kind, id, raw); err != nil {
			return err
		}
	}

	if rr.RefsIntact && rr.DiscsIntact {
		if err := writeDiscTables(c, base, names, rr.Disc.DiscUUID); err != nil {
			return err
		}
	}

	for _, rec := range rr.Refs.Records {
		snapshots[object.ID(rec.SnapshotID)] = true
	}
	held, err := c.ListSnapshots()
	if err != nil {
		return err
	}
	for _, id := range held {
		snapshots[id] = true
	}
	for id := range snapshots {
		if err := c.RefreshComplete(id); err != nil {
			return err
		}
	}
	return nil
}

// writeDiscTables copies the INDEX, REFS and DISCS files of the newest
// run under base into the catalog.
func writeDiscTables(c *catalog.Catalog, base string, names *image.NameCache, discUUID [16]byte) error {
	runDir, err := image.NewestRunDir(names.Join(base, "runs"))
	if err != nil {
		return err
	}
	catalogDir := names.Join(runDir, "catalog")
	var bufs [3][]byte
	for i, p := range []string{
		filepath.Join(runDir, names.Resolve(runDir, "INDEX.bin")),
		filepath.Join(catalogDir, names.Resolve(catalogDir, "REFS.bin")),
		filepath.Join(catalogDir, names.Resolve(catalogDir, "DISCS.bin")),
	} {
		if bufs[i], err = os.ReadFile(p); err != nil {
			return err
		}
	}
	return c.WriteDisc(discUUID, bufs[0], bufs[1], bufs[2])
}

// mergeDiscsRows unions existing, the rows of the disc ledger, with
// rows, the DISCS rows of a disc. A row of existing wins for its uuid.
// The result is sorted by creation time, and by the uuid on a tie.
func mergeDiscsRows(existing, rows []format.DiscsRow) []format.DiscsRow {
	byUUID := make(map[[16]byte]format.DiscsRow, len(existing)+len(rows))
	for _, row := range rows {
		byUUID[row.DiscUUID] = row
	}
	for _, row := range existing {
		byUUID[row.DiscUUID] = row
	}
	return slices.SortedFunc(maps.Values(byUUID), func(a, b format.DiscsRow) int {
		return cmp.Or(cmp.Compare(a.CreatedSec, b.CreatedSec), bytes.Compare(a.DiscUUID[:], b.DiscUUID[:]))
	})
}

// mergeRefRecords returns the records of existing, then each record of
// records that existing does not hold.
func mergeRefRecords(existing, records []format.RefRecord) []format.RefRecord {
	out := slices.Clone(existing)
	for _, rec := range records {
		if !slices.Contains(out, rec) {
			out = append(out, rec)
		}
	}
	return out
}

// newestRefs maps each ref name of records to the text id of the
// snapshot of its newest record.
func newestRefs(records []format.RefRecord) map[string]string {
	best := make(map[string]format.RefRecord, len(records))
	for _, rec := range records {
		catalog.MergeRef(best, rec)
	}
	out := make(map[string]string, len(best))
	for name, rec := range best {
		out[name] = object.ID(rec.SnapshotID).TextForm()
	}
	return out
}
