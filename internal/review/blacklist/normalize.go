package blacklist

import (
	"fmt"
	"net/url"
	"strings"
)

// country is a default-country rule for local numbers.
type country struct {
	code     string // calling code
	nsnLen   []int  // valid national significant number lengths
	trunk0   bool   // local numbers start with a 0 that is dropped
	firstNSN string // allowed first digits of the national number ("" = any)
}

// countries covers the regions the bot's first deployments use; the libphonenumber-backed
// normalizer (F-123) replaces this table when it is wired.
var countries = map[string]country{
	"SA": {code: "966", nsnLen: []int{9}, trunk0: true, firstNSN: "5"},
	"AE": {code: "971", nsnLen: []int{9}, trunk0: true, firstNSN: "5"},
	"EG": {code: "20", nsnLen: []int{10}, trunk0: true, firstNSN: "1"},
	"KW": {code: "965", nsnLen: []int{8}, firstNSN: "4569"},
	"QA": {code: "974", nsnLen: []int{8}, firstNSN: "3567"},
	"BH": {code: "973", nsnLen: []int{8}, firstNSN: "3"},
	"OM": {code: "968", nsnLen: []int{8}, firstNSN: "79"},
	"JO": {code: "962", nsnLen: []int{9}, trunk0: true, firstNSN: "7"},
	"GB": {code: "44", nsnLen: []int{10}, trunk0: true, firstNSN: "7"},
	"US": {code: "1", nsnLen: []int{10}},
}

// normalizeNumber implements SimpleNormalizer.Normalize.
func normalizeNumber(input, defaultCountry string) (string, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", fmt.Errorf("empty: %w", ErrInvalid)
	}
	raw = fromLink(raw)
	plus := strings.HasPrefix(raw, "+")
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')' || r == '+' || r == ' ' ||
			r == '\u200e' || r == '\u200f' || r == '\u202a' || r == '\u202c':
		default:
			return "", fmt.Errorf("%q: unexpected %q: %w", input, r, ErrInvalid)
		}
	}
	d := b.String()
	switch {
	case plus:
	case strings.HasPrefix(d, "00"):
		d = d[2:]
	default:
		c, ok := countries[strings.ToUpper(defaultCountry)]
		switch {
		case ok && strings.HasPrefix(d, c.code) && validNSN(c, d[len(c.code):]):
			// Already international, written without the "+".
		case ok:
			nsn := d
			if c.trunk0 {
				nsn = strings.TrimPrefix(nsn, "0")
			}
			if c.code == "1" && len(nsn) == 11 {
				nsn = strings.TrimPrefix(nsn, "1")
			}
			if !validNSN(c, nsn) {
				return "", fmt.Errorf("%q is not a valid %s number: %w", input, strings.ToUpper(defaultCountry), ErrInvalid)
			}
			d = c.code + nsn
		case len(d) >= 11 && d[0] != '0':
			// No default country: only a full international number is accepted.
		default:
			return "", fmt.Errorf("%q: needs a country code: %w", input, ErrInvalid)
		}
	}
	if len(d) < 8 || len(d) > 15 || d[0] == '0' {
		return "", fmt.Errorf("%q: %w", input, ErrInvalid)
	}
	for _, c := range countries {
		if strings.HasPrefix(d, c.code) && (c.code != "1" || strings.HasPrefix(d, "1")) {
			nsn := d[len(c.code):]
			if len(c.code) >= 2 && !validNSN(c, nsn) && strings.HasPrefix(nsn, "0") {
				// A trunk 0 kept after the country code ("+966 05…") is a common typo.
				if fixed := strings.TrimPrefix(nsn, "0"); validNSN(c, fixed) {
					d = c.code + fixed
				}
			}
		}
	}
	return "+" + d, nil
}

func validNSN(c country, nsn string) bool {
	ok := false
	for _, n := range c.nsnLen {
		ok = ok || len(nsn) == n
	}
	if !ok {
		return false
	}
	return c.firstNSN == "" || strings.ContainsRune(c.firstNSN, rune(nsn[0]))
}

// fromLink extracts the number from a wa.me or api.whatsapp.com link.
func fromLink(s string) string {
	l := strings.ToLower(s)
	if !strings.Contains(l, "wa.me/") && !strings.Contains(l, "whatsapp.com/") {
		return s
	}
	if !strings.Contains(l, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	if p := u.Query().Get("phone"); p != "" {
		return "+" + strings.TrimPrefix(p, "+")
	}
	return "+" + strings.Trim(u.Path, "/+")
}
