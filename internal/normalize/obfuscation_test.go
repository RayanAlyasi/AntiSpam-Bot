package normalize

import (
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/normtest"
)

func TestArabicSpamObfuscation(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		norm, loose  string
		wantStripped int
	}{
		{"underlined letters", "س̲ڪ̲ل̲ي̲ف̲ اج̲ازة", "سكليف اجازة", "سكليف اجازه", 6},
		{"overline", "س̅ك̅ل̅ي̅ف̅", "سكليف", "سكليف", 5},
		{"macron below", "سك̱ليف", "سكليف", "سكليف", 1},
		{"underline on tashkeel", "سَ̲كليف", "سكليف", "سكليف", 1},
		{"underlined latin", "f̲r̲e̲e̲", "free", "free", 4},
		{"underlined spaces", "g̲e̲t̲ ̲f̲r̲e̲e̲ ̲c̲r̲y̲p̲t̲o̲", "get free crypto", "get free crypto", 15},
		{"underlined arabic phrase", "ا̲ج̲ا̲ز̲ه̲ ̲م̲ر̲ض̲ي̲ة̲", "اجازه مرضية", "اجازه مرضيه", 11},
		{"underlined digits", "0̲5̲5̲", "055", "055", 3},
		{"swash kaf", "سڪليف", "سكليف", "سكليف", 0},
		{"kaf with ring", "سګليف", "سكليف", "سكليف", 0},
		{"kaf with dot above", "سڬليف", "سكليف", "سكليف", 0},
		{"persian kaf", "سکليف", "سكليف", "سكليف", 0},
		{"dot between tatweel", "اجـ.ـازه مرضـ.ـية", "اجازه مرضية", "اجازه مرضيه", 2},
		{"tatweel one side", "اجـ.ازه", "اجازه", "اجازه", 1},
		{"each separator", "اجـ·ـا اجـ•ـا اجـ-ـا اجـ_ـا اجـ|ـا اجـ/ـا", "اجا اجا اجا اجا اجا اجا", "اجا اجا اجا اجا اجا اجا", 6},
		{"repeated separators", "سكـ..ـليف", "سكليف", "سكليف", 2},
		{"after tashkeel", "اجَـ.ـازه", "اجازه", "اجازه", 1},
		{"tashkeel on tatweel", "اجـٌ.ـازه", "اجازه", "اجازه", 1},
		{"underlined wedge", "ا̲ج̲ـ̲.̲ـ̲ا̲ز̲ه̲", "اجازه", "اجازه", 9},
		{"all three", "س̲ڪ̲ـ.ـل̲ي̲ف̲", "سكليف", "سكليف", 6},
		// Unchanged.
		{"tashkeel", "مَرْحَبًا بِكُمْ", "مرحبا بكم", "مرحبا بكم", 0},
		{"abbreviation without tatweel", "د.محمد", "د.محمد", "د.محمد", 0},
		{"alternatives without tatweel", "ذكر/انثى", "ذكر/انثى", "ذكر/انثي", 0},
		{"separator before a space", "اجـ. ازه", "اج. ازه", "اج. ازه", 0},
		{"separator before a digit", "رقمـ-5", "رقم-5", "رقم-5", 0},
		{"decimal", "3.5", "3.5", "3.5", 0},
		{"arabic decimal", "٣.٥", "3.5", "3.5", 0},
		{"domain", "a.b.com", "a.b.com", "a.b.com", 0},
		{"email", "user.name@mail.com", "user.name@mail.com", "user.name@mail.com", 0},
		{"date", "2024-10-18", "2024-10-18", "2024-10-18", 0},
		{"latin abbreviation", "e.g. this", "e.g. this", "e.g. this", 0},
		{"url", "https://x.com/a_b-c", "https://x.com/a_b-c", "https://x.com/a_b-c", 0},
		{"strikethrough below flood", "s̶p̶a̶m̶", "s̶p̶a̶m̶", "s̶p̶a̶m̶", 0},
		{"overline character", "‾", " ", " ", 0},
		{"macron below on latin", "t\u0331", "t", "t", 1},
		{"precomposed letter", "ṯ", "ṯ", "ṯ", 0},
		{"negated relation", "=\u0338", "≠", "≠", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Text(tt.in)
			if r.Norm != tt.norm || r.Loose != tt.loose || r.Stripped != tt.wantStripped {
				t.Errorf("Text(%+q) = Norm %+q, Loose %+q, Stripped %d; want %+q, %+q, %d",
					tt.in, r.Norm, r.Loose, r.Stripped, tt.norm, tt.loose, tt.wantStripped)
			}
		})
	}
}

func TestObfuscationFoldsLinear(t *testing.T) {
	for name, unit := range map[string]string{
		"underlined":   "س̲",
		"wedged":       "بـ.ـ",
		"tatweel runs": "بـ.ـ.ـ ",
	} {
		n := (64 << 10) / len(unit)
		normtest.Linear(t, "Text("+name+")", func(k int) string { return strings.Repeat(unit, k*n) },
			func(in string) { Text(in) })
	}
}
