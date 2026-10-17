package policy

import (
	"slices"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// PriorPrefix is the code prefix of identity-prior signals (DESIGN.md §7.6): number prefix,
// picture and name reuse. Analyzers that emit priors must name them "intel.<kind>"; they never
// act alone (DET-003).
const PriorPrefix = "intel."

// FindingFrom turns a scored verdict (after the AI tier resolved it) into the decision
// engine's input. Counted signals keep their applied weight; hard signals become violations
// (rule = signal code) next to the violations EvaluateContent found, unless one with that rule
// is already there.
func FindingFrom(v scoring.Verdict, ct ContentType, violations []Violation, at time.Time) Finding {
	f := Finding{
		Band:       Band(v.Band),
		Source:     Source(v.Source),
		Score:      v.Score.P,
		Type:       ct,
		Violations: append([]Violation(nil), violations...),
		At:         at,
	}
	if f.Band == "" {
		f.Band = BandClean
	}
	for _, s := range v.Score.Signals {
		if s.Hard {
			if !slices.ContainsFunc(f.Violations, func(x Violation) bool { return x.Rule == s.Code }) {
				f.Violations = append(f.Violations, Violation{Rule: s.Code, Detail: s.Evidence})
			}
			continue
		}
		if !s.Counted {
			continue
		}
		f.Signals = append(f.Signals, Signal{
			Code: s.Code, Weight: s.Applied, Known: s.Known,
			Prior: strings.HasPrefix(s.Code, PriorPrefix),
		})
	}
	return f
}

// JustJoinedScoringSignal is the just-joined signal as a scoring input (DESIGN.md §8.2): the
// pipeline appends it to the analyzers' signals before scoring. Its weight is the group's
// configured weight, decayed over the window; there's no entry in the weight table, so the
// group setting is what counts. It is never shown to members.
func JustJoinedScoringSignal(cfg JustJoined, joinedAt, now time.Time) (scoring.Signal, bool) {
	s, ok := JustJoinedSignal(cfg, joinedAt, now)
	if !ok {
		return scoring.Signal{}, false
	}
	return scoring.Signal{Code: s.Code, Weight: s.Weight, Evidence: "sender joined recently"}, true
}
