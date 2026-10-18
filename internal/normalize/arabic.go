package normalize

import (
	"strings"
	"unicode/utf8"
)

// FoldArabic applies the DESIGN.md §7.2 step 5 folds that keep words apart:
// it removes tashkeel and tatweel, unifies alef forms, maps Persian ک/ی and
// the kaf variants ڪ ګ ڬ to Arabic ك/ي, and turns Arabic-Indic and Persian digits into ASCII. Text
// without these characters is returned unchanged.
func FoldArabic(s string) string {
	if !strings.ContainsFunc(s, isArabicFoldable) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case isTashkeel(r), r == 'ـ':
			return -1
		case r == 'آ', r == 'أ', r == 'إ', r == 'ٱ':
			return 'ا'
		case isKafVariant(r):
			return 'ك'
		case r == 'ی':
			return 'ي'
		case r >= '٠' && r <= '٩':
			return '0' + r - '٠'
		case r >= '۰' && r <= '۹':
			return '0' + r - '۰'
		}
		return r
	}, s)
}

// LooseArabic adds the lossy folds of the §7.2 match variant to an already
// folded string: ى→ي and ة→ه. They merge distinct words (على/علي), so they
// live in their own variant.
func LooseArabic(s string) string {
	if !strings.ContainsAny(s, "ىة") {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case 'ى':
			return 'ي'
		case 'ة':
			return 'ه'
		}
		return r
	}, s)
}

func isArabicFoldable(r rune) bool {
	return r >= 0x0622 && r <= 0x06F9 && (isTashkeel(r) || r == 'ـ' ||
		r == 'آ' || r == 'أ' || r == 'إ' || r == 'ٱ' || isKafVariant(r) || r == 'ی' ||
		r >= '٠' && r <= '٩' || r >= '۰' && r <= '۹')
}

// isTashkeel reports Arabic diacritics: harakat and other marks U+064B–U+065F,
// superscript alef U+0670, and the Quranic annotation signs U+06D6–U+06ED.
func isTashkeel(r rune) bool {
	return r >= 0x064B && r <= 0x065F || r == 0x0670 || r >= 0x06D6 && r <= 0x06ED
}

// isKafVariant reports Persian kaf ک and the kaf forms that spam writes in
// place of ك: swash kaf ڪ, kaf with ring ګ and kaf with dot above ڬ.
func isKafVariant(r rune) bool {
	return r >= 0x06A9 && r <= 0x06AC
}

// StripWordSeparators removes separators wedged into an Arabic word with
// tatweel (اجـ.ـازه → اجازه) and returns how many separators it removed. A
// run of tatweel, tashkeel and the separators . · • - _ | / is removed whole
// when it has at least one tatweel and one separator and sits between two
// Arabic letters. Without
// tatweel the separator is kept, because Arabic writes abbreviations and
// alternatives that way (د.محمد, ذكر/أنثى). Latin, digits and URLs never
// qualify.
func StripWordSeparators(s string) (string, int) {
	if !strings.ContainsRune(s, 'ـ') || !strings.ContainsAny(s, wordSeparators) {
		return s, 0
	}
	var b strings.Builder
	removed := 0
	changed := false
	afterLetter := false // the last non-mark character is an Arabic letter
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if afterLetter && isWedge(r) {
			end, tatweel, seps := i, false, 0
			for end < len(s) {
				r, w := utf8.DecodeRuneInString(s[end:])
				if isTashkeel(r) {
					end += w
					continue
				}
				if !isWedge(r) {
					break
				}
				if r == 'ـ' {
					tatweel = true
				} else {
					seps++
				}
				end += w
			}
			if next, _ := utf8.DecodeRuneInString(s[end:]); tatweel && seps > 0 && isWordLetter(next) {
				if !changed {
					b.Grow(len(s))
					b.WriteString(s[:i])
					changed = true
				}
				removed += seps
			} else if changed {
				b.WriteString(s[i:end])
			}
			i, afterLetter = end, false
			continue
		}
		if !isMark(r) {
			afterLetter = isWordLetter(r)
		}
		if changed {
			b.WriteString(s[i : i+w])
		}
		i += w
	}
	if !changed {
		return s, 0
	}
	return b.String(), removed
}

// wordSeparators are the separators StripWordSeparators removes.
const wordSeparators = ".·•-_|/"

// isWedge reports tatweel and the separators spam wedges into Arabic words.
func isWedge(r rune) bool {
	return r == 'ـ' || strings.ContainsRune(wordSeparators, r)
}

// isWordLetter reports an Arabic letter other than tatweel.
func isWordLetter(r rune) bool {
	return r != 'ـ' && isArabicLetter(r)
}
