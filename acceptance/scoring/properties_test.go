package scoring_test

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// DESIGN §18: heuristics and looser near-duplicate matches never delete or ban on their own;
// the AI unsure, low confidence, timeout or AI off → admins, nothing deleted.
func TestHeuristicsAloneNeverDelete(t *testing.T) {
	const seed = 51
	rnd := newRand(seed)
	for i := range propIters {
		w := genWeights(rnd)
		sigs := genHeuristics(rnd)
		th := genThresholds(rnd)
		r := scoring.Score(w, sigs)
		v := scoring.Classify(r, th)
		if v.DeletesAsSpam() || v.Source == scoring.SourceKnown || v.Band == scoring.BandCertain {
			t.Fatalf("seed %d iter %d: rules-only %s (P = %.4f, T_review %.3f) classified %q/%q deletes %v, want clean or uncertain/rules and no delete (heuristics alone never reach the delete line, DESIGN §7.3, §18)",
				seed, i, describe(sigs), r.P, th.Review, v.Band, v.Source, v.DeletesAsSpam())
		}
		out, desc := genNotConfidentSpam(rnd, th)
		got := scoring.Resolve(v, out, th)
		if got.DeletesAsSpam() {
			t.Fatalf("seed %d iter %d: rules-only %s (P = %.4f) with AI %s: deletes as spam (band %q source %q), want no delete (without a confident AI spam verdict the message goes to admins, DESIGN §7.0, §18)",
				seed, i, describe(sigs), r.P, desc, got.Band, got.Source)
		}
		if got.Source == scoring.SourceKnown {
			t.Fatalf("seed %d iter %d: rules-only signals with AI %s gave source known, want rules, ai or none (DESIGN §7.3)", seed, i, desc)
		}
		if v.Band == scoring.BandUncertain && !(out != nil && out.Label == ai.LabelNotSpam && out.Confidence >= th.AIConfidence && out.Confidence <= 1) &&
			got.Band != scoring.BandStillUncertain {
			t.Fatalf("seed %d iter %d: UNCERTAIN with AI %s resolved to %q, want still_uncertain (ask admins, DESIGN §7.0)", seed, i, desc, got.Band)
		}
	}
}

// DESIGN §9.2: an AI-only spam verdict is source ai, never known, so the matrix can keep its ban
// with admins until the AI has a track record.
func TestAIOnlySpamIsSourceAINeverKnown(t *testing.T) {
	const seed = 52
	rnd := newRand(seed)
	tested := 0
	for i := range propIters {
		w := genWeights(rnd)
		sigs := genHeuristics(rnd)
		r := scoring.Score(w, sigs)
		if r.P <= 0 || r.P >= 1 {
			continue
		}
		th := scoring.Thresholds{Review: r.P * (0.05 + 0.95*rnd.Float64()), AIConfidence: 0.5 + rnd.Float64()*0.5}
		if th.Review <= 0 {
			continue
		}
		v := scoring.Classify(r, th)
		if v.Band != scoring.BandUncertain || v.Source != scoring.SourceRules {
			t.Fatalf("seed %d iter %d: rules-only %s with P %.4f ≥ T_review %.4f classified %q/%q, want uncertain/rules (DESIGN §7.0)",
				seed, i, describe(sigs), r.P, th.Review, v.Band, v.Source)
		}
		conf := th.AIConfidence + (1-th.AIConfidence)*rnd.Float64()
		got := scoring.Resolve(v, &ai.Verdict{Label: ai.LabelSpam, Confidence: conf}, th)
		if got.Source != scoring.SourceAI || got.Band != scoring.BandUncertain || !got.DeletesAsSpam() {
			t.Fatalf("seed %d iter %d: AI spam %.4f (≥ %.4f) on rules-only %s gave %q/%q deletes %v, want uncertain/ai and deleted (source ai, never known, DESIGN §7.3, §9.2)",
				seed, i, conf, th.AIConfidence, describe(sigs), got.Band, got.Source, got.DeletesAsSpam())
		}
		tested++
	}
	if tested < propIters/4 {
		t.Fatalf("only %d of %d generated cases were usable, want at least %d (generator check)", tested, propIters, propIters/4)
	}
}

// DESIGN §18: thresholds are monotonic. Raising T_review never turns a clean verdict uncertain,
// and a CERTAIN verdict does not depend on T_review at all.
func TestClassifyThresholdMonotonic(t *testing.T) {
	const seed = 53
	rnd := newRand(seed)
	for i := range propIters {
		w := genWeights(rnd)
		sigs := genAny(rnd)
		r := scoring.Score(w, sigs)
		lo := 0.01 + rnd.Float64()*0.98
		hi := lo + (0.99-lo)*rnd.Float64()
		vlo := scoring.Classify(r, scoring.Thresholds{Review: lo, AIConfidence: 0.9})
		vhi := scoring.Classify(r, scoring.Thresholds{Review: hi, AIConfidence: 0.9})
		if vlo.Band == scoring.BandClean && vhi.Band != scoring.BandClean {
			t.Fatalf("seed %d iter %d: %s (P = %.4f) clean at T_review %.4f but %q at higher %.4f, want clean (raising the threshold never makes a verdict harsher, DESIGN §18)",
				seed, i, describe(sigs), r.P, lo, vhi.Band, hi)
		}
		if (vlo.Band == scoring.BandCertain) != (vhi.Band == scoring.BandCertain) || (vlo.Source == scoring.SourceKnown) != (vhi.Source == scoring.SourceKnown) {
			t.Fatalf("seed %d iter %d: %s CERTAIN/known differs between T_review %.4f (%q/%q) and %.4f (%q/%q), want the same (certain evidence doesn't depend on the score threshold, DESIGN §7.0)",
				seed, i, describe(sigs), lo, vlo.Band, vlo.Source, hi, vhi.Band, vhi.Source)
		}
	}
}
