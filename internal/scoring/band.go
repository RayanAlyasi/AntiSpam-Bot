package scoring

import (
	"errors"
	"slices"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
)

// Source is the strongest kind of evidence behind a verdict (DESIGN.md §7.3). The auto-ban
// matrix uses it to decide which evidence is enough to ban (§9.2).
type Source string

// Confidence sources.
const (
	SourceNone  Source = ""      // no spam evidence (clean, or a policy violation only)
	SourceRules Source = "rules" // heuristics, enrichment, behaviour, loose near-duplicates: never act alone
	SourceKnown Source = "known" // known spam: the only source that can auto-ban by default
	SourceAI    Source = "ai"    // the AI verified it; an AI-only ban goes to admins (§9.2)
)

// Band is a certainty band (DESIGN.md §7.0).
type Band string

// Bands.
const (
	BandClean          Band = "clean"           // below T_review, or the AI confidently said not spam
	BandCertain        Band = "certain"         // known spam or a hard policy violation: act at once
	BandUncertain      Band = "uncertain"       // suspicious: the AI verifies (source rules → ai)
	BandStillUncertain Band = "still_uncertain" // the AI couldn't settle it: ask admins
)

// Thresholds are a group's decision thresholds (DESIGN.md §7.3).
type Thresholds struct {
	Review       float64 // T_review: at or above it a non-certain message is suspicious
	AIConfidence float64 // ai.spam_confidence: the AI confidence that settles an UNCERTAIN message
}

// DefaultThresholds returns T_review 0.60 and ai.spam_confidence 0.9.
func DefaultThresholds() Thresholds { return Thresholds{Review: 0.60, AIConfidence: 0.9} }

// Validate reports whether the thresholds are usable: 0 < Review < 1 and 0 < AIConfidence ≤ 1.
func (t Thresholds) Validate() error {
	if !finite(t.Review) || t.Review <= 0 || t.Review >= 1 {
		return errors.New("thresholds: review must be between 0 and 1 (exclusive)")
	}
	if !finite(t.AIConfidence) || t.AIConfidence <= 0 || t.AIConfidence > 1 {
		return errors.New("thresholds: ai confidence must be above 0 and at most 1")
	}
	return nil
}

// Verdict is a scored message with its band and confidence source.
type Verdict struct {
	Band      Band
	Source    Source
	Violation bool        // a hard policy signal fired (policy route, §9.3)
	Score     Result      // the score and the signals behind it
	AI        *ai.Verdict // the AI's answer, when Resolve was given one
}

// Classify assigns the band before the AI runs (DESIGN.md §7.0):
//   - a counted known-spam signal with positive weight → CERTAIN, source known;
//   - a hard policy signal → CERTAIN with Violation (source known only alongside known spam);
//   - otherwise P ≥ T_review → UNCERTAIN, source rules (the AI verifies);
//   - otherwise clean.
//
// Invalid thresholds fall back to the defaults.
func Classify(r Result, t Thresholds) Verdict {
	if t.Validate() != nil {
		t = DefaultThresholds()
	}
	v := Verdict{Band: BandClean, Source: SourceNone, Score: r}
	known := false
	for _, s := range r.Signals {
		if s.Hard {
			v.Violation = true
		}
		if s.Counted && s.Known && s.Applied > 0 {
			known = true
		}
	}
	switch {
	case known:
		v.Band, v.Source = BandCertain, SourceKnown
	case v.Violation:
		v.Band = BandCertain
	case r.P >= t.Review:
		v.Band, v.Source = BandUncertain, SourceRules
	}
	return v
}

// Resolve applies the AI's answer to an UNCERTAIN verdict; other bands come back unchanged,
// because only the UNCERTAIN band is sent to the AI (DESIGN.md §7.4). out is nil when the AI
// isn't configured, is out of budget, timed out or failed.
//   - spam with confidence ≥ AIConfidence → stays UNCERTAIN, source ai (AI-verified: deleted,
//     and the ban goes to admins under the early-AI safety rule, §9.2);
//   - not_spam with confidence ≥ AIConfidence → clean, source ai;
//   - anything else, including an unknown label or an out-of-range confidence → STILL_UNCERTAIN,
//     source rules (ask admins; nothing is deleted).
//
// Invalid thresholds never let the AI settle a message.
func Resolve(v Verdict, out *ai.Verdict, t Thresholds) Verdict {
	if v.Band != BandUncertain {
		return v
	}
	v.AI = nil
	if out != nil {
		cp := *out
		cp.Categories = slices.Clone(out.Categories)
		v.AI = &cp
	}
	v.Band, v.Source = BandStillUncertain, SourceRules
	if out == nil || t.Validate() != nil || !finite(out.Confidence) ||
		out.Confidence < 0 || out.Confidence > 1 || out.Confidence < t.AIConfidence {
		return v
	}
	switch out.Label {
	case ai.LabelSpam:
		v.Band, v.Source = BandUncertain, SourceAI
	case ai.LabelNotSpam:
		v.Band, v.Source = BandClean, SourceAI
	}
	return v
}

// DeletesAsSpam reports whether the verdict reaches the delete line as spam (DESIGN.md §7.3):
// CERTAIN known spam, or an AI-verified UNCERTAIN message. Heuristics alone never do. A policy
// violation's delete is the policy route's decision, not this one.
func (v Verdict) DeletesAsSpam() bool {
	return (v.Band == BandCertain && v.Source == SourceKnown) ||
		(v.Band == BandUncertain && v.Source == SourceAI)
}
