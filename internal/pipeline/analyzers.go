package pipeline

import (
	"context"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/domain"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/exact"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/script"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textrules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// AnalyzerSet is everything the v0.1 fast lane runs (DESIGN.md §7.1 T0–T1).
type AnalyzerSet struct {
	Rules  *rules.Holder            // rules, fingerprints and blacklist snapshot (F-041)
	Hash   func(e164 string) string // blacklist.Hasher.Hash with the deployment key
	Groups Groups                   // the group settings the rule views read
	Bot    wa.JID
	Corpus neardup.Indexes // confirmed-spam near-duplicate indexes (F-117); nil: none
	// CorpusText builds a message's whole text the way corpus entries store theirs (redacted,
	// normalized), so a resend of confirmed spam with a number or a mention matches it. Nil:
	// only the text surfaces are matched.
	CorpusText func(e *envelope.Envelope) string
}

// Analyzers returns the T0/T1 analyzers: keyword, regex and fuzzy phrase rules, script
// anomalies, domain lists, exact hashes and advertised numbers, the manual blacklist (F-127)
// and the corpus near-duplicate matcher.
func (a AnalyzerSet) Analyzers() []Analyzer {
	src := ruleview.Source{Rules: a.Rules, Settings: a.viewOptions}
	out := []Analyzer{
		textrules.NewKeyword[policy.Settings](src),
		textrules.NewRegex[policy.Settings](src),
		textrules.NewFuzzy[policy.Settings](src),
		script.New[policy.Settings](),
		domain.New[policy.Settings](src),
		exact.New[policy.Settings](src, a.Hash),
		blacklist.New[policy.Settings](src, a.Hash),
	}
	if a.Corpus != nil {
		var opts []neardup.Option
		if a.CorpusText != nil {
			opts = append(opts, neardup.WithWholeText(a.CorpusText))
		}
		out = append(out, neardup.NewAnalyzer[policy.Settings](a.Corpus, opts...))
	}
	return out
}

// viewOptions selects the rule scopes for a message from its group's settings (§12.3): the
// subscribed packs, the allow-listed domains and the instance blacklist.
func (a AnalyzerSet) viewOptions(e *envelope.Envelope) rules.ViewOptions {
	o := rules.ViewOptions{Bot: string(a.Bot), Group: string(e.Group), UseInstanceBlacklist: true}
	if a.Groups == nil {
		return o
	}
	// The settings were already loaded for this message; a cached read keeps this cheap.
	s, ok, err := a.Groups.Settings(context.Background(), e.Group)
	if err != nil || !ok {
		return o
	}
	o.Packs = s.BlocklistRefs
	o.AllowDomains = append(append([]string(nil), s.Allowlist.Domains...), s.Content.LinkDomains.Allow...)
	return o
}
