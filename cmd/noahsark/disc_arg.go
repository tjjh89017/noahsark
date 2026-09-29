package main

import (
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
)

// discArgCandidate is one disc that a disc argument can name.
type discArgCandidate struct {
	Seq   uint64
	Label string
	UUID  [16]byte
}

// uniqueDiscCandidates gives one candidate for each disc uuid. A disc
// with more than one run has one ledger row for each run. The function
// drops a disc when hidden reports true for its uuid.
func uniqueDiscCandidates(rows []format.DiscsRow, hidden func(uuid [16]byte) bool) []discArgCandidate {
	seen := make(map[[16]byte]bool)
	var out []discArgCandidate
	for _, r := range rows {
		if seen[r.DiscUUID] {
			continue
		}
		seen[r.DiscUUID] = true
		if hidden != nil && hidden(r.DiscUUID) {
			continue
		}
		out = append(out, discArgCandidate{Seq: r.DiscSeq, Label: labelText(r.Label[:r.LabelLen]), UUID: r.DiscUUID})
	}
	return out
}

// resolveDiscArg resolves a disc argument against every disc of the
// ledger rows. resolveDiscArgExcept holds the rules.
func resolveDiscArg(rows []format.DiscsRow, arg string) ([16]byte, error) {
	return resolveDiscArgExcept(rows, arg, nil)
}

// discNumberMaxDigits is the length limit of a disc number argument. A
// value of decimal digits only with more digits is a uuid prefix.
const discNumberMaxDigits = 7

// resolveDiscArgExcept resolves arg, a disc number, a full uuid or a
// uuid prefix, to the uuid of one disc. A value of 1 to 7 decimal
// digits is a disc number and nothing else. Every other value is a uuid
// prefix. A uuid and a prefix can have hyphens and can use any letter
// case. A disc for which hidden reports true matches no argument. A nil
// hidden hides no disc. The error text is the refusal for no match or
// for more than one match; the caller exits with the usage error code.
func resolveDiscArgExcept(rows []format.DiscsRow, arg string, hidden func(uuid [16]byte) bool) ([16]byte, error) {
	discs := uniqueDiscCandidates(rows, hidden)
	var matches []discArgCandidate

	if len(arg) <= discNumberMaxDigits {
		if seq, err := strconv.ParseUint(arg, 10, 64); err == nil {
			for _, d := range discs {
				if d.Seq == seq {
					matches = append(matches, d)
				}
			}
			return oneDisc(arg, matches)
		}
	}

	if prefix, ok := uuidPrefix(arg); ok {
		for _, d := range discs {
			if strings.HasPrefix(hex.EncodeToString(d.UUID[:]), prefix) {
				matches = append(matches, d)
			}
		}
	}
	return oneDisc(arg, matches)
}

// uuidPrefix removes the hyphens from s and makes it lower case. It
// reports false when the result is empty, is longer than a uuid, or
// holds a character that is not hexadecimal.
func uuidPrefix(s string) (string, bool) {
	p := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	if p == "" || len(p) > 32 {
		return "", false
	}
	for _, c := range p {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", false
		}
	}
	return p, true
}

// oneDisc gives the uuid of the only match, or the refusal for no match
// or for more than one match.
func oneDisc(arg string, matches []discArgCandidate) ([16]byte, error) {
	switch len(matches) {
	case 0:
		return [16]byte{}, errors.New("no disc matches " + arg)
	case 1:
		return matches[0].UUID, nil
	}
	var b strings.Builder
	b.WriteString(arg + " matches more than one disc:")
	for _, d := range matches {
		b.WriteString("\n" + discNameShort(d.Seq, d.Label) + "  " + uuidText(d.UUID))
	}
	return [16]byte{}, errors.New(b.String())
}
