package textmatch

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Prepare puts text and patterns into the form the matchers compare: lower case, every run of
// white space (including newlines) becomes one space, and leading and trailing space goes.
// Text and patterns must both be prepared, after the same normalization variant.
func Prepare(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Words is the form keyword, phrase and fuzzy matching compare (DESIGN.md §7.2: Aho-Corasick
// over normalized tokens): Prepare, then every run of characters that are not letters, marks
// or digits becomes one space. So "free-bitcoin!!" and "free bitcoin" are the same words.
func Words(s string) string {
	return strings.Join(Tokens(Prepare(s)), " ")
}

// isWordRune reports whether r is part of a word (letters, marks and digits): a whole-word
// match may not continue into one.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r)
}

// atWordBoundary reports whether s[start:end] is not glued to word characters on either side.
func atWordBoundary(s string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		first, _ := utf8.DecodeRuneInString(s[start:])
		if isWordRune(r) && isWordRune(first) {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		last, _ := utf8.DecodeLastRuneInString(s[:end])
		if isWordRune(r) && isWordRune(last) {
			return false
		}
	}
	return true
}
