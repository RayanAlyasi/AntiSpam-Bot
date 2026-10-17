package review

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const bullet = '•'

// phoneRun matches a run that looks like a phone number: an optional + or 00, then digits
// (ASCII, Arabic-Indic or Persian) with spaces, dots, dashes or parentheses between them.
var phoneRun = regexp.MustCompile(`\+?[0-9٠-٩۰-۹][0-9٠-٩۰-۹ \-.()+]{5,}[0-9٠-٩۰-۹]`)

// emailRun matches an email address.
var emailRun = regexp.MustCompile(`[\p{L}\p{N}._%+\-]+@[\p{L}\p{N}.\-]+\.[\p{L}]{2,}`)

// minPhoneDigits is the fewest digits a run needs to count as a phone number; shorter runs are
// prices, dates or codes and stay readable as evidence.
const minPhoneDigits = 8

// redact masks phone numbers and email addresses in text.
func redact(text string) string {
	text = emailRun.ReplaceAllStringFunc(text, func(m string) string {
		_, domain, _ := strings.Cut(m, "@")
		return "•••@" + domain
	})
	return phoneRun.ReplaceAllStringFunc(text, func(m string) string {
		d := digits(m)
		if len(d) < minPhoneDigits {
			return m
		}
		return maskNumber(d)
	})
}

// digits returns the ASCII digits of s, folding Arabic-Indic and Persian digits, and keeps a
// leading "+" (a "00" prefix becomes "+").
func digits(s string) string {
	var b strings.Builder
	plus := strings.HasPrefix(strings.TrimSpace(s), "+")
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		}
	}
	d := b.String()
	if !plus && strings.HasPrefix(d, "00") && len(d) > 10 {
		d, plus = d[2:], true
	}
	if plus {
		return "+" + d
	}
	return d
}

// maskNumber masks a number except the first national digit and the last two digits, grouped
// in threes from the end: "+966512345612" → "+966 5•• ••• •12". Numbers without a "+" are masked as one
// national number.
func maskNumber(number string) string {
	d := digits(number)
	cc := ""
	if strings.HasPrefix(d, "+") {
		d = d[1:]
		n := callingCodeLen(d)
		if n < len(d) {
			cc, d = "+"+d[:n]+" ", d[n:]
		}
	}
	if len(d) <= 2 {
		return cc + strings.Repeat(string(bullet), len(d))
	}
	// Groups of three counted from the end, so the visible last two digits stay together.
	var b strings.Builder
	b.WriteString(cc)
	for i, r := range d {
		if i > 0 && (len(d)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		if (i == 0 && len(d) > 4) || i >= len(d)-2 {
			b.WriteRune(r)
		} else {
			b.WriteRune(bullet)
		}
	}
	return b.String()
}

// callingCodeLen returns the length of the country calling code that starts d (ITU E.164 zones).
func callingCodeLen(d string) int {
	if d == "" {
		return 0
	}
	if d[0] == '1' || d[0] == '7' {
		return 1
	}
	if len(d) < 2 {
		return len(d)
	}
	two := d[:2]
	switch d[0] {
	case '2':
		if two == "20" || two == "27" {
			return 2
		}
	case '3':
		if strings.Contains("30 31 32 33 34 36 39", two) {
			return 2
		}
	case '4':
		if two != "42" {
			return 2
		}
	case '5':
		if two != "50" && two != "59" {
			return 2
		}
	case '6':
		if two <= "66" {
			return 2
		}
	case '8':
		if strings.Contains("81 82 84 86", two) {
			return 2
		}
	case '9':
		if two <= "95" || two == "98" {
			return 2
		}
	}
	return min(3, len(d))
}

// truncate shortens text to at most n runes, ending with "…" when it cut something, and
// collapses whitespace so a review line stays one line.
func truncate(text string, n int) string {
	text = strings.Join(strings.FieldsFunc(text, unicode.IsSpace), " ")
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	r := []rune(text)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
