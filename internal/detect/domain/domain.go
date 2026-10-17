// Package domain checks every link's domain and URL against the per-group, per-bot and
// global allow and deny lists (DESIGN.md §6 URL analyzer step 4, §12.1, §12.3) at T0. A hard
// deny is known spam evidence (§7.3); an allow-listed domain never fires.
package domain

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/links"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// Signal codes.
const (
	// CodeDomainBlocklisted: a link to a domain on a deny list with a hard rule. Known.
	CodeDomainBlocklisted = "url.domain_blocklisted"
	// CodeDomainListed: a link to a domain on a weighted list.
	CodeDomainListed = "url.domain_listed"
	// CodeURLBlocklisted: a link under a hard URL-prefix rule or a spam url fingerprint. Known.
	CodeURLBlocklisted = "url.blocklisted"
	// CodeURLListed: a link under a weighted URL-prefix rule.
	CodeURLListed = "url.listed"
)

// Analyzer is the domain-list analyzer (T0).
type Analyzer[P any] struct{ src ruleview.Source }

// New returns the analyzer.
func New[P any](src ruleview.Source) *Analyzer[P] { return &Analyzer[P]{src: src} }

// Name implements detect.Analyzer.
func (*Analyzer[P]) Name() string { return "url.domain_list" }

// Tier implements detect.Analyzer.
func (*Analyzer[P]) Tier() detect.Tier { return detect.T0 }

// Applies implements detect.Analyzer: messages that can carry links.
func (*Analyzer[P]) Applies(e *envelope.Envelope, _ *P) bool {
	return len(e.URLs) > 0 || len(e.Texts) > 0 || len(e.Contacts) > 0 || e.Ads != nil || e.Status != nil
}

// Analyze implements detect.Analyzer. Each link is checked against its domain lists, then its
// URL-prefix lists, then its url fingerprint. An allow, or a hard (known) deny, ends the check
// for that link; a weighted hit doesn't. One signal is emitted per code, for the first link
// that produced it.
func (a *Analyzer[P]) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	v := a.src.View(e)
	var out []detect.Signal
	emitted := map[string]bool{}
	emit := func(s detect.Signal) {
		if !emitted[s.Code] {
			emitted[s.Code] = true
			out = append(out, s)
		}
	}
	for _, l := range links.Collect(e) {
		d := v.Domain(l.Host)
		if d.Allowed {
			continue
		}
		if d.Rule != nil {
			emit(signal(d, l, CodeDomainBlocklisted, CodeDomainListed, "domain "+d.Matched))
			if d.Rule.ActionClass == rules.ActionHard {
				continue // known already; nothing more specific can add to it
			}
		}
		// A weighted domain doesn't end the check: a URL prefix or a confirmed spam URL under
		// it is stronger, known evidence.
		sl := l.SchemeLess()
		u := v.URL(sl)
		if u.Allowed {
			continue
		}
		if u.Rule != nil {
			emit(signal(u, l, CodeURLBlocklisted, CodeURLListed, "link "+u.Matched+"…"))
			if u.Rule.ActionClass == rules.ActionHard {
				continue
			}
		}
		if r := v.Exact(rules.FPURL, sl); r.Fingerprint != nil {
			emit(detect.Signal{
				Code: CodeURLBlocklisted, Weight: rules.KnownWeight(nil), Known: true,
				Evidence: fmt.Sprintf("link %s (%s) is known spam (%s)", l.Canonical, l.Source, r.Scope),
			})
		}
	}
	return out, nil
}

func signal(r rules.ListResult, l links.Link, knownCode, weightedCode, what string) detect.Signal {
	if r.Rule.ActionClass == rules.ActionHard {
		return detect.Signal{
			Code: knownCode, Weight: rules.KnownWeight(r.Rule), Known: true,
			Evidence: fmt.Sprintf("link to blocklisted %s (%s list) in %s", what, r.Scope, l.Source),
		}
	}
	return detect.Signal{
		Code: weightedCode, Weight: r.Rule.Weight,
		Evidence: fmt.Sprintf("link to listed %s (%s list) in %s", what, r.Scope, l.Source),
	}
}
