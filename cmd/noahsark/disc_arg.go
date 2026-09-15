package main

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
)

// discArgCandidate is one disc a resolveDiscArg refusal lists: enough to
// print "seq  label  uuid-prefix", and, on a match, the disc's own
// uuid.
type discArgCandidate struct {
	Seq   uint64
	Label string
	UUID  [16]byte
}

// uniqueDiscCandidates folds ledger rows down to one entry per disc
// uuid, since a disc with more than one run on it has one ledger row
// per run.
func uniqueDiscCandidates(rows []format.DiscsRow) []discArgCandidate {
	seen := make(map[[16]byte]bool)
	var out []discArgCandidate
	for _, r := range rows {
		if seen[r.DiscUUID] {
			continue
		}
		seen[r.DiscUUID] = true
		out = append(out, discArgCandidate{Seq: r.DiscSeq, Label: labelText(r.Label[:r.LabelLen]), UUID: r.DiscUUID})
	}
	return out
}

// isDecimal reports whether s is a decimal integer of 1 to 7 digits.
func isDecimal(s string) bool {
	if len(s) == 0 || len(s) > 7 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// asHexPrefix reports whether s, with any hyphens removed, is 8 or more
// hexadecimal characters, and returns that cleaned form.
func asHexPrefix(s string) (clean string, ok bool) {
	clean = strings.ToLower(strings.ReplaceAll(s, "-", ""))
	if len(clean) < 8 {
		return clean, false
	}
	if _, err := hex.DecodeString(padHexEven(clean)); err != nil {
		return clean, false
	}
	return clean, true
}

// padHexEven appends a "0" when s has an odd length, so hex.DecodeString
// can validate an odd-length prefix's characters without rejecting it
// only for its length.
func padHexEven(s string) string {
	if len(s)%2 != 0 {
		return s + "0"
	}
	return s
}

// uuidHex is d's uuid as plain lowercase hex, no hyphens.
func uuidHex(d [16]byte) string {
	return hex.EncodeToString(d[:])
}

// resolveDiscArg resolves arg, a command-line disc argument, against
// repo's disc list, in this order:
//
//  1. A decimal integer of 7 digits or fewer: match disc_seq.
//  2. A full uuid, 32 hex characters with or without hyphens: exact
//     match.
//  3. 8 or more hex characters: a uuid prefix, accepted only when
//     exactly one disc matches.
//  4. Any other text: an exact match against the on-disc label,
//     accepted only when exactly one disc matches.
//
// It refuses with an error listing every candidate disc when arg
// matches zero discs, or more than one.
func resolveDiscArg(rows []format.DiscsRow, arg string) ([16]byte, error) {
	discs := uniqueDiscCandidates(rows)

	if isDecimal(arg) {
		seq, _ := strconv.ParseUint(arg, 10, 64)
		for _, d := range discs {
			if d.Seq == seq {
				return d.UUID, nil
			}
		}
		return [16]byte{}, refuseDiscArgNoMatch(arg, discs)
	}

	if clean, ok := asHexPrefix(arg); ok {
		if len(clean) == 32 {
			raw, err := hex.DecodeString(clean)
			if err == nil {
				var uuid [16]byte
				copy(uuid[:], raw)
				for _, d := range discs {
					if d.UUID == uuid {
						return d.UUID, nil
					}
				}
			}
			return [16]byte{}, refuseDiscArgNoMatch(arg, discs)
		}
		var matches []discArgCandidate
		for _, d := range discs {
			if strings.HasPrefix(uuidHex(d.UUID), clean) {
				matches = append(matches, d)
			}
		}
		if len(matches) == 1 {
			return matches[0].UUID, nil
		}
		if len(matches) > 1 {
			return [16]byte{}, refuseDiscArgAmbiguous(arg, matches)
		}
		return [16]byte{}, refuseDiscArgNoMatch(arg, discs)
	}

	var matches []discArgCandidate
	for _, d := range discs {
		if d.Label == arg {
			matches = append(matches, d)
		}
	}
	if len(matches) == 1 {
		return matches[0].UUID, nil
	}
	if len(matches) > 1 {
		return [16]byte{}, refuseDiscArgAmbiguous(arg, matches)
	}
	return [16]byte{}, refuseDiscArgNoMatch(arg, discs)
}

// refuseDiscArgNoMatch builds the error resolveDiscArg returns when arg
// matched no disc, listing every disc in the repository, one per line,
// as "seq  label  uuid-prefix".
func refuseDiscArgNoMatch(arg string, discs []discArgCandidate) error {
	return fmt.Errorf("%q matches no disc in this repository's disc list%s", arg, candidateLines(discs))
}

// refuseDiscArgAmbiguous builds the error resolveDiscArg returns when
// arg matched more than one disc, listing every match, one per line, as
// "seq  label  uuid-prefix".
func refuseDiscArgAmbiguous(arg string, matches []discArgCandidate) error {
	return fmt.Errorf("%q matches more than one disc; use the seq or the full uuid:%s", arg, candidateLines(matches))
}

// candidateLines renders one "\n  seq  label  uuid-prefix" line per
// candidate.
func candidateLines(candidates []discArgCandidate) string {
	var b strings.Builder
	for _, d := range candidates {
		fmt.Fprintf(&b, "\n  %d  %s  %s", d.Seq, d.Label, uuidHex(d.UUID)[:8])
	}
	return b.String()
}
