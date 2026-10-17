package logging

import "strings"

// Masked is what MaskPhone and MaskJID return for input they can't read as a phone number or
// JID, so that it is never echoed into a log.
const Masked = "•••"

const (
	// minNational is the shortest national number that is masked rather than replaced by
	// Masked. Shorter numbers would show most of their digits.
	minNational = 6
	// maxDigits is the E.164 limit for a full international number.
	maxDigits = 15
)

// Phone is a phone number that masks itself when logged or formatted:
//
//	logger.Info().Stringer("phone", logging.Phone(number)).Msg("...")
type Phone string

// String returns the number masked by MaskPhone.
func (p Phone) String() string { return MaskPhone(string(p)) }

// MarshalText returns the masked number, so JSON encoding and zerolog's Interface mask too.
func (p Phone) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

// JID is a WhatsApp JID that masks its phone number when logged or formatted:
//
//	logger.Info().Stringer("jid", logging.JID(jid.String())).Msg("...")
type JID string

// String returns the JID masked by MaskJID.
func (j JID) String() string { return MaskJID(string(j)) }

// MarshalText returns the masked JID, so JSON encoding and zerolog's Interface mask too.
func (j JID) MarshalText() ([]byte, error) { return []byte(j.String()), nil }

// MaskPhone renders an international phone number with only its country calling code and the
// first and last two digits of the national number visible, e.g. "+966 5•• ••• •12" for
// +966512345612. The national number is grouped in threes from the left.
//
// The number may start with "+" or "00", or with the country code itself, and may contain
// spaces, dashes, dots and parentheses. Anything else, a national number shorter than six
// digits, or more than fifteen digits in all, gives Masked. An empty string stays empty.
func MaskPhone(s string) string {
	if s == "" {
		return ""
	}
	digits, ok := phoneDigits(s)
	if !ok {
		return Masked
	}
	ccLen := countryCodeLen(digits)
	if len(digits) < ccLen+minNational {
		return Masked
	}
	national := digits[ccLen:]

	var b strings.Builder
	b.Grow(1 + ccLen + len(national)*4)
	b.WriteByte('+')
	b.WriteString(digits[:ccLen])
	for i := range len(national) {
		if i%3 == 0 {
			b.WriteByte(' ')
		}
		if i == 0 || i >= len(national)-2 {
			b.WriteByte(national[i])
		} else {
			b.WriteRune('•')
		}
	}
	return b.String()
}

// phoneDigits returns the digits of s after the international prefix ("+" or "00"), or false
// if s isn't written like an international phone number.
func phoneDigits(s string) (string, bool) {
	s = strings.TrimSpace(s)
	plus := strings.HasPrefix(s, "+")
	if plus {
		s = s[1:]
	}
	digits := make([]byte, 0, len(s))
	for i := range len(s) {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits = append(digits, c)
		case c == ' ' || c == '-' || c == '.' || c == '(' || c == ')':
		default:
			return "", false
		}
	}
	if !plus && len(digits) >= 2 && digits[0] == '0' && digits[1] == '0' {
		digits = digits[2:]
	}
	// No country calling code starts with 0; a leading 0 is a national trunk prefix, and
	// without the country code the number can't be masked reliably.
	if len(digits) == 0 || digits[0] == '0' || len(digits) > maxDigits {
		return "", false
	}
	return string(digits), true
}

// countryCodeLen returns the length of the ITU country calling code at the start of digits:
// 1 for zones 1 and 7, 2 for the codes assigned two digits, 3 for everything else.
func countryCodeLen(digits string) int {
	if digits[0] == '1' || digits[0] == '7' {
		return 1
	}
	if len(digits) < 2 {
		return 3
	}
	switch digits[:2] {
	case "20", "27",
		"30", "31", "32", "33", "34", "36", "39",
		"40", "41", "43", "44", "45", "46", "47", "48", "49",
		"51", "52", "53", "54", "55", "56", "57", "58",
		"60", "61", "62", "63", "64", "65", "66",
		"81", "82", "84", "86",
		"90", "91", "92", "93", "94", "95", "98":
		return 2
	}
	return 3
}

// MaskJID masks the phone number in a WhatsApp JID ("966512345612@s.whatsapp.net" becomes
// "+966 5•• ••• •12@s.whatsapp.net"), keeping any agent and device suffix (".1:12").
//
// LIDs, new-style group JIDs, newsletters, broadcasts and bots aren't phone numbers and stay
// readable. Old-style group JIDs ("<creator phone>-<created>@g.us") have the creator's phone
// masked. A string without "@" is masked as a phone number, and a JID on an unknown server, or
// one that doesn't parse, gives Masked.
func MaskJID(s string) string {
	user, server, ok := strings.Cut(s, "@")
	if !ok {
		return MaskPhone(s)
	}
	switch server {
	case "s.whatsapp.net", "c.us", "hosted":
		number, suffix := user, ""
		if i := strings.IndexAny(user, ".:"); i >= 0 {
			number, suffix = user[:i], user[i:]
		}
		if !validDeviceSuffix(suffix) {
			return Masked + "@" + server
		}
		return MaskPhone(number) + suffix + "@" + server
	case "g.us":
		if creator, created, ok := strings.Cut(user, "-"); ok {
			masked := MaskPhone(creator)
			if masked == Masked || masked == "" || len(created) > 10 || !allDigits(created) {
				return Masked + "@" + server
			}
			return masked + "-" + created + "@" + server
		}
		fallthrough
	case "lid", "hosted.lid", "newsletter", "broadcast", "bot":
		if !opaqueID(user) {
			return Masked + "@" + server
		}
		return s
	}
	return Masked
}

// validDeviceSuffix reports whether s is empty or an agent and/or device suffix of a JID:
// ".<agent>", ":<device>" or ".<agent>:<device>", each one to three digits.
func validDeviceSuffix(s string) bool {
	if rest, ok := strings.CutPrefix(s, "."); ok {
		agent, device, hasDevice := strings.Cut(rest, ":")
		if !shortNumber(agent) {
			return false
		}
		return !hasDevice || shortNumber(device)
	}
	if rest, ok := strings.CutPrefix(s, ":"); ok {
		return shortNumber(rest)
	}
	return s == ""
}

func shortNumber(s string) bool { return len(s) >= 1 && len(s) <= 3 && allDigits(s) }

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// opaqueID reports whether s looks like the user part of a non-phone JID: letters, digits and
// ".:_-". Anything else (spaces, "+") suggests free text that might hold a phone number.
func opaqueID(s string) bool {
	for i := range len(s) {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c == '.' || c == ':' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return s != ""
}
