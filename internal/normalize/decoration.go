package normalize

import "strings"

// StripDecorations removes underline and overline marks (s̲p̲a̲m̲, س̲ڪ̲ل̲ي̲ف̲)
// and returns how many it removed. Underline generators put a mark after
// every character, spaces and punctuation included, so marks go whatever
// their base. No orthography uses these marks on their own; a precomposed
// letter such as ṯ is not a mark and stays.
func StripDecorations(s string) (string, int) {
	if !strings.ContainsFunc(s, isDecoration) {
		return s, 0
	}
	removed := 0
	s = strings.Map(func(r rune) rune {
		if isDecoration(r) {
			removed++
			return -1
		}
		return r
	}, s)
	return s, removed
}

// isDecoration reports the combining marks used only to underline or
// overline text: overline U+0305, macron below U+0331, low line U+0332,
// double low line U+0333, double overline U+033F, and the double macrons
// U+035E and U+035F. The strikethrough overlays U+0334–U+0338 stay: fewer
// than floodMarks of them on a letter are kept (DESIGN.md §7.2 step 2).
func isDecoration(r rune) bool {
	return r == 0x0305 || r >= 0x0331 && r <= 0x0333 || r == 0x033F || r == 0x035E || r == 0x035F
}
