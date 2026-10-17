// Package redact makes message text safe to keep or show (DESIGN.md §7.5,
// §9.5, §7.4): it masks third-party phone numbers, emails, mentions and
// names, keeps a spam message's own advertised numbers only as keyed hashes,
// and truncates. The corpus, review cards and the AI context all use it.
package redact

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

// Options says what to mask and how much to keep.
type Options struct {
	// Region is the group's default country, for reading national numbers.
	Region string
	// Key keys the hashes of advertised numbers. When empty, advertised
	// numbers are masked like any other.
	Key []byte
	// Advertised are the E.164 numbers the spam itself advertises. They are
	// kept as [phone:<id>] so the same number can be matched later.
	Advertised []string
	// Names are people's names to mask (whole words, case-insensitive).
	// Names shorter than two runes are ignored.
	Names []string
	// MaxRunes truncates the result to at most this many runes, the trailing
	// … included. Zero means no limit.
	MaxRunes int
}

// Placeholders that replace masked text.
const (
	PhoneToken   = "[phone]"
	EmailToken   = "[email]"
	MentionToken = "[mention]"
	NameToken    = "[name]"
	ellipsis     = "…"
	hashIDLen    = 12
	// minMaskedDigits is the shortest digit run masked even when it isn't a
	// valid number for the region.
	minMaskedDigits = 9
)

var (
	placeholderRe = regexp.MustCompile(`\[(?:phone(?::[0-9a-f]{12})?|email|mention|name)\]`)
	mentionRe     = regexp.MustCompile(`@[0-9]{6,}`)
	longDigitsRe  = regexp.MustCompile(`[0-9](?:[ \-.]?[0-9]){8,}`)
)

// PhoneHash returns the lowercase hex HMAC-SHA256 of an E.164 number, so a
// store can match numbers without keeping them.
func PhoneHash(key []byte, e164 string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(e164))
	return hex.EncodeToString(m.Sum(nil))
}

type span struct {
	start, end int // byte offsets into the input
	token      string
}

// Text returns s with phone numbers, emails, mentions and the given names
// replaced by placeholders, truncated to o.MaxRunes. Invisible and
// fullwidth characters inside any of them don't stop the masking. Prices,
// dates, short numbers and URLs are kept. Redacting twice changes nothing.
func Text(s string, o Options) string {
	v := newView(s)
	var keep, found []span
	for _, p := range v.spans(placeholderRe, "") {
		// Text shaped like a hash placeholder but holding a phone number
		// ("[phone:966551234567]") is masked, not trusted.
		if hidesPhone(s[p.start:p.end], o.Region) {
			found = append(found, span{p.start, p.end, PhoneToken})
			continue
		}
		keep = append(keep, p)
	}

	advertised := map[string]bool{}
	for _, e := range o.Advertised {
		advertised[e] = true
	}
	for _, p := range normalize.FindPhones(s, o.Region) {
		tok := PhoneToken
		if len(o.Key) > 0 && advertised[p.E164] {
			tok = "[phone:" + PhoneHash(o.Key, p.E164)[:hashIDLen] + "]"
		}
		found = append(found, span{p.Start, p.End, tok})
	}
	if strings.Contains(v.text, "@") {
		found = append(found, v.emailSpans()...)
		found = append(found, v.spans(mentionRe, MentionToken)...)
	}
	if strings.ContainsAny(v.text, "0123456789") {
		found = append(found, v.spans(longDigitsRe, PhoneToken)...)
	}
	for _, r := range normalize.SpelledDigitRuns(s, minMaskedDigits) {
		found = append(found, span{r[0], r[1], PhoneToken})
	}
	for _, re := range namesRegexps(o.Names) {
		found = append(found, v.wordSpans(re, NameToken)...)
	}

	out, marks := apply(s, merge(found, keep))
	return truncate(out, marks, o.MaxRunes)
}

// hidesPhone reports whether a [phone:<id>] placeholder's id is all digits
// and reads as a phone number. A real hash id is rarely all digits, and
// almost never also a valid number.
func hidesPhone(placeholder, region string) bool {
	id, ok := strings.CutPrefix(placeholder, "[phone:")
	if !ok {
		return false
	}
	id = strings.TrimSuffix(id, "]")
	if strings.Trim(id, "0123456789") != "" {
		return false
	}
	return len(normalize.FindPhones(id, region)) > 0 || len(normalize.FindPhones("+"+id, "")) > 0
}

// merge sorts spans, drops any that overlap an existing placeholder, and
// joins overlapping ones so no part of a masked item is left showing. The
// first span of a joined group gives the placeholder.
func merge(found, keep []span) []span {
	slices.SortFunc(found, func(a, b span) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(b.end, a.end))
	})
	var out []span
	for _, f := range found {
		if overlapsAny(f, keep) {
			continue
		}
		if n := len(out); n > 0 && f.start < out[n-1].end {
			out[n-1].end = max(out[n-1].end, f.end)
			continue
		}
		out = append(out, f)
	}
	return out
}

func overlapsAny(f span, keep []span) bool {
	i, _ := slices.BinarySearchFunc(keep, f.start, func(k span, start int) int {
		return cmp.Compare(k.end, start+1)
	})
	return i < len(keep) && keep[i].start < f.end
}

// apply writes s with the spans replaced, turning invalid UTF-8 into U+FFFD.
// It returns the result and the byte ranges of every placeholder in it.
func apply(s string, spans []span) (string, [][2]int) {
	var b strings.Builder
	b.Grow(len(s))
	var marks [][2]int
	write := func(t string) {
		for _, r := range t {
			b.WriteRune(r)
		}
	}
	at := 0
	for _, sp := range spans {
		write(s[at:sp.start])
		marks = append(marks, [2]int{b.Len(), b.Len() + len(sp.token)})
		b.WriteString(sp.token)
		at = sp.end
	}
	write(s[at:])
	out := b.String()
	for _, m := range placeholderRe.FindAllStringIndex(out, -1) {
		marks = append(marks, [2]int{m[0], m[1]})
	}
	return out, marks
}

// truncate cuts s to at most max runes including a trailing …, never inside
// a placeholder, a rune or a combining sequence.
func truncate(s string, marks [][2]int, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	cut, n := 0, 0
	for i := range s {
		if n == maxRunes-1 {
			cut = i
			break
		}
		n++
	}
	for _, m := range marks {
		if m[0] < cut && cut < m[1] {
			cut = min(cut, m[0])
		}
	}
	for cut > 0 {
		r, _ := utf8.DecodeRuneInString(s[cut:])
		if !unicode.IsMark(r) {
			break
		}
		_, w := utf8.DecodeLastRuneInString(s[:cut])
		cut -= w
	}
	return s[:cut] + ellipsis
}

// namesPerRegexp keeps each names pattern well under the regexp size limit.
const namesPerRegexp = 256

// namesRegexps matches the names, longest first, case-insensitively, split
// across several patterns so a long list never fails to compile.
func namesRegexps(names []string) []*regexp.Regexp {
	var alts []string
	for _, n := range names {
		n = foldForView(n)
		if utf8.RuneCountInString(n) >= 2 {
			alts = append(alts, regexp.QuoteMeta(n))
		}
	}
	slices.SortFunc(alts, func(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a, b)) })
	alts = slices.Compact(alts)
	var res []*regexp.Regexp
	for chunk := range slices.Chunk(alts, namesPerRegexp) {
		if re, err := regexp.Compile(`(?i)` + strings.Join(chunk, "|")); err == nil {
			res = append(res, re)
			continue
		}
		for _, a := range chunk { // a single oversized name: compile it alone
			if re, err := regexp.Compile(`(?i)` + a); err == nil {
				res = append(res, re)
			}
		}
	}
	return res
}

// view is s with invisibles removed and each character folded (NFKC, and
// Arabic-Indic digits to ASCII), with a map back to byte offsets in s, so
// the patterns see through obfuscation while the masks cover the original.
type view struct {
	text   string
	starts []int // for each view byte, the start of its source character
	ends   []int // and its end
}

func newView(s string) view {
	var b strings.Builder
	b.Grow(len(s))
	starts := make([]int, 0, len(s))
	ends := make([]int, 0, len(s))
	for i, r := range s {
		w := utf8.RuneLen(r)
		if r == utf8.RuneError {
			w = 1
			if strings.HasPrefix(s[i:], "�") {
				w = 3
			}
		}
		if normalize.IsInvisible(r) {
			continue
		}
		f := foldRune(r)
		b.WriteString(f)
		for range len(f) {
			starts = append(starts, i)
			ends = append(ends, i+w)
		}
	}
	return view{text: b.String(), starts: starts, ends: ends}
}

func foldRune(r rune) string {
	switch {
	case r < utf8.RuneSelf:
		return string(r)
	case r >= '٠' && r <= '٩':
		return string('0' + r - '٠')
	case r >= '۰' && r <= '۹':
		return string('0' + r - '۰')
	}
	return norm.NFKC.String(string(r))
}

func foldForView(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !normalize.IsInvisible(r) {
			b.WriteString(foldRune(r))
		}
	}
	return b.String()
}

func (v view) toSpan(vs, ve int, tok string) span {
	return span{v.starts[vs], v.ends[ve-1], tok}
}

func (v view) spans(re *regexp.Regexp, tok string) []span {
	var out []span
	for _, m := range re.FindAllStringIndex(v.text, -1) {
		if m[1] > m[0] {
			out = append(out, v.toSpan(m[0], m[1], tok))
		}
	}
	return out
}

// emailSpans finds addresses around each @: a local part of letters, digits
// and ._%+- before it (with an optional mailto: in front), and a domain of
// two or more labels after it. Neither side crosses another @, so the scan
// stays linear.
func (v view) emailSpans() []span {
	t := v.text
	var out []span
	for at := strings.IndexByte(t, '@'); at >= 0; {
		start := at
		for start > 0 {
			r, w := utf8.DecodeLastRuneInString(t[:start])
			if !isLocalRune(r) {
				break
			}
			start -= w
		}
		end, dots, label := at+1, 0, 0
		for end < len(t) {
			r, w := utf8.DecodeRuneInString(t[end:])
			switch {
			case r == '.' && label > 0:
				dots++
				label = 0
			case r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r):
				label++
			default:
				goto done
			}
			end += w
		}
	done:
		if label == 0 && dots > 0 { // a trailing dot ends the sentence, not the domain
			end--
			dots--
		}
		if start < at && dots > 0 {
			if start >= 7 && strings.EqualFold(t[start-7:start], "mailto:") {
				start -= 7
			}
			out = append(out, v.toSpan(start, end, EmailToken))
		}
		next := strings.IndexByte(t[at+1:], '@')
		if next < 0 {
			break
		}
		at += 1 + next
	}
	return out
}

func isLocalRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._%+-", r)
}

// wordSpans is spans for matches that stand as whole words.
func (v view) wordSpans(re *regexp.Regexp, tok string) []span {
	var out []span
	for pos := 0; pos < len(v.text); {
		m := re.FindStringIndex(v.text[pos:])
		if m == nil {
			break
		}
		start, end := pos+m[0], pos+m[1]
		if end == start {
			break
		}
		before, _ := utf8.DecodeLastRuneInString(v.text[:start])
		after, _ := utf8.DecodeRuneInString(v.text[end:])
		if !isWordRune(before) && !isWordRune(after) {
			out = append(out, v.toSpan(start, end, tok))
			pos = end
			continue
		}
		_, w := utf8.DecodeRuneInString(v.text[start:])
		pos = start + w
	}
	return out
}

func isWordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r))
}
