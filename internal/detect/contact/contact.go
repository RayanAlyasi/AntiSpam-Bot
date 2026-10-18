// Package contact is the contact-solicitation analyzer (DESIGN.md §6, §7.1 T0): a message that
// gives a way to reach someone outside the group. Ads for services, sick notes, homework and
// "investment" groups nearly always do; most ordinary chat doesn't. It's a weak signal that
// never reaches T_review alone, and only adds up with other evidence, such as wording related
// to confirmed spam (corpus.related).
package contact

import (
	"context"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/links"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/urls"
)

// CodeAdvertised is the signal code: the message offers a contact channel.
const CodeAdvertised = "contact.advertised"

// Weight is the default weight. With the default bias it gives p ≈ 0.12 alone, and stays below
// T_review (0.60) together with the obfuscation hints (text.mixed_script, text.invisible_chars).
const Weight = 1.0

// What a message can offer, in the order the evidence lists them.
const (
	offerPhone    = "a phone number"
	offerCard     = "a contact card"
	offerChat     = "a WhatsApp chat link"
	offerInvite   = "a group invite"
	offerChannel  = "a channel link"
	offerTelegram = "a Telegram link"
	offerPrivate  = "a request to order or ask in private"
)

var offerOrder = []string{offerPhone, offerCard, offerChat, offerInvite, offerChannel, offerTelegram, offerPrivate}

// telegramHosts are the Telegram link hosts.
var telegramHosts = map[string]bool{"t.me": true, "telegram.me": true, "telegram.dog": true}

// Analyzer emits CodeAdvertised. P is the group policy type; it isn't read.
type Analyzer[P any] struct{}

// New returns the analyzer.
func New[P any]() *Analyzer[P] { return &Analyzer[P]{} }

var _ detect.Analyzer[envelope.Envelope, struct{}] = (*Analyzer[struct{}])(nil)

// Name implements detect.Analyzer.
func (*Analyzer[P]) Name() string { return "contact" }

// Tier implements detect.Analyzer: no I/O.
func (*Analyzer[P]) Tier() detect.Tier { return detect.T0 }

// Applies implements detect.Analyzer: every message; a contact card has no text.
func (*Analyzer[P]) Applies(*envelope.Envelope, *P) bool { return true }

// Analyze implements detect.Analyzer. It emits at most one signal, naming every kind of contact
// the message offers.
func (*Analyzer[P]) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	found := Offers(e)
	if len(found) == 0 {
		return nil, nil
	}
	return []detect.Signal{{
		Code: CodeAdvertised, Weight: Weight,
		Evidence: "offers a way to reach someone outside the group: " + strings.Join(found, ", "),
	}}, nil
}

// Offers lists the kinds of contact e offers, in a fixed order.
func Offers(e *envelope.Envelope) []string {
	has := map[string]bool{}
	if len(e.Phones) > 0 || nationalNumber(e) {
		has[offerPhone] = true
	}
	if len(e.Contacts) > 0 {
		has[offerCard] = true
	}
	if len(e.Invites) > 0 {
		has[offerInvite] = true
	}
	for _, c := range e.Channels {
		if c.Source != "forwarded" {
			has[offerChannel] = true
		}
	}
	for _, l := range links.Collect(e) {
		switch {
		case l.Target == nil:
			if telegramHosts[strings.TrimPrefix(l.Host, "www.")] {
				has[offerTelegram] = true
			}
		case l.Target.Kind == urls.TargetGroupInvite:
			has[offerInvite] = true
		case l.Target.Kind == urls.TargetChannel:
			has[offerChannel] = true
		default: // wa.me/<number>, wa.me/message, wa.me/qr, api.whatsapp.com/send
			has[offerChat] = true
		}
	}
	if privateOrder(e) {
		has[offerPrivate] = true
	}
	var out []string
	for _, o := range offerOrder {
		if has[o] {
			out = append(out, o)
		}
	}
	return out
}

// nationalNumber reports whether a text surface has a phone number the normalizer couldn't
// read into E.164 (no default country yet, DESIGN.md §7.2 step 7): a run of 9 to 15 digits, with
// single spaces, dots or hyphens allowed between them, that is shaped like a phone number. Links
// and @mentions are cut out first: digits in a meeting link or a mention aren't a number to call.
func nationalNumber(e *envelope.Envelope) bool {
	for i := range e.Texts {
		t := &e.Texts[i]
		s := t.Norm
		if s == "" {
			s = t.Raw
		}
		s = mentionRe.ReplaceAllString(linkRe.ReplaceAllString(s, " "), " ")
		for _, loc := range digitRunRe.FindAllStringIndex(s, -1) {
			if standsAlone(s, loc[0], loc[1]) && phoneShaped(s[loc[0]:loc[1]]) {
				return true
			}
		}
	}
	return false
}

var (
	// linkRe matches links with a scheme or www, and bare domains with a path.
	linkRe = regexp.MustCompile(`(?i)(?:https?://|\bw{2,}\.)\S*|\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}/\S*`)
	// mentionRe matches WhatsApp mentions, which are written as @ and the member's number.
	mentionRe = regexp.MustCompile(`@\+?[0-9]+`)
	// digitRunRe matches 9 to 15 digits with at most one space, dot or hyphen between two digits.
	digitRunRe  = regexp.MustCompile(`\+?[0-9](?:[ .\-]?[0-9]){8,14}`)
	separatorRe = regexp.MustCompile(`[ .\-]`)
)

// standsAlone reports whether s[i:j] isn't glued to more digits or letters, so it isn't the middle
// of a longer number or a word.
func standsAlone(s string, i, j int) bool {
	before, _ := utf8.DecodeLastRuneInString(s[:i])
	after, _ := utf8.DecodeRuneInString(s[j:])
	glued := func(r rune) bool { return r != utf8.RuneError && (unicode.IsDigit(r) || unicode.IsLetter(r)) }
	return !glued(before) && !glued(after)
}

// phoneShaped reports whether a digit run looks like a phone number rather than a date, a time,
// a schedule, a list of years or an ID: it starts with + or a trunk 0 (0…, 00…), or it is 11 to 15
// digits written together (a country code without +). Groups that read like a date (a 19xx or
// 20xx year beside one- or two-digit groups) never count.
func phoneShaped(run string) bool {
	groups := separatorRe.Split(strings.TrimPrefix(run, "+"), -1)
	digits := strings.Join(groups, "")
	if looksLikeDate(groups) {
		return false
	}
	switch {
	case strings.HasPrefix(run, "+"), strings.HasPrefix(digits, "0"):
		return true
	default:
		return len(groups) == 1 && len(digits) >= 11
	}
}

func looksLikeDate(groups []string) bool {
	year, short := false, false
	for _, g := range groups {
		switch {
		case len(g) == 4 && (strings.HasPrefix(g, "19") || strings.HasPrefix(g, "20")):
			year = true
		case len(g) <= 2:
			short = true
		}
	}
	return year && short
}
