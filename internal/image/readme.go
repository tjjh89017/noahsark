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

	repl := strings.NewReplacer(
		"{repo_uuid}", uuidText(opts.RepoUUID),
		"{disc_uuid}", uuidText(opts.DiscUUID),
		"{disc_seq}", fmt.Sprintf("%d", discSeq),
		"{label}", labelText(label),
		"{hash_algo}", "sha2-256",
		"{created}", created,
		"{fec_k}", fmt.Sprintf("%d", fec.K),
		"{fec_m}", fmt.Sprintf("%d", fec.M),
	)
	return []byte(repl.Replace(readmeTemplate))
}
