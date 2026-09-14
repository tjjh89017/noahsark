package image

import (
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/fec"
	"github.com/tjjh89017/noahsark/internal/format"
)

// readmeTemplate is the fixed README.txt text, copied byte for byte from
// FORMAT.md, with its substitution slots still in place.
//
//go:embed readme_template.txt
var readmeTemplate string

// FormatTxt is the fixed FORMAT.txt text for format major 1 minor 0,
// copied byte for byte from FORMAT.md. It carries no substitution slot;
// a writer of this minor version writes these bytes and no others.
//
//go:embed format.txt
var FormatTxt []byte

// mediaTypeNames names the media type registry, for README.txt's
// {media_type} slot.
var mediaTypeNames = map[format.MediaType]string{
	format.MediaTypeBDRSL25GB:   "BD-R SL 25 GB",
	format.MediaTypeBDRDL50GB:   "BD-R DL 50 GB",
	format.MediaTypeBDRXL100GB:  "BD-R XL 100 GB",
	format.MediaTypeBDRXL128GB:  "BD-R XL 128 GB",
	format.MediaTypeDVDPlusRSL:  "DVD+R SL 4.7 GB",
	format.MediaTypeDVDMinusRSL: "DVD-R SL 4.7 GB",
}

// fsProfileNames names the disc filesystem profile registry, for
// README.txt's {fs_profile} slot.
var fsProfileNames = map[format.DiscFSProfile]string{
	format.DiscFSProfileOneshot: "oneshot",
}

// chunkerProfileNames names the chunker profile registry, for
// README.txt's {chunker_profile} slot.
var chunkerProfileNames = map[format.ChunkerProfile]string{
	format.ChunkerProfileP3: "P3",
	format.ChunkerProfileP4: "P4",
	format.ChunkerProfileP5: "P5",
}

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
// and the first run's RUN.bin. hash_algo and chunker_profile name the
// first run's header fields; the superblock carries no such fields of
// its own.
func buildReadme(opts BuildOptions, packTime time.Time, label []byte) []byte {
	_, tzOffset := packTime.Zone()
	sign := "+"
	if tzOffset < 0 {
		sign = "-"
		tzOffset = -tzOffset
	}
	created := fmt.Sprintf("%s%s%02d:%02d",
		packTime.Format("2006-01-02T15:04:05"), sign, tzOffset/3600, (tzOffset%3600)/60)

	repl := strings.NewReplacer(
		"{version_minor}", "0",
		"{repo_uuid}", uuidText(opts.RepoUUID),
		"{disc_uuid}", uuidText(opts.DiscUUID),
		"{disc_seq}", fmt.Sprintf("%d", buildDiscSeq),
		"{label}", labelText(label),
		"{media_type}", mediaTypeNames[opts.MediaType],
		"{fs_profile}", fsProfileNames[format.DiscFSProfileOneshot],
		"{hash_algo}", "sha2-256",
		"{chunker_profile}", chunkerProfileNames[format.ChunkerProfileP4],
		"{created}", created,
		"{fanout_levels}", "1",
		"{fec_k}", fmt.Sprintf("%d", fec.K),
		"{fec_m}", fmt.Sprintf("%d", fec.M),
	)
	return []byte(repl.Replace(readmeTemplate))
}
