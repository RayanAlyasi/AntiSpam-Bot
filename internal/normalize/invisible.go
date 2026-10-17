package normalize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	zwnj      = '\u200C'
	zwj       = '\u200D'
	keycap    = '\u20E3'
	blackFlag = '\U0001F3F4'
	tagCancel = '\U000E007F'
	tagFirst  = '\U000E0020'
	tagLast   = '\U000E007E'
	textVS    = '\uFE0E'
	emojiVS   = '\uFE0F'
)

// IsInvisible reports whether r is an invisible character that Norm never
// keeps (zero-width, bidi control, tag, variation selector, soft hyphen and
// similar fillers). Other packages use it to see through hidden characters.
func IsInvisible(r rune) bool { return isInvisible(r) }

// isInvisible reports whether r is always removed from Norm: the DESIGN.md
// §7.2 list (zero-width, bidi controls, tag characters, variation selectors,
// soft hyphen) plus other default-ignorable fillers used to hide text.
func isInvisible(r rune) bool {
	switch {
	case r < 0x00AD:
		return false
	case r == 0x00AD, // soft hyphen
		r == 0x034F,              // combining grapheme joiner
		r == 0x061C,              // Arabic letter mark
		r == 0x115F, r == 0x1160, // Hangul choseong and jungseong fillers
		r == 0x17B4, r == 0x17B5, // Khmer inherent vowels
		r == 0x180E,              // Mongolian vowel separator
		r == 0x3164, r == 0xFFA0, // Hangul fillers
		r == 0xFEFF: // zero-width no-break space (BOM)
		return true
	case r >= 0x200B && r <= 0x200F, // zero-width space, ZWNJ, ZWJ, LRM, RLM
		r >= 0x202A && r <= 0x202E,   // bidi embeddings and overrides
		r >= 0x2060 && r <= 0x2069,   // word joiner, invisible operators, bidi isolates
		r >= 0xFE00 && r <= 0xFE0F,   // variation selectors
		r >= 0x1BCA0 && r <= 0x1BCA3, // shorthand format controls
		r >= 0xE0000 && r <= 0xE007F, // tag characters
		r >= 0xE0100 && r <= 0xE01EF: // variation selectors supplement
		return true
	}
	return false
}

// StripInvisibles removes every invisible character from s, replaces invalid
// UTF-8 with U+FFFD, and counts the removed characters that look like
// obfuscation. Invisibles with a legitimate job where they stand (emoji
// presentation, emoji joiners, Persian ZWNJ, tag flags) are removed but not
// counted; see legitimateInvisibles.
func StripInvisibles(s string) (string, int) {
	if utf8.ValidString(s) && !strings.ContainsFunc(s, isInvisible) {
		return s, 0
	}
	in := make([]rune, 0, len(s))
	for _, r := range s {
		in = append(in, r) // range yields U+FFFD for each invalid byte
	}
	legit := legitimateInvisibles(in)
	var b strings.Builder
	b.Grow(len(s))
	count := 0
	for i, r := range in {
		if !isInvisible(r) {
			b.WriteRune(r)
			continue
		}
		if !legit[i] {
			count++
		}
	}
	return b.String(), count
}

// removeInvisibles drops invisible characters without counting them.
func removeInvisibles(s string) string {
	return strings.Map(func(r rune) rune {
		if isInvisible(r) {
			return -1
		}
		return r
	}, s)
}

// legitimateInvisibles marks the invisible characters in in that are judged
// legitimate where they stand, so they don't raise the obfuscation count:
//   - U+FE0E/U+FE0F right after an emoji-like character, or after a keycap
//     base (digit, '#', '*') when U+20E3 follows;
//   - U+200D between two emoji-like characters, skipping variation selectors;
//   - U+200C/U+200D between two Arabic-script letters (Persian ZWNJ), skipping
//     combining marks;
//   - a well-formed emoji tag sequence: U+1F3F4, tags U+E0020–U+E007E, U+E007F.
//
// Everything is decided on the input as written, in linear time.
func legitimateInvisibles(in []rune) []bool {
	n := len(in)
	legit := make([]bool, n)

	// next non-variation-selector and next non-mark index for each position,
	// filled right to left; -1 when there is none.
	nextNonVS := make([]int32, n)
	nextNonMark := make([]int32, n)
	nv, nm := int32(-1), int32(-1)
	for i := n - 1; i >= 0; i-- {
		nextNonVS[i], nextNonMark[i] = nv, nm
		if !isVariationSelector(in[i]) {
			nv = int32(i)
		}
		if !unicode.IsMark(in[i]) {
			nm = int32(i)
		}
	}

	prevNonVS, prevNonMark := -1, -1
	tagFlagEnd := -1
	for i, r := range in {
		switch {
		case r == blackFlag:
			if end := tagSequenceEnd(in, i); end > 0 {
				tagFlagEnd = end
			}
		case i <= tagFlagEnd:
			legit[i] = true
		case r == textVS || r == emojiVS:
			legit[i] = i > 0 && (emojiLike(in[i-1]) ||
				isKeycapBase(in[i-1]) && i+1 < n && in[i+1] == keycap)
		case r == zwj || r == zwnj:
			if r == zwj && prevNonVS >= 0 && nextNonVS[i] >= 0 &&
				emojiLike(in[prevNonVS]) && emojiLike(in[nextNonVS[i]]) {
				legit[i] = true
			}
			if prevNonMark >= 0 && nextNonMark[i] >= 0 &&
				isArabicLetter(in[prevNonMark]) && isArabicLetter(in[nextNonMark[i]]) {
				legit[i] = true
			}
		}
		if !isVariationSelector(r) {
			prevNonVS = i
		}
		if !unicode.IsMark(r) {
			prevNonMark = i
		}
	}
	return legit
}

// tagSequenceEnd returns the index of the U+E007F that ends a well-formed tag
// sequence starting at the U+1F3F4 at in[start], or -1.
func tagSequenceEnd(in []rune, start int) int {
	j := start + 1
	for j < len(in) && in[j] >= tagFirst && in[j] <= tagLast {
		j++
	}
	if j > start+1 && j < len(in) && in[j] == tagCancel {
		return j
	}
	return -1
}

// emojiLike reports whether r can carry an emoji variation selector or be
// joined by ZWJ: anything except a letter, digit, mark, space or invisible.
func emojiLike(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) &&
		!unicode.IsSpace(r) && !isInvisible(r)
}

func isKeycapBase(r rune) bool {
	return r >= '0' && r <= '9' || r == '#' || r == '*'
}

func isVariationSelector(r rune) bool {
	return r >= 0xFE00 && r <= 0xFE0F || r >= 0xE0100 && r <= 0xE01EF
}

func isArabicLetter(r rune) bool {
	return unicode.Is(unicode.Arabic, r) && unicode.IsLetter(r)
}
