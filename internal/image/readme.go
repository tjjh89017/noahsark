package image

import (
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/fec"
)

// readmeTemplate is the fixed README.txt text, copied byte for byte from
// FORMAT.md, with its substitution slots still in place.
//
//go:embed readme_template.txt
var readmeTemplate string

// parityFilesValue, parityRepairFEC and parityRepairNone are the slot
// values FORMAT.md's README.txt section prints beside the text. A run
// with parity takes parityFilesValue and parityRepairFEC. A run with no
// parity drops the {parity_files} line and takes parityRepairNone.
const (
	parityFilesValue = `/NOAHSARK/runs/<seq>/checksum.bin   per-block digests of the data blocks
/NOAHSARK/runs/<seq>/parity/        one file per parity column
`

	parityRepairFEC = `The run carries Reed-Solomon parity over its own data files, concatenated in
the order INDEX.bin lists them, each padded to 2048 bytes: the FEC stream.
The stream is cut into {fec_k} equal columns of L blocks of 2048 bytes each;
FORMAT.txt says how to derive L from the run header. Stripe i is block i of
every column. checksum.bin is one more column: its block i holds an 8-byte
digest of each of the {fec_k} data blocks of stripe i, so a damaged block can
be found. The {fec_m} files under parity/ are the parity columns. Any
{fec_k} of the {fec_k} data plus {fec_m} parity blocks of one stripe
reconstruct the rest. The forward error correction part of FORMAT.txt gives
the field arithmetic, the matrix and a worked example. It is the full recipe
for a repair.
`

	parityRepairNone = `This disc carries no parity: the run directory holds no checksum.bin and no
parity/ directory. A damaged byte on this disc cannot be repaired from this
disc. Read the object from the second copy of this disc, or from another
disc of the repository that holds the same object.
`
)

// FormatTxt is the on-disc FORMAT.txt: a byte copy of FORMAT.md. A
// writer writes these bytes and no others.
//
//go:embed format.txt
var FormatTxt []byte

// uuidText formats a 16-byte uuid as hyphenated lowercase text.
func uuidText(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// labelText renders the meaningful label bytes for README.txt's {label}
// slot, with every byte outside 0x20 to 0x7E replaced by '?'.
func labelText(label []byte) string {
	b := make([]byte, len(label))
	for i, c := range label {
		if c < 0x20 || c > 0x7E {
			b[i] = '?'
		} else {
			b[i] = c
		}
	}
	return string(b)
}

// parityText returns the three parity-dependent slot values for a run
// that has parity, or has none. A run with no parity names no geometry,
// lists no checksum.bin and no parity/ directory, and tells the reader
// to take the object from another copy.
func parityText(fecEnabled bool) (identity, files, repair string) {
	geometry := strings.NewReplacer(
		"{fec_k}", fmt.Sprintf("%d", fec.K),
		"{fec_m}", fmt.Sprintf("%d", fec.M),
	)
	if !fecEnabled {
		return "parity: none", "", strings.TrimSuffix(parityRepairNone, "\n")
	}
	identity = geometry.Replace("parity geometry: k={fec_k} data columns, m={fec_m} parity columns")
	files = strings.TrimSuffix(parityFilesValue, "\n")
	repair = geometry.Replace(strings.TrimSuffix(parityRepairFEC, "\n"))
	return identity, files, repair
}

// buildReadme renders README.txt for one disc build: the fixed template
// with every slot substituted from the values Build writes into DISC.bin
// and the run header. hash_algo names the run header field; the
// superblock carries no such field of its own.
func buildReadme(opts BuildOptions, packTime time.Time, label []byte, discSeq uint64) []byte {
	_, tzOffset := packTime.Zone()
	sign := "+"
	if tzOffset < 0 {
		sign = "-"
		tzOffset = -tzOffset
	}
	created := fmt.Sprintf("%s%s%02d:%02d",
		packTime.Format("2006-01-02T15:04:05"), sign, tzOffset/3600, (tzOffset%3600)/60)

	identity, files, repair := parityText(opts.FECEnabled)
	filesLine := files + "\n"
	if files == "" {
		filesLine = ""
	}

	repl := strings.NewReplacer(
		"{repo_uuid}", uuidText(opts.RepoUUID),
		"{disc_uuid}", uuidText(opts.DiscUUID),
		"{disc_seq}", fmt.Sprintf("%d", discSeq),
		"{label}", labelText(label),
		"{hash_algo}", "sha2-256",
		"{created}", created,
		"{parity_identity}", identity,
		"{parity_files}\n", filesLine,
		"{parity_repair}", repair,
	)
	return []byte(repl.Replace(readmeTemplate))
}
