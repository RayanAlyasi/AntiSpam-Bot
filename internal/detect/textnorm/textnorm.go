// Package textnorm fills in the normalized forms of every text surface of an envelope
// (DESIGN.md §5.3, §7.2) and attaches the phone numbers written in them, before any analyzer
// runs. Raw is never changed: it stays the evidence.
package textnorm

import (
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

// Prepare normalizes every text surface of e in place: Norm, Skeleton, Leet, Loose, the count
// of stripped obfuscation characters and the spam emoji tokens, from normalize.Text(Raw). It
// then sets e.Phones to the numbers the message advertises: those written in the surfaces,
// found with normalize.FindPhones in Raw and in Norm, then the waid and TEL numbers of its contact cards, each
// valid number once, in that order. region is the group's default country (ISO 3166-1
// alpha-2) for numbers written without a country code; empty accepts only international
// ones. Calling it again gives the same result.
func Prepare(e *envelope.Envelope, region string) {
	e.Phones = nil
	seen := map[string]bool{}
	add := func(e164, source string) {
		if !seen[e164] {
			seen[e164] = true
			e.Phones = append(e.Phones, envelope.PhoneRef{E164: e164, Source: source})
		}
	}
	for i := range e.Texts {
		t := &e.Texts[i]
		r := normalize.Text(t.Raw)
		t.Norm, t.Skeleton, t.Leet, t.Loose = r.Norm, r.Skeleton, r.Leet, r.Loose
		t.Stripped, t.EmojiTokens = r.Stripped, r.EmojiTokens
		for _, p := range normalize.FindPhones(t.Raw, region) {
			add(p.E164, t.Source)
		}
		// NFKC turns circled, superscript and other compatibility digits into ASCII ones
		// (⓪⑤⑤… → 055…); FindPhones doesn't read those forms in Raw.
		if t.Norm != t.Raw {
			for _, p := range normalize.FindPhones(t.Norm, region) {
				add(p.E164, t.Source)
			}
		}
	}
	for _, c := range e.Contacts {
		for _, w := range c.WAIDs {
			// A waid is the full international number without the plus.
			if n, err := normalize.ParsePhone("+"+strings.TrimPrefix(w, "+"), ""); err == nil {
				add(n, SourceVCard)
			}
		}
		for _, tel := range c.Phones {
			if n, err := normalize.ParsePhone(tel, region); err == nil {
				add(n, SourceVCard)
			}
		}
	}
}

// SourceVCard is the PhoneRef source of numbers from contact cards.
const SourceVCard = "vcard"

// Text returns the variants of a prepared surface that rules match on.
func Text(t *envelope.TextSurface) rules.Text {
	return rules.Text{Norm: t.Norm, Skeleton: t.Skeleton, Leet: t.Leet, Loose: t.Loose}
}
