// Package links gathers every link in an envelope, canonicalized, for the analyzers that
// check links against lists (DESIGN.md §6 URL analyzer, steps 1–4).
package links

import (
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/urls"
)

// Link is one canonicalized link and where it was found.
type Link struct {
	urls.URL
	Source string // the URLRef source, or the text surface source for links found in text
}

// SchemeLess is the canonical URL without its scheme ("example.com/path?q"), the form URL
// prefix rules and url fingerprints use, so http and https links match alike.
func (l Link) SchemeLess() string {
	if i := strings.Index(l.Canonical, "://"); i >= 0 {
		return l.Canonical[i+3:]
	}
	return l.Canonical
}

// Collect returns the distinct links of e, by canonical form, in a stable order: e.URLs,
// then links in contact cards, the ad context and status attributions, then links written in
// the text surfaces that the builder didn't already list. Strings that don't canonicalize
// (no host, another scheme) are skipped.
func Collect(e *envelope.Envelope) []Link {
	var out []Link
	seen := map[string]bool{}
	add := func(raw, source string) {
		u, err := urls.Canonicalize(raw)
		if err != nil || seen[u.Canonical] {
			return
		}
		seen[u.Canonical] = true
		out = append(out, Link{URL: u, Source: source})
	}
	for _, r := range e.URLs {
		add(r.URL, r.Source)
	}
	for _, c := range e.Contacts {
		for _, u := range c.URLs {
			add(u, "vcard")
		}
	}
	if e.Ads != nil && e.Ads.SourceURL != "" {
		add(e.Ads.SourceURL, "ad")
	}
	if e.Status != nil {
		for _, a := range e.Status.Attributions {
			if a.ActionURL != "" {
				add(a.ActionURL, "status")
			}
			for _, u := range a.ExternalShareURLs {
				add(u, "status")
			}
		}
	}
	for i := range e.Texts {
		t := &e.Texts[i]
		for _, u := range urls.Extract(t.Raw) {
			add(u, t.Source)
		}
		if t.Norm != "" && t.Norm != t.Raw {
			for _, u := range urls.Extract(t.Norm) {
				add(u, t.Source)
			}
		}
	}
	return out
}
