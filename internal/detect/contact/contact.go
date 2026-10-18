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
)

var offerOrder = []string{offerPhone, offerCard, offerChat, offerInvite, offerChannel, offerTelegram}

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
	var out []string
	for _, o := range offerOrder {
		if has[o] {
			out = append(out, o)
		}
	}
	return out
}

// nationalNumber reports whether a text surface has a phone number the normalizer couldn't
// read into E.164 (no default country yet, DESIGN.md §7.2 step 7): 9 to 15 digits, with single
// spaces, dots or hyphens allowed between them, shaped like a phone number, alone or beside other
// numbers and dates in a longer run of digits (phoneIn). Links
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
			if phoneIn(s, loc[0], loc[1]) {
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
	// digitRunRe matches digits with at most one space, dot or hyphen between two digits.
	digitRunRe  = regexp.MustCompile(`\+?[0-9](?:[ .\-]?[0-9])*`)
	separatorRe = regexp.MustCompile(`[ .\-]`)
)

// Phone numbers have 9 to 15 digits.
const minDigits, maxDigits = 9, 15

// phoneIn reports whether the digit run s[i:j] is a phone number, or splits at its spaces into
// phone numbers, dates and short counts with at least one phone number among them: two numbers
// one space apart, or a number beside a date, make a run that is too long or reads like a date as
// a whole. Every part must be explained, so a 0-led group inside a spaced IBAN or card number
// ("SA03 8000 0000 6080 1016 7519") isn't read as a number on its own.
func phoneIn(s string, i, j int) bool {
	if isPhone(s, i, j) {
		return true
	}
	var parts [][2]int // offsets of the space-separated parts of s[i:j]
	for k := i; k < j; {
		end := strings.IndexByte(s[k:j], ' ')
		if end < 0 {
			end = j - k
		}
		parts = append(parts, [2]int{k, k + end})
		k += end + 1
	}
	// read[k] says how parts[:k] split: not at all, into dates and counts only, or with a phone.
	const (
		unread = iota
		noPhone
		withPhone
	)
	read := make([]int, len(parts)+1)
	read[0] = noPhone
	for a := range parts {
		if read[a] == unread {
			continue
		}
		if p := parts[a]; dateOrCount(s[p[0]:p[1]]) && (a > 0 || standsAlone(s, p[0], p[1])) {
			read[a+1] = max(read[a+1], read[a])
		}
		for b := a; b < len(parts) && digitCount(s[parts[a][0]:parts[b][1]]) <= maxDigits; b++ {
			if isPhone(s, parts[a][0], parts[b][1]) {
				read[b+1] = withPhone
			}
		}
	}
	return read[len(parts)] == withPhone
}

// dateOrCount reports whether a part of a digit run is a date, a time, a year or a short count:
// every group has one or two digits or is a 19xx or 20xx year.
func dateOrCount(part string) bool {
	for _, g := range separatorRe.Split(part, -1) {
		if len(g) > 2 && !isYear(g) {
			return false
		}
	}
	return true
}

func isYear(g string) bool {
	return len(g) == 4 && (strings.HasPrefix(g, "19") || strings.HasPrefix(g, "20"))
}

// isPhone reports whether s[i:j] is a phone number on its own.
func isPhone(s string, i, j int) bool {
	run := s[i:j]
	n := digitCount(run)
	return n >= minDigits && n <= maxDigits && standsAlone(s, i, j) && phoneShaped(run)
}

func digitCount(run string) int {
	n := 0
	for _, r := range run {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// standsAlone reports whether s[i:j] isn't glued to more digits or letters, so it isn't the middle
// of a longer number or a word. An Arabic word written straight onto a number with a trunk
// prefix ("واتساب0551234567") still counts: Arabic chat often drops that space, while a Latin
// letter before digits is a code ("A0551234567").
func standsAlone(s string, i, j int) bool {
	before, _ := utf8.DecodeLastRuneInString(s[:i])
	after, _ := utf8.DecodeRuneInString(s[j:])
	glued := func(r rune) bool { return r != utf8.RuneError && (unicode.IsDigit(r) || unicode.IsLetter(r)) }
	if glued(after) || unicode.IsDigit(before) {
		return false
	}
	if before == utf8.RuneError || !unicode.IsLetter(before) {
		return true
	}
	return unicode.Is(unicode.Arabic, before) && hasTrunkPrefix(s[i:j])
}

// hasTrunkPrefix reports whether run starts with + or with a trunk 0 (0…, 00…) inside a group of
// at least two digits: a lone 0 is a price or a count, not a prefix.
func hasTrunkPrefix(run string) bool {
	if strings.HasPrefix(run, "+") {
		return true
	}
	first := separatorRe.Split(run, 2)[0]
	return strings.HasPrefix(first, "0") && len(first) >= 2
}

// phoneShaped reports whether a digit run looks like a phone number rather than a date, a time,
// a price list, a schedule, a list of years or an ID: it starts with + or a trunk 0, or it is 11
// to 15 digits written together (a country code without +). Groups that read like a date or a
// time list never count: a 19xx or 20xx year beside one- or two-digit groups, or three or more
// one- or two-digit groups ("05-12-26 10.30", "08.00 09.30 11.00", "0.50 0.75 1.00").
func phoneShaped(run string) bool {
	groups := separatorRe.Split(strings.TrimPrefix(run, "+"), -1)
	if looksLikeDate(groups) {
		return false
	}
	return hasTrunkPrefix(run) || len(groups) == 1 && len(groups[0]) >= 11
}

func looksLikeDate(groups []string) bool {
	year, short := false, 0
	for _, g := range groups {
		switch {
		case isYear(g):
			year = true
		case len(g) <= 2:
			short++
		}
	}
	return year && short > 0 || short >= 3
}
