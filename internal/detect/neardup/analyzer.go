package neardup

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Signal codes the analyzer emits.
const (
	// CodeCertain is a near-copy of confirmed spam: CERTAIN known evidence (DESIGN.md §7.0).
	CodeCertain = "corpus.near_duplicate"
	// CodeLoose is related to confirmed spam but outside the tight limits: a rules signal that
	// only sends the message on to the AI, or to admins without one (DESIGN.md §7.3).
	CodeLoose = "corpus.similar"
	// CodeRelated shares its wording with confirmed spam (match terms; see Terms) without being
	// similar as a whole: a weak rules signal that only adds up with other evidence, such as an
	// offered contact channel.
	CodeRelated = "corpus.related"
)

// Default weights. A loose match on its own must reach T_review (0.60 under the default bias of
// -3.0: sigmoid(-3.0 + 3.5) ≈ 0.62), because "a looser match … sends the message to the AI"
// (DESIGN.md §7.5). A group that raises T_review above 0.62 makes a lone loose match clean. It
// can't delete or ban: only known evidence or an AI verdict does.
//
// A related match is weaker: 2.5 alone gives p ≈ 0.38, clean, and reaches T_review only with
// at least +0.9 of other evidence, such as contact.advertised (+1.0).
const (
	WeightCertain = 4.0
	WeightLoose   = 3.5
	WeightRelated = 2.5
)

// minTextRunes skips surfaces too short to say anything ("ok", "👍"): a two-letter reply that
// happens to equal a confirmed message's only word would otherwise be a near-duplicate.
const minTextRunes = 8

// Indexes gives the confirmed-spam indexes that apply to a group, most specific first (the
// group's own corpus, then shared ones it subscribes to). A nil entry is skipped.
type Indexes func(group wa.JID) []*Index

// Analyzer is the T1 near-duplicate analyzer (DESIGN.md §7.1, §7.5 "How it feeds back" item 2).
// It fingerprints every normalized text surface and matches it against the corpus after the
// fingerprint allow-list check. P is the group-policy type of the detect framework; the analyzer
// doesn't read it.
type Analyzer[P any] struct {
	indexes Indexes
	whole   func(e *envelope.Envelope) string
}

// Option configures an Analyzer.
type Option func(*options)

type options struct {
	whole func(e *envelope.Envelope) string
}

// WithWholeText also matches the whole message as one text, built by whole: give it the function
// the corpus stored its entries' text with (corpus.FromMessage's redacted normalized text), so a
// resend of confirmed spam that contains a phone number or a mention compares like for like.
func WithWholeText(whole func(e *envelope.Envelope) string) Option {
	return func(o *options) { o.whole = whole }
}

// NewAnalyzer returns the analyzer over the given indexes.
func NewAnalyzer[P any](indexes Indexes, opts ...Option) *Analyzer[P] {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &Analyzer[P]{indexes: indexes, whole: o.whole}
}

var _ detect.Analyzer[envelope.Envelope, struct{}] = (*Analyzer[struct{}])(nil)

// Name implements detect.Analyzer.
func (a *Analyzer[P]) Name() string { return "neardup" }

// Tier implements detect.Analyzer.
func (a *Analyzer[P]) Tier() detect.Tier { return detect.T1 }

// Applies implements detect.Analyzer: only messages with text.
func (a *Analyzer[P]) Applies(e *envelope.Envelope, _ *P) bool { return len(e.Texts) > 0 }

// Analyze implements detect.Analyzer. It emits at most one signal: the closest match over all
// surfaces and indexes, certain before loose before related. Surfaces are matched by
// FingerprintNorm, so entries must be fingerprinted with FingerprintText or FingerprintNorm (an
// entry from Compute(Key(norm)) has no match terms and never matches as related).
func (a *Analyzer[P]) Analyze(ctx context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	type candidate struct {
		source string
		fp     Fingerprint
	}
	var cands []candidate
	for _, t := range e.Texts {
		if len([]rune(t.Norm)) >= minTextRunes {
			cands = append(cands, candidate{t.Source, FingerprintNorm(t.Norm)})
		}
	}
	if a.whole != nil {
		if w := a.whole(e); len([]rune(w)) >= minTextRunes {
			cands = append(cands, candidate{"message", FingerprintNorm(w)})
		}
	}
	indexes := a.indexes(e.Group)
	// An overturned message never matches again, against any index (§7.5): if any of its forms
	// (a surface or the whole text) is allow-listed, none of them is matched.
	for _, c := range cands {
		if allowed(indexes, c.fp) {
			return nil, nil
		}
	}
	var best Match
	var source string
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, ix := range indexes {
			if ix == nil {
				continue
			}
			if m := ix.Match(c.fp); m.Closeness != None && better(m, best) {
				best, source = m, c.source
			}
		}
	}
	switch best.Closeness {
	case Certain:
		return []detect.Signal{{Code: CodeCertain, Weight: WeightCertain, Known: true, Evidence: evidence(best, source)}}, nil
	case Loose:
		return []detect.Signal{{Code: CodeLoose, Weight: WeightLoose, Evidence: evidence(best, source)}}, nil
	case Related:
		return []detect.Signal{{Code: CodeRelated, Weight: WeightRelated, Evidence: fmt.Sprintf(
			"%s shares %.0f%% of its wording with confirmed spam %s", source, best.Containment*100, best.EntryID)}}, nil
	default:
		return nil, nil
	}
}

func allowed(indexes []*Index, fp Fingerprint) bool {
	for _, ix := range indexes {
		if ix != nil && ix.Allowed(fp) {
			return true
		}
	}
	return false
}

func evidence(m Match, source string) string {
	return fmt.Sprintf("%s %.0f%% similar to confirmed spam %s (SimHash distance %d)", source, m.Jaccard*100, m.EntryID, m.Distance)
}
