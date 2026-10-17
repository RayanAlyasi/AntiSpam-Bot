// Package textrules holds the analyzers for text rules (DESIGN.md §6.1, §7.2): keyword and
// phrase lists matched with Aho-Corasick (T0), RE2 regex rules (T0) and fuzzy near-misses of
// phrases (T1). They read the normalized variants textnorm.Prepare filled in.
package textrules

import (
	"context"
	"fmt"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textmatch"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// Signal codes.
const (
	// CodeKeyword: weighted keyword or phrase rules matched; the weight is their sum.
	CodeKeyword = "text.keyword"
	// CodeKeywordBlocked: a hard keyword or phrase rule matched (a policy violation, §9.3).
	CodeKeywordBlocked = "text.keyword_blocked"
	// CodeRegex: weighted regex rules matched.
	CodeRegex = "text.regex"
	// CodeRegexBlocked: a hard regex rule matched (a policy violation).
	CodeRegexBlocked = "text.regex_blocked"
	// CodeFuzzy: a phrase rule matched approximately. Never CERTAIN: it only sends the
	// message on to verification (§7.0).
	CodeFuzzy = "text.fuzzy_match"
)

// MaxWeight caps the summed weight of one text-rule signal, so a message that repeats many
// listed words can't outweigh every other signal on its own.
const MaxWeight = 6.0

// DefaultFuzzyWeight is the weight of a fuzzy match of a hard rule, or of a weighted rule
// with no positive weight, before scaling by similarity.
const DefaultFuzzyWeight = 2.0

type hit struct {
	rule    *rules.Rule
	source  string
	matched string
	sim     float64
}

// collect runs match on every text surface and keeps the first hit per rule.
func collect(e *envelope.Envelope, match func(rules.Text) []rules.TextHit) []hit {
	out, _ := collectErr(e, func(t rules.Text) ([]rules.TextHit, error) { return match(t), nil })
	return out
}

func collectErr(e *envelope.Envelope, match func(rules.Text) ([]rules.TextHit, error)) ([]hit, error) {
	var out []hit
	seen := map[int64]bool{}
	for i := range e.Texts {
		t := &e.Texts[i]
		hs, err := match(textnorm.Text(t))
		if err != nil {
			return nil, err
		}
		for _, h := range hs {
			if !seen[h.Rule.ID] {
				seen[h.Rule.ID] = true
				out = append(out, hit{rule: h.Rule, source: t.Source, matched: h.Matched, sim: h.Similarity})
			}
		}
	}
	return out, nil
}

// maxEvidenceRunes bounds how much matched message text one evidence item quotes.
const maxEvidenceRunes = 60

func quote(s string) string {
	if r := []rune(s); len(r) > maxEvidenceRunes {
		s = string(r[:maxEvidenceRunes]) + "…"
	}
	return fmt.Sprintf("%q", s)
}

// describe names a hit for admins: the matched text, and the listed phrase when it differs
// (an obfuscated spelling, or any regex).
func describe(h hit) string {
	if h.matched == "" || textmatch.Words(h.matched) == h.rule.NormPattern {
		return fmt.Sprintf("%s in %s", quote(h.rule.Pattern), h.source)
	}
	if h.rule.Type == rules.TypeRegex {
		return fmt.Sprintf("%s (rule %d) in %s", quote(h.matched), h.rule.ID, h.source)
	}
	return fmt.Sprintf("%s as %s in %s", quote(h.rule.Pattern), quote(h.matched), h.source)
}

// signals turns exact text-rule hits into at most two signals: one Hard signal for hard
// rules and one weighted signal for the others.
func signals(hits []hit, kind, weightedCode, hardCode string) []detect.Signal {
	var hard, weighted []string
	sum := 0.0
	for _, h := range hits {
		if h.rule.ActionClass == rules.ActionHard {
			hard = append(hard, describe(h))
			continue
		}
		sum += h.rule.Weight
		weighted = append(weighted, fmt.Sprintf("%s (%+.1f)", describe(h), h.rule.Weight))
	}
	var out []detect.Signal
	if len(hard) > 0 {
		out = append(out, detect.Signal{
			Code: hardCode, Hard: true,
			Evidence: fmt.Sprintf("blocked %s %s", kind, strings.Join(hard, ", ")),
		})
	}
	if len(weighted) > 0 {
		out = append(out, detect.Signal{
			Code: weightedCode, Weight: max(-MaxWeight, min(sum, MaxWeight)),
			Evidence: fmt.Sprintf("listed %s %s", kind, strings.Join(weighted, ", ")),
		})
	}
	return out
}

func hasText(e *envelope.Envelope) bool { return len(e.Texts) > 0 }

// Keyword matches keyword and phrase rules (T0).
type Keyword[P any] struct{ src ruleview.Source }

// NewKeyword returns the keyword analyzer.
func NewKeyword[P any](src ruleview.Source) *Keyword[P] { return &Keyword[P]{src: src} }

// Name implements detect.Analyzer.
func (*Keyword[P]) Name() string { return "text.keyword" }

// Tier implements detect.Analyzer.
func (*Keyword[P]) Tier() detect.Tier { return detect.T0 }

// Applies implements detect.Analyzer: any message with text.
func (*Keyword[P]) Applies(e *envelope.Envelope, _ *P) bool { return hasText(e) }

// Analyze implements detect.Analyzer.
func (a *Keyword[P]) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	return signals(collect(e, a.src.View(e).Keywords), "phrase", CodeKeyword, CodeKeywordBlocked), nil
}

// Regex matches regex rules (T0).
type Regex[P any] struct{ src ruleview.Source }

// NewRegex returns the regex analyzer.
func NewRegex[P any](src ruleview.Source) *Regex[P] { return &Regex[P]{src: src} }

// Name implements detect.Analyzer.
func (*Regex[P]) Name() string { return "text.regex" }

// Tier implements detect.Analyzer.
func (*Regex[P]) Tier() detect.Tier { return detect.T0 }

// Applies implements detect.Analyzer: any message with text.
func (*Regex[P]) Applies(e *envelope.Envelope, _ *P) bool { return hasText(e) }

// Analyze implements detect.Analyzer.
func (a *Regex[P]) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	return signals(collect(e, a.src.View(e).Regexes), "pattern", CodeRegex, CodeRegexBlocked), nil
}

// Fuzzy matches near-misses of phrase rules (T1). Its signal is always weighted, whatever
// the rule's class: an approximate match is never CERTAIN evidence (§7.0, §7.3).
type Fuzzy[P any] struct{ src ruleview.Source }

// NewFuzzy returns the fuzzy analyzer.
func NewFuzzy[P any](src ruleview.Source) *Fuzzy[P] { return &Fuzzy[P]{src: src} }

// Name implements detect.Analyzer.
func (*Fuzzy[P]) Name() string { return "text.fuzzy" }

// Tier implements detect.Analyzer.
func (*Fuzzy[P]) Tier() detect.Tier { return detect.T1 }

// Applies implements detect.Analyzer: any message with text.
func (*Fuzzy[P]) Applies(e *envelope.Envelope, _ *P) bool { return hasText(e) }

// Analyze implements detect.Analyzer. A rule that also matched exactly in any surface is
// left to the Keyword analyzer.
func (a *Fuzzy[P]) Analyze(ctx context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	v := a.src.View(e)
	exact := map[int64]bool{}
	for _, h := range collect(e, v.Keywords) {
		exact[h.rule.ID] = true
	}
	hits, err := collectErr(e, func(t rules.Text) ([]rules.TextHit, error) { return v.Fuzzy(ctx, t) })
	if err != nil {
		return nil, err
	}
	var parts []string
	sum := 0.0
	for _, h := range hits {
		if exact[h.rule.ID] {
			continue
		}
		w := h.rule.Weight
		if h.rule.ActionClass == rules.ActionHard || w <= 0 {
			w = DefaultFuzzyWeight
		}
		w *= h.sim
		sum += w
		parts = append(parts, fmt.Sprintf("%s ≈ %s in %s (%.0f%% similar, %+.1f)", quote(h.matched), quote(h.rule.Pattern), h.source, h.sim*100, w))
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return []detect.Signal{{
		Code: CodeFuzzy, Weight: max(-MaxWeight, min(sum, MaxWeight)),
		Evidence: "text close to listed phrase " + strings.Join(parts, ", "),
	}}, nil
}
