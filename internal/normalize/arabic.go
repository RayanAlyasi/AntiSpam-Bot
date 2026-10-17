package normalize

import "strings"

// FoldArabic applies the DESIGN.md §7.2 step 5 folds that keep words apart:
// it removes tashkeel and tatweel, unifies alef forms, maps Persian ک/ی to
// Arabic ك/ي, and turns Arabic-Indic and Persian digits into ASCII. Text
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
		case r == 'ک':
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
		r == 'آ' || r == 'أ' || r == 'إ' || r == 'ٱ' || r == 'ک' || r == 'ی' ||
		r >= '٠' && r <= '٩' || r >= '۰' && r <= '۹')
}

// isTashkeel reports Arabic diacritics: harakat and other marks U+064B–U+065F,
// superscript alef U+0670, and the Quranic annotation signs U+06D6–U+06ED.
func isTashkeel(r rune) bool {
	return r >= 0x064B && r <= 0x065F || r == 0x0670 || r >= 0x06D6 && r <= 0x06ED
}
