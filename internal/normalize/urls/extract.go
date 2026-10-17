// Package urls finds links in text and canonicalizes them for matching
// (DESIGN.md §6, URL analyzer steps 1–3). It works on plain strings, so
// callers apply it to every text surface of a message.
package urls

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

const (
	// MaxURLs is the most URLs Extract returns for one text.
	MaxURLs = 64
	// MaxURLBytes is the longest URL Extract returns; longer ones are cut.
	MaxURLBytes = 2048
)

// spacedTLDs are the suffixes for which a dot written with spaces around it
// ("example . com") still joins a domain. Prose like "done . now" doesn't.
var spacedTLDs = map[string]bool{
	"com": true, "net": true, "org": true, "info": true, "biz": true, "io": true,
	"co": true, "me": true, "ly": true, "xyz": true, "top": true, "site": true,
	"online": true, "shop": true, "link": true, "app": true, "sa": true, "ae": true,
	"ru": true, "cc": true, "tv": true, "vip": true, "club": true, "live": true, "store": true,
}

// wordTLDs are the spacedTLDs that are also ordinary words.
var wordTLDs = map[string]bool{
	"me": true, "top": true, "site": true, "online": true, "shop": true, "link": true,
	"app": true, "live": true, "store": true, "club": true, "info": true, "co": true, "vip": true,
}

// wordFollows reports whether s starts with spaces and then a letter.
func wordFollows(s string) bool {
	t := strings.TrimLeft(s, " ")
	if len(t) == len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(t)
	return unicode.IsLetter(r)
}

// Extract returns the http(s) URLs in text in order of appearance, without
// duplicates. It undoes common defanging (hxxp, http[:]//, [.], (dot),
// spaced and fullwidth dots), ignores invisible characters, and adds
// http:// to links written without a scheme (www.example.com, t.me/x,
// example.com/path). Path, query and fragment are kept as written.
func Extract(text string) []string {
	s := clean(text)
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(s) && len(out) < MaxURLs; {
		u, end := urlAt(s, i)
		if end <= i {
			_, w := utf8.DecodeRuneInString(s[i:])
			i += w
			continue
		}
		i = end
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// clean folds the text (NFKC, invisibles removed), undoes defanged schemes
// and dots, and joins spaced dots in front of a common suffix.
func clean(text string) string {
	folded, _ := normalize.Fold(text)
	return joinSpacedDots(undefang(folded))
}

// defangRules rewrite a suffix of the output as soon as it appears, so
// nested forms like [[.].] reduce fully in one linear pass.
var defangRules = []struct{ from, to string }{
	{"[.]", "."},
	{"(.)", "."},
	{"{.}", "."},
	{"[dot]", "."},
	{"(dot)", "."},
	{"hxxp://", "http://"},
	{"hxxps://", "https://"},
	{"http[:]//", "http://"},
	{"https[:]//", "https://"},
	{"hxxp[:]//", "http://"},
	{"hxxps[:]//", "https://"},
}

func undefang(s string) string {
	if !strings.ContainsAny(s, "[({") && !containsFold(s, "hxxp") {
		return ideographicDots(s)
	}
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = utf8.AppendRune(b, r)
		for reduced := true; reduced; {
			reduced = false
			for _, rule := range defangRules {
				if n := len(rule.from); len(b) >= n && strings.EqualFold(string(b[len(b)-n:]), rule.from) {
					b = append(b[:len(b)-n], rule.to...)
					reduced = true
				}
			}
		}
	}
	return ideographicDots(string(b))
}

// ideographicDots turns U+3002 (what NFKC makes of the halfwidth ｡ too) into
// a dot when it sits between ASCII letters or digits, as in example。com.
// Between CJK words it stays a full stop.
func ideographicDots(s string) string {
	const dot = "。"
	if !strings.Contains(s, dot) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, dot)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := i + len(dot)
		if i > 0 && j < len(s) && isASCIIAlnum(s[i-1]) && isASCIIAlnum(s[j]) {
			b.WriteString(s[:i])
			b.WriteByte('.')
		} else {
			b.WriteString(s[:j])
		}
		s = s[j:]
	}
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), sub)
}

// joinSpacedDots turns "example . com" into "example.com" when the domain
// before it starts with www or follows a scheme, or when there is a space
// before the dot and the dotted name ends in a common suffix written in
// lower case (or followed by a path). That keeps sentence breaks such as
// "the best. Top quality" out.
func joinSpacedDots(s string) string {
	if !strings.Contains(s, " .") && !strings.Contains(s, ". ") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		j, before, ok := spacedDot(s, i)
		if !ok {
			b.WriteByte(s[i])
			i++
			continue
		}
		prev, _ := utf8.DecodeLastRuneInString(b.String())
		if isLabelRune(prev) && (chainHasPrefix(b.String()) || before > 0 && endsInSpacedTLD(s, j)) {
			b.WriteByte('.')
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// endsInSpacedTLD follows the dotted name starting at s[j] (plain or spaced
// dots, at most a domain's length) and reports whether its last label is a
// common suffix in lower case, or any case when a path follows.
func endsInSpacedTLD(s string, j int) bool {
	start := j
	for {
		end := labelEnd(s, j)
		if end == j || end-start > 253 {
			return false
		}
		next := end
		if next < len(s) && s[next] == '.' && next+1 < len(s) {
			if r, _ := utf8.DecodeRuneInString(s[next+1:]); isLabelRune(r) {
				j = next + 1
				continue
			}
		}
		if k, _, ok := spacedDot(s, next); ok {
			j = k
			continue
		}
		label := s[j:end]
		if !spacedTLDs[strings.ToLower(label)] {
			return false
		}
		if end < len(s) && strings.ContainsRune("/?#", rune(s[end])) {
			return true
		}
		if label != strings.ToLower(label) {
			return false
		}
		// Suffixes that are also English words (me, shop, live) join only
		// when no word follows, so "ok . me too" stays prose.
		return !wordTLDs[label] || !wordFollows(s[end:])
	}
}

// spacedDot reports whether s[i:] starts with spaces and a dot and spaces
// (at least one space in all) before a label, returning the label start and
// the number of spaces before the dot.
func spacedDot(s string, i int) (int, int, bool) {
	j := i
	for j < len(s) && s[j] == ' ' {
		j++
	}
	spaces := j - i
	before := spaces
	if j >= len(s) || s[j] != '.' || j-i > 3 {
		return 0, 0, false
	}
	j++
	k := j
	for k < len(s) && s[k] == ' ' {
		k++
	}
	spaces += k - j
	if spaces == 0 || spaces > 4 || k >= len(s) {
		return 0, 0, false
	}
	r, _ := utf8.DecodeRuneInString(s[k:])
	return k, before, isLabelRune(r)
}

// chainHasPrefix reports whether the dotted name ending b starts with www.
// or follows a scheme. Names longer than a domain can be are not looked at.
func chainHasPrefix(b string) bool {
	start := len(b)
	for start > 0 {
		if len(b)-start > 253 {
			return false
		}
		r, w := utf8.DecodeLastRuneInString(b[:start])
		if !isLabelRune(r) && r != '.' {
			break
		}
		start -= w
	}
	chain := strings.ToLower(b[start:])
	return strings.HasPrefix(chain, "www") || strings.HasSuffix(b[:start], "://")
}

func labelEnd(s string, i int) int {
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !isLabelRune(r) {
			break
		}
		i += w
	}
	return i
}

// isLabelRune reports characters that may appear in a domain label.
func isLabelRune(r rune) bool {
	return r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// urlAt returns the URL starting at s[i] and the offset after it. An end of
// i means nothing starts here; a later end with an empty URL means skip to
// it (a word or dotted name that isn't a link).
func urlAt(s string, i int) (string, int) {
	if i > 0 {
		prev, _ := utf8.DecodeLastRuneInString(s[:i])
		afterDots := prev == '.' && i > 1 && s[i-2] == '.' // an ellipsis, as in "now...t.me/x"
		if isLabelRune(prev) || prev == '.' && !afterDots || prev == '@' {
			return "", i
		}
	}
	rest := s[i:]
	switch {
	case hasPrefixFold(rest, "http://"), hasPrefixFold(rest, "https://"):
		scheme := strings.ToLower(rest[:strings.Index(rest, ":")])
		end := i + len(scheme) + 3 + urlLen(s[i+len(scheme)+3:])
		return finish(scheme+"://"+s[i+len(scheme)+3:end], false), end
	case hasPrefixFold(rest, "www."):
		end := i + urlLen(rest)
		return finish("http://"+s[i:end], true), end
	}
	r, _ := utf8.DecodeRuneInString(rest)
	if !isLabelRune(r) {
		return "", i
	}
	hostEnd := i
	var prev rune
	for hostEnd < len(s) {
		r, w := utf8.DecodeRuneInString(s[hostEnd:])
		if !isLabelRune(r) && r != '.' || entersArabic(prev, r) || r == '.' && prev == '.' {
			break
		}
		prev = r
		hostEnd += w
	}
	host := strings.TrimRight(s[i:hostEnd], ".")
	if next, _ := utf8.DecodeRuneInString(s[hostEnd:]); next == '@' || !isBareDomain(host) {
		return "", hostEnd
	}
	end := i + len(host)
	next, _ := utf8.DecodeRuneInString(s[end:])
	hasPath := next == '/' || next == ':' || next == '?' || next == '#'
	if !hasPath && titleCaseWordTLD(host) && wordFollows(s[end:]) {
		// "work.Me too": a sentence that lost its space, not a link.
		return "", hostEnd
	}
	if hasPath {
		end += urlLen(s[end:])
	}
	return finish("http://"+s[i:end], false), end
}

// titleCaseWordTLD reports a last label that is an English word written
// capitalized (Me, Shop), which in prose starts a sentence rather than ends
// a domain. Other suffixes (Evil.Com) are domains in any case.
func titleCaseWordTLD(host string) bool {
	tld := host[strings.LastIndex(host, ".")+1:]
	r, w := utf8.DecodeRuneInString(tld)
	return len(tld) > w && unicode.IsUpper(r) && tld[w:] == strings.ToLower(tld[w:]) &&
		wordTLDs[strings.ToLower(tld)]
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// urlLen returns how many bytes of s belong to a URL: URL characters up to
// whitespace, a disallowed character or a switch into Arabic script, with
// trailing punctuation and unbalanced closing brackets left out.
func urlLen(s string) int {
	end := 0
	var prev rune
	for end < len(s) {
		r, w := utf8.DecodeRuneInString(s[end:])
		if !isURLRune(r) || entersArabic(prev, r) {
			break
		}
		prev = r
		end += w
	}
	return trimTrailing(s[:end])
}

// entersArabic reports a switch from a Latin-or-other letter or digit
// straight into Arabic script, where a link written in running Arabic text
// ends.
func entersArabic(prev, r rune) bool {
	return unicode.Is(unicode.Arabic, r) && (unicode.IsLetter(prev) || unicode.IsDigit(prev)) && !unicode.Is(unicode.Arabic, prev)
}

func isURLRune(r rune) bool {
	if r < utf8.RuneSelf {
		return r > ' ' && strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~:/?#[]@!$&'()*+,;=%", r)
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// trimTrailing drops sentence punctuation and unbalanced closing brackets
// from the end of a URL and returns its new length.
func trimTrailing(u string) int {
	open := strings.Count(u, "(") - strings.Count(u, ")")
	square := strings.Count(u, "[") - strings.Count(u, "]")
	for len(u) > 0 {
		r, w := utf8.DecodeLastRuneInString(u)
		switch {
		case strings.ContainsRune(".,!?;:'\"،؟", r):
		case r == ')' && open < 0:
			open++
		case r == ']' && square < 0:
			square++
		default:
			return len(u)
		}
		u = u[:len(u)-w]
	}
	return 0
}

// finish bounds a candidate and checks it parses as an http(s) URL with a
// host. www hosts need at least one more dot.
func finish(u string, www bool) string {
	if len(u) > MaxURLBytes {
		cut := MaxURLBytes
		for cut > 0 && !utf8.RuneStart(u[cut]) {
			cut--
		}
		u = u[:trimTrailing(u[:cut])]
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" || p.Hostname() == "" || p.Opaque != "" {
		return ""
	}
	if www && strings.Count(p.Hostname(), ".") < 2 {
		return ""
	}
	if !plausibleHost(p.Hostname()) {
		return ""
	}
	return u
}

func plausibleHost(h string) bool {
	for _, r := range h {
		if !isLabelRune(r) && r != '.' && r != ':' {
			return false
		}
	}
	return true
}

// isBareDomain reports whether a name without a scheme is a domain: two or
// more labels ending in an ICANN public suffix, with a registrable part.
// That keeps decimals, versions, IPs, abbreviations and file names out.
func isBareDomain(host string) bool {
	if !strings.Contains(host, ".") || strings.Contains(host, "..") || len(host) > 253 {
		return false
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return false
	}
	ascii = strings.ToLower(ascii)
	tld := ascii[strings.LastIndex(ascii, ".")+1:]
	if tld == "" || strings.Trim(tld, "0123456789") == "" {
		return false
	}
	suffix, icann := publicsuffix.PublicSuffix(ascii)
	if !icann || suffix == ascii {
		return false
	}
	_, err = publicsuffix.EffectiveTLDPlusOne(ascii)
	return err == nil
}
