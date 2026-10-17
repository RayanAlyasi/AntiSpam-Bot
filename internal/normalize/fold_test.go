package normalize

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func TestStripInvisibles(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		wantCount int
	}{
		{"plain", "free money", "free money", 0},
		{"zwsp between letters", "fr\u200Bee", "free", 1},
		{"every listed range", "a\u200B\u200C\u200D\u200E\u200F\u2060\u2064\uFEFF\u202A\u202E\u2066\u2069\u00ADb", "ab", 13},
		{"variation selector between letters", "fr\uFE0Fee", "free", 1},
		{"supplementary variation selector", "a\U000E0100b", "ab", 1},
		{"zwj between latin letters", "fr\u200Dee", "free", 1},
		{"zwnj between latin letters", "fr\u200Cee", "free", 1},
		{"bidi override", "\u202Eeerf", "eerf", 1},
		{"hangul filler name", "\u3164\u3164", "", 2},
		{"tag smuggling", "hi\U000E0069\U000E0067\U000E006E", "hi", 3},
		{"tag run after flag without cancel", "\U0001F3F4\U000E0067\U000E0062", "\U0001F3F4", 2},
		{"emoji presentation", "❤\uFE0F", "❤", 0},
		{"text presentation", "❤\uFE0E", "❤", 0},
		{"keycap", "1\uFE0F\u20E3", "1\u20E3", 0},
		{"vs16 after digit without keycap", "1\uFE0F2", "12", 1},
		{"family zwj", "\U0001F468\u200D\U0001F469\u200D\U0001F467", "\U0001F468\U0001F469\U0001F467", 0},
		{"zwj after vs", "\U0001F3F3\uFE0F\u200D\U0001F308", "\U0001F3F3\U0001F308", 0},
		{"doubled zwj", "\U0001F468\u200D\u200D\U0001F469", "\U0001F468\U0001F469", 2},
		{"persian zwnj", "\u0645\u06CC\u200C\u062E\u0648\u0627\u0647\u0645", "\u0645\u06CC\u062E\u0648\u0627\u0647\u0645", 0},
		{"persian zwnj after tashkeel", "\u0628\u064E\u200C\u0628", "\u0628\u064E\u0628", 0},
		{"zwnj arabic then latin", "\u0628\u200Cb", "\u0628b", 1},
		{"scotland flag", "\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", "\U0001F3F4", 0},
		{"invalid utf8", "a\xffb", "a�b", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, n := StripInvisibles(tt.in)
			if got != tt.want || n != tt.wantCount {
				t.Errorf("StripInvisibles(%+q) = %+q, %d; want %+q, %d (legitimate emoji and Persian joiners must not raise the obfuscation count)",
					tt.in, got, n, tt.want, tt.wantCount)
			}
		})
	}
}

func TestStripFloods(t *testing.T) {
	zalgo := func(base string, marks int) string {
		var b strings.Builder
		for _, r := range base {
			b.WriteRune(r)
			for i := range marks {
				b.WriteRune(rune(0x0316 + i%10)) // marks below that never compose
			}
		}
		return b.String()
	}
	tests := []struct {
		name      string
		in        string
		want      string
		wantCount int
	}{
		{"no marks", "free", "free", 0},
		{"three marks kept", zalgo("free", 3), zalgo("free", 3), 0},
		{"four marks stripped", zalgo("free", 4), "free", 16},
		{"long flood", zalgo("a", 200), "a", 200},
		{"leading marks without base", "\u0300\u0301\u0302\u0303x", "x", 4},
		{"leading marks below threshold", "\u0300x", "\u0300x", 0},
		{"expanding mark counts double", "q\u0344\u0344", "q", 2},
		{"composing mark doesn't count", "a\u0344\u0344", "a\u0344\u0344", 0},
		{"enclosing marks", "a\u20DD\u20DD\u20DD\u20DD", "a", 4},
		{"arabic tashkeel kept", "\u0628\u0651\u064E", "\u0628\u0651\u064E", 0},
		{"keycap mark kept", "1\u20E3", "1\u20E3", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, n := StripFloods(tt.in)
			if got != tt.want || n != tt.wantCount {
				t.Errorf("StripFloods(%+q) = %+q, %d; want %+q, %d (a flood is %d or more marks on one base)",
					tt.in, got, n, tt.want, tt.wantCount, floodMarks)
			}
		})
	}
}

func TestFold(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		wantCount int
	}{
		{"ascii untouched", "buy crypto now", "buy crypto now", 0},
		{"fullwidth", "ｆｒｅｅ\u3000１００", "free 100", 0},
		{"math bold", "𝐟𝐫𝐞𝐞", "free", 0},
		{"circled", "ⓕⓡⓔⓔ", "free", 0},
		{"ligature", "ﬁnance", "finance", 0},
		{"decomposed accent composes", "cafe\u0301", "café", 0},
		{"invisible between base and accent", "cafe\u200B\u0301", "café", 1},
		{"composable mark leaves three", "e\u0301\u0302\u0303\u0304", "é\u0302\u0303\u0304", 0},
		{"zalgo with composable marks", "e\u0301\u0302\u0303\u0304\u0305", "e", 5},
		{"precomposed plus three kept", "é\u0302\u0303\u0304", "é\u0302\u0303\u0304", 0},
		{"halfwidth sound marks become a flood", "a\uFF9E\uFF9E\uFF9E\uFF9E", "a", 4},
		{"hangul filler between jamo", "\u1100\u3164\u1161", "가", 1},
		{"only invisibles", "\u200B\u200C\uFEFF", "", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, n := Fold(tt.in)
			if got != tt.want || n != tt.wantCount {
				t.Errorf("Fold(%+q) = %+q, %d; want %+q, %d", tt.in, got, n, tt.want, tt.wantCount)
			}
		})
	}
}

func TestFoldLongMarkRunLeavesNoGraphemeJoiner(t *testing.T) {
	// NFKC inserts U+034F after 30 non-starters; none may survive or be counted.
	in := "a" + strings.Repeat("\U0001D165", 100) // Mc with a non-zero combining class
	got, n := Fold(in)
	if strings.ContainsRune(got, 0x034F) {
		t.Errorf("Fold left U+034F in %+q; want none (it is never part of the input)", got)
	}
	if n != 100 {
		t.Errorf("Fold count = %d, want 100 (every stacked character is a flood mark)", n)
	}
}

func FuzzFold(f *testing.F) {
	for _, s := range []string{
		"", "free", "ｆｒｅｅ", "fr\u200Bee", "❤\uFE0F", "1\uFE0F\u20E3",
		"\U0001F468\u200D\U0001F469", "\U0001F3F4\U000E0067\U000E0062\U000E007F",
		"e\u0301\u0302\u0303\u0304", "a\uFF9E\uFF9E\uFF9E\uFF9E", "\u1100\u3164\u1161", "a\xffb",
		"\u0645\u06CC\u200C\u062E", "a" + strings.Repeat("\u0300", 40),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got, n := Fold(in)
		if !utf8.ValidString(got) {
			t.Fatalf("Fold(%+q) = %+q is not valid UTF-8", in, got)
		}
		if !norm.NFKC.IsNormalString(got) {
			t.Fatalf("Fold(%+q) = %+q is not NFKC-normal", in, got)
		}
		if strings.ContainsFunc(got, isInvisible) {
			t.Fatalf("Fold(%+q) = %+q still has an invisible character", in, got)
		}
		if n < 0 || n > utf8.RuneCountInString(in) {
			t.Fatalf("Fold(%+q) count = %d, want 0..%d", in, n, utf8.RuneCountInString(in))
		}
		again, n2 := Fold(got)
		if again != got || n2 != 0 {
			t.Fatalf("Fold is not idempotent: Fold(%+q) = %+q, %d; want %+q, 0", got, again, n2, got)
		}
	})
}
