package normalize

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// leetFolds are the digits and symbols read as letters (DESIGN.md §7.2
// step 4). 1 reads as i here; Skeleton covers the 1→l reading.
var leetFolds = map[rune]rune{
	'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '$': 's', '@': 'a',
}

func isLeet(r rune) bool {
	_, ok := leetFolds[r]
	return ok
}

func isLeetUnit(r rune) bool { return unicode.IsLetter(r) || isLeet(r) }

// Leet returns the leetspeak variant of a Norm string: spelled-out runs that
// mix letters and leet characters collapse (f.r.3.3), leet characters fold to
// letters inside words with at least as many letters as leet characters
// (fr33, m0ney, c@sh), and stretched letters squeeze again. Numbers, prices
// and phone numbers stay as they are, because they have no letters.
func Leet(s string) string {
	if !strings.ContainsFunc(s, isLeet) {
		return s
	}
	t := collapse(s, isLeetUnit, func(run []token) bool {
		var letters, leet []rune
		for i := 0; i < len(run); i += 2 {
			for _, r := range run[i].text {
				if isLeet(r) {
					leet = append(leet, r)
				} else if unicode.IsLetter(r) {
					letters = append(letters, r)
				}
			}
		}
		return len(leet) > 0 && len(letters) >= len(leet)
	})
	t = foldLeetWords(t)
	t = SqueezeRepeats(t)
	if t == s {
		return s
	}
	return norm.NFKC.String(t)
}

// foldLeetWords folds leet characters inside each maximal run of letters and
// leet characters that has at least as many letters as leet characters.
func foldLeetWords(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if !isLeetUnit(rs[i]) {
			b.WriteRune(rs[i])
			i++
			continue
		}
		j, letters, leet := i, 0, 0
		for j < len(rs) && (isLeetUnit(rs[j]) || unicode.IsMark(rs[j])) {
			if isLeet(rs[j]) {
				leet++
			} else if unicode.IsLetter(rs[j]) {
				letters++
			}
			j++
		}
		fold := leet > 0 && letters >= leet
		for _, r := range rs[i:j] {
			if f, ok := leetFolds[r]; ok && fold {
				r = f
			}
			b.WriteRune(r)
		}
		i = j
	}
	return b.String()
}
