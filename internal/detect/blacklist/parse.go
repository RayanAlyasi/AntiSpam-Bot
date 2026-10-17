// Package blacklist is the domain side of the manual phone-number blacklist (DESIGN.md §9.8):
// parsing the numbers an admin types into E.164, hashing them for content matching, and the
// T0 analyzer that treats a blacklisted number in a vCard, a wa.me link or the message text as
// known spam. The number_blacklist table itself lives in internal/store.
package blacklist

import (
	"slices"
	"strings"
	"unicode"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/urls"
)

// maxNumberTokens is the most space-separated pieces one written number may have
// ("+966 55 123 4567" has four; "(055) 123 - 45 67" has five).
const maxNumberTokens = 6

// Parsed is the result of ParseArgs.
type Parsed struct {
	// Numbers are the valid numbers in E.164, in the order written, without duplicates.
	Numbers []string
	// Invalid are the written forms that look like numbers but aren't valid ones, in order.
	// They are reported back to the admin, never silently dropped (§9.8).
	Invalid []string
	// Reason is the free text after the numbers, trimmed; empty when none was given.
	Reason string
}

// ParseArgs parses the arguments of `.blacklist add|remove|check`: one or more numbers
// followed by an optional reason. Numbers may be written in any common form (§9.8):
// `+966 55 123 4567`, `00966551234567`, `0551234567` with the group's default region,
// Arabic-Indic or Persian digits, or a `wa.me/9665…` link; spaces inside a number are
// allowed, and several numbers may follow each other. The first piece that is not part of a
// number (a word) starts the reason. region is the group's ISO 3166-1 alpha-2 default
// country; without one, only internationally written numbers are accepted.
func ParseArgs(args, region string) Parsed {
	var p Parsed
	fields := strings.Fields(args)
	n := 0
	for n < len(fields) && numberish(fields[n]) {
		n++
	}
	// Bare punctuation right before the reason ("… 0551234567 - sells fake visas") belongs
	// to the reason.
	for n > 0 && !hasDigitOrLink(fields[n-1]) {
		n--
	}
	if n < len(fields) {
		// The reason keeps the admin's own spacing after the numbers.
		rest := args
		for _, f := range fields[:n] {
			rest = rest[strings.Index(rest, f)+len(f):]
		}
		p.Reason = strings.TrimSpace(rest)
	}
	var bad []string
	flush := func() {
		if len(bad) > 0 {
			p.Invalid = append(p.Invalid, strings.Join(bad, " "))
			bad = nil
		}
	}
	for i := 0; i < n; {
		if isLink(fields[i]) {
			if e164, ok := linkNumber(fields[i]); ok {
				flush()
				if !slices.Contains(p.Numbers, e164) {
					p.Numbers = append(p.Numbers, e164)
				}
			} else {
				bad = append(bad, fields[i])
			}
			i++
			continue
		}
		// Longest run of pieces, starting at i, that is exactly one valid number.
		took := 0
		for k := min(maxNumberTokens, n-i); k >= 1; k-- {
			if e164, err := normalize.ParsePhone(strings.Join(fields[i:i+k], " "), region); err == nil {
				flush()
				if !slices.Contains(p.Numbers, e164) {
					p.Numbers = append(p.Numbers, e164)
				}
				took = k
				break
			}
		}
		if took == 0 {
			bad = append(bad, fields[i])
			took = 1
		}
		i += took
	}
	flush()
	return p
}

// numberish reports whether a piece can be part of a written number: digits in any script,
// the punctuation numbers are written with, and invisible format characters, or a wa.me /
// api.whatsapp.com link.
func numberish(f string) bool {
	if isLink(f) {
		return true
	}
	for _, r := range f {
		if !unicode.IsDigit(r) && !strings.ContainsRune("+-().\u00a0", r) && !unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// linkNumber returns the number of a wa.me or api.whatsapp.com/send link.
func linkNumber(f string) (string, bool) {
	raw := f
	if !strings.Contains(strings.ToLower(raw), "://") {
		raw = "https://" + raw
	}
	u, err := urls.Canonicalize(raw)
	if err != nil || u.Target == nil || u.Target.Kind != urls.TargetPhone {
		return "", false
	}
	return u.Target.ID, true
}

func hasDigitOrLink(f string) bool {
	return isLink(f) || strings.ContainsFunc(f, unicode.IsDigit)
}

func isLink(f string) bool {
	low := strings.ToLower(f)
	for _, prefix := range []string{"https://", "http://"} {
		low = strings.TrimPrefix(low, prefix)
	}
	return strings.HasPrefix(low, "wa.me/") || strings.HasPrefix(low, "api.whatsapp.com/")
}
