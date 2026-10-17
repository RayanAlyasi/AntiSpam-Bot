package normalize

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// floodMarks is the number of combining marks on one base that counts as a
// flood (Zalgo). Legitimate Arabic, Hebrew and IPA text stacks up to three.
const floodMarks = 4

// StripFloods removes every run of floodMarks or more combining marks from
// the base they sit on, keeping the base, and returns how many mark
// characters it removed. The run is measured as NFKC leaves it: U+0344
// counts as two, and a mark that composes into the base doesn't count.
// Runs of 1–3 marks are kept.
func StripFloods(s string) (string, int) {
	var b strings.Builder
	removed := 0
	changed := false
	for i := 0; i < len(s); {
		start := i
		if r, w := utf8.DecodeRuneInString(s[i:]); !isMark(r) {
			i += w
		}
		baseEnd := i
		weight, marks := 0, 0
		for i < len(s) {
			r, w := utf8.DecodeRuneInString(s[i:])
			if !isMark(r) {
				break
			}
			weight += markWeight(r)
			marks++
			i += w
		}
		if weight >= floodMarks && composedMarks(s[start:i]) >= floodMarks {
			if !changed {
				b.Grow(len(s))
				b.WriteString(s[:start])
				changed = true
			}
			b.WriteString(s[start:baseEnd])
			removed += marks
			continue
		}
		if changed {
			b.WriteString(s[start:i])
		}
	}
	if !changed {
		return s, 0
	}
	return b.String(), removed
}

// composedMarks counts the marks a cluster keeps after NFKC, so marks that
// compose into the base (e + U+0301 → é) don't count and the precomposed and
// decomposed spellings are judged alike.
func composedMarks(cluster string) int {
	n := 0
	for _, r := range norm.NFKC.String(cluster) {
		if isMark(r) && r != 0x034F {
			n++
		}
	}
	return n
}

// isMark reports whether r stacks on the character before it: a nonspacing
// or enclosing mark, or any character with a non-zero combining class.
func isMark(r rune) bool {
	if r < 0x0300 {
		return false
	}
	if unicode.In(r, unicode.Mn, unicode.Me) {
		return true
	}
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	return norm.NFD.Properties(buf[:n]).CCC() != 0
}

// markWeight is the number of marks r becomes under NFKD, at least one.
func markWeight(r rune) int {
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	d := norm.NFKD.Properties(buf[:n]).Decomposition()
	if len(d) == 0 {
		return 1
	}
	w := 0
	for _, dr := range string(d) {
		if isMark(dr) {
			w++
		}
	}
	return max(w, 1)
}
