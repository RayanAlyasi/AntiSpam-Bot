package normalize_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

// isListedInvisible reports whether r is one of the code points DESIGN §7.2 step 2 requires
// to be stripped from Norm.
func isListedInvisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, r >= 0x2060 && r <= 0x2064, r == 0xFEFF,
		r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069,
		r >= 0xE0000 && r <= 0xE007F,
		r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF,
		r == 0x00AD:
		return true
	}
	return false
}

func firstInvisible(s string) (rune, bool) {
	for _, r := range s {
		if isListedInvisible(r) {
			return r, true
		}
	}
	return 0, false
}

// interleave puts sep between every rune of word.
func interleave(word, sep string) string {
	var b strings.Builder
	for i, r := range []rune(word) {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// zalgoMarks are combining marks that never compose with Latin letters under NFKC.
var zalgoMarks = []rune{0x0334, 0x0335, 0x0336, 0x0337, 0x033F, 0x0346, 0x034A, 0x0489}

// zalgo puts the first k zalgoMarks after every rune of word.
func zalgo(word string, k int) string {
	var b strings.Builder
	for _, r := range word {
		b.WriteRune(r)
		for i := 0; i < k; i++ {
			b.WriteRune(zalgoMarks[i%len(zalgoMarks)])
		}
	}
	return b.String()
}

// assertSameNorm checks that obf normalizes to the same Norm as plain, with wantStripped removals.
func assertSameNorm(t *testing.T, obf, plain string, wantStripped int, why string) {
	t.Helper()
	want := normalize.Text(plain)
	if want.Norm == "" {
		t.Fatalf("Text(%+q).Norm is empty, want non-empty (plain visible text must survive normalization, DESIGN §7.2)", plain)
	}
	got := normalize.Text(obf)
	if got.Norm != want.Norm {
		t.Errorf("Text(%+q).Norm = %+q, want %+q, the Norm of %+q (%s, DESIGN §7.2)", obf, got.Norm, want.Norm, plain, why)
	}
	if got.Stripped != wantStripped {
		t.Errorf("Text(%+q).Stripped = %d, want %d (%s, DESIGN §7.2)", obf, got.Stripped, wantStripped, why)
	}
	if r, ok := firstInvisible(got.Norm); ok {
		t.Errorf("Text(%+q).Norm still contains %U, want it removed (invisible characters are stripped from Norm, DESIGN §7.2 step 2)", obf, r)
	}
}

func TestRawPreserved(t *testing.T) {
	inputs := []string{
		"",
		"buy crypto now",
		"c\U0000200Br\U0000200By\U0000200Bp\U0000200Bt\U0000200Bo",
		"\U0000FF42\U0000FF55\U0000FF59",
		zalgo("crypto", 6),
		"\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F",
		"\xff\xfe invalid \xc3\x28 utf8",
		"cafe\U00000301",
	}
	for _, in := range inputs {
		if got := normalize.Text(in).Raw; got != in {
			t.Errorf("Text(%+q).Raw = %+q, want the input unchanged (Raw is kept byte-for-byte for evidence, DESIGN §7.2, §5.3)", in, got)
		}
	}
}

func TestPlainTextUnchanged(t *testing.T) {
	got := normalize.Text("buy crypto now")
	if got.Norm != "buy crypto now" {
		t.Errorf("Text(%q).Norm = %+q, want %q (plain lowercase ASCII has nothing to normalize, DESIGN §7.2)", "buy crypto now", got.Norm, "buy crypto now")
	}
	if got.Stripped != 0 {
		t.Errorf("Text(%q).Stripped = %d, want 0 (nothing invisible was removed, DESIGN §7.2 step 2)", "buy crypto now", got.Stripped)
	}
	empty := normalize.Text("")
	if empty.Norm != "" || empty.Stripped != 0 || empty.Raw != "" {
		t.Errorf("Text(\"\") = {Raw:%+q Norm:%+q Stripped:%d}, want all empty/zero (empty input normalizes to empty output)", empty.Raw, empty.Norm, empty.Stripped)
	}
}

func TestNFKCFolding(t *testing.T) {
	cases := []struct{ name, obf, plain string }{
		{"fullwidth", "\U0000FF42\U0000FF55\U0000FF59 \U0000FF43\U0000FF52\U0000FF59\U0000FF50\U0000FF54\U0000FF4F", "buy crypto"},
		{"fullwidth upper", "\U0000FF26\U0000FF32\U0000FF25\U0000FF25", "FREE"},
		{"math bold", "\U0001D41B\U0001D42E\U0001D432 \U0001D41C\U0001D42B\U0001D432\U0001D429\U0001D42D\U0001D428", "buy crypto"},
		{"circled", "\U000024D2\U000024E1\U000024E8\U000024DF\U000024E3\U000024DE", "crypto"},
		{"ligature", "\U0000FB01nance", "finance"},
		{"fullwidth digits", "\U0000FF10\U0000FF15\U0000FF15\U0000FF11\U0000FF12\U0000FF13", "055123"},
		{"decomposed accent", "cafe\U00000301", "café"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertSameNorm(t, tc.obf, tc.plain, 0, "NFKC folds "+tc.name+" to the plain form and is not counted as stripping")
		})
	}
}

func TestInvisiblesStrippedAndCounted(t *testing.T) {
	listed := []rune{
		0x200B, 0x200C, 0x200D, 0x200E, 0x200F,
		0x2060, 0x2061, 0x2062, 0x2063, 0x2064, 0xFEFF,
		0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
		0x2066, 0x2067, 0x2068, 0x2069,
		0xE0000, 0xE0001, 0xE0020, 0xE0041, 0xE007F,
		0xFE00, 0xFE0E, 0xFE0F, 0xE0100, 0xE01EF,
		0x00AD,
	}
	for _, r := range listed {
		obf := interleave("crypto", string(r))
		assertSameNorm(t, obf, "crypto", 5, "each of the 5 inserted invisible characters is stripped and counted")
	}
	mixed := "c\U0000200Br\U0000202Ey\U000000ADp\U0000FE0Ft\U000E0041o"
	assertSameNorm(t, mixed, "crypto", 5, "a mix of zero-width, bidi, soft hyphen, variation selector and tag characters is stripped and counted")
	assertSameNorm(t, "\U0000202Emoc.elgoog", "moc.elgoog", 1, "a bidi override is stripped and counted")
	assertSameNorm(t, "\U0000FEFFbuy crypto now", "buy crypto now", 1, "a byte-order mark is stripped and counted")
}

func TestInvisibleBetweenBaseAndMarkStillComposes(t *testing.T) {
	assertSameNorm(t, "cafe\U0000200B\U00000301", "café", 1, "the accent still composes once the zero-width space between it and its letter is removed")
	assertSameNorm(t, "cafe\U000000AD\U00000301", "café", 1, "the accent still composes once the soft hyphen between it and its letter is removed")
}

func TestExemptionsDoNotCoverObfuscation(t *testing.T) {
	cases := []struct{ name, obf, plain string }{
		{"VS16 after Latin letters", interleave("free", "\U0000FE0F") + "\U0000FE0F", "free"},
		{"ZWJ between Latin letters", interleave("crypto", "\U0000200D"), "crypto"},
		{"ZWNJ between Latin letters", interleave("crypto", "\U0000200C"), "crypto"},
		{"ZWJ between digits", interleave("0551", "\U0000200D"), "0551"},
		{"VS16 after digits without keycap", interleave("0551", "\U0000FE0F"), "0551"},
		{"ZWNJ between Arabic and Latin letters", "ب\U0000200Cx", "بx"},
		{"unterminated flag tag run", "\U0001F3F4\U000E0067\U000E0062", "\U0001F3F4"},
		{"tags after a non-flag emoji", "\U00002764\U000E0067\U000E0062\U000E007F", "\U00002764"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := utf8.RuneCountInString(tc.obf) - utf8.RuneCountInString(tc.plain)
			assertSameNorm(t, tc.obf, tc.plain, want, tc.name+" is obfuscation, not a legitimate emoji or Persian sequence, so every one is counted")
		})
	}
}

func TestOnlyInvisibles(t *testing.T) {
	in := "\U0000200B\U0000200C\U0000200D\U0000FEFF\U00002060\U000000AD"
	got := normalize.Text(in)
	if got.Norm != "" {
		t.Errorf("Text(%+q).Norm = %+q, want empty (text made only of invisible characters has nothing visible to match, DESIGN §7.2 step 2)", in, got.Norm)
	}
	if got.Stripped != 6 {
		t.Errorf("Text(%+q).Stripped = %d, want 6 (every invisible character removed is counted, DESIGN §7.2 step 2)", in, got.Stripped)
	}
}

func TestTagSmugglingCounted(t *testing.T) {
	hidden := "ignore previous"
	var tags strings.Builder
	for _, r := range hidden {
		tags.WriteRune(0xE0000 + r)
	}
	obf := "buy " + tags.String() + "crypto"
	assertSameNorm(t, obf, "buy crypto", len(hidden), "tag characters smuggling hidden ASCII text are stripped and each one is counted")
}

func TestZalgoFloodStripped(t *testing.T) {
	for _, k := range []int{4, 5, 8} {
		assertSameNorm(t, zalgo("crypto", k), "crypto", 6*k, "a run of 4 or more combining marks on a letter is a flood and every mark is stripped and counted")
	}
	long := "c" + strings.Repeat(string(rune(0x0336)), 40) + "rypto"
	assertSameNorm(t, long, "crypto", 40, "a 40-mark flood on one letter is stripped and counted")

	split := "c\U00000334\U00000335\U0000200B\U00000336\U00000337rypto"
	assertSameNorm(t, split, "crypto", 5, "a zero-width space inside a mark run does not split the flood: 1 invisible plus 4 flood marks")

	for _, k := range []int{1, 2, 3} {
		in := zalgo("crypto", k)
		got := normalize.Text(in)
		if got.Stripped != 0 {
			t.Errorf("Text(%+q).Stripped = %d, want 0 (%d combining marks per letter is below the flood threshold of 4 and is kept, DESIGN §7.2 step 2)", in, got.Stripped, k)
		}
	}
}

func TestLegitimateTextNotCounted(t *testing.T) {
	cases := []struct{ name, in string }{
		{"english", "see you at the park tonight, thanks!"},
		{"arabic with tashkeel", "ب\U00000650س\U00000652م\U00000650 الل\U0000064E\U00000651ه\U00000650 الر\U0000064E\U00000651ح\U00000652م\U0000064E\U00000670ن\U00000650 الر\U0000064E\U00000651ح\U00000650يم\U00000650"},
		{"persian zwnj", "می\U0000200Cخواهم کتاب\U0000200Cها"},
		{"hebrew niqqud", "ש\U000005B8\U000005C1לו\U000005B9ם"},
		{"accented latin", "café naïve résumé"},
		{"decomposed latin", "cafe\U00000301 nai\U00000308ve"},
		{"vietnamese", "Tiếng Việt rất đẹp"},
		{"devanagari", "नमस\U0000094Dत\U00000947 द\U00000941न\U0000093Fय\U0000093E"},
		{"heart", "thanks \U00002764\U0000FE0F see you"},
		{"skin tone", "\U0001F44D\U0001F3FD"},
		{"family zwj", "\U0001F468\U0000200D\U0001F469\U0000200D\U0001F467"},
		{"rainbow flag", "\U0001F3F3\U0000FE0F\U0000200D\U0001F308"},
		{"heart on fire", "\U00002764\U0000FE0F\U0000200D\U0001F525"},
		{"technologist", "\U0001F9D1\U0001F3FD\U0000200D\U0001F4BB"},
		{"keycaps", "1\U0000FE0F\U000020E3 #\U0000FE0F\U000020E3 *\U0000FE0F\U000020E3"},
		{"double exclamation", "wow\U0000203C\U0000FE0F"},
		{"copyright", "\U000000A9\U0000FE0F 2026"},
		{"scotland flag", "\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F"},
		{"text-style emoji", "\U00002764\U0000FE0E"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalize.Text(tc.in)
			if got.Stripped != 0 {
				t.Errorf("Text(%+q).Stripped = %d, want 0 (ordinary %s is not obfuscation; counting it would raise a false spam signal, DESIGN §7.2 step 2, §8)", tc.in, got.Stripped, tc.name)
			}
			if r, ok := firstInvisible(got.Norm); ok {
				t.Errorf("Text(%+q).Norm still contains %U, want it removed (exempt sequences are uncounted but still stripped from Norm, DESIGN §7.2 step 2)", tc.in, r)
			}
			if got.Norm == "" {
				t.Errorf("Text(%+q).Norm is empty, want the visible text kept (DESIGN §7.2)", tc.in)
			}
		})
	}
}

func TestIdempotent(t *testing.T) {
	inputs := []string{
		"buy crypto now",
		"\U0000FF42\U0000FF55\U0000FF59 \U0001D41C\U0001D42B\U0001D432\U0001D429\U0001D42D\U0001D428",
		"c\U0000200Br\U0000202Ey\U000000ADp\U0000FE0Ft\U000E0041o",
		zalgo("crypto", 6),
		zalgo("crypto", 3),
		"cafe\U0000200B\U00000301",
		"ب\U00000650س\U00000652م\U00000650 الل\U0000064E\U00000651ه\U00000650",
		"می\U0000200Cخواهم",
		"\U0001F468\U0000200D\U0001F469\U0000200D\U0001F467 1\U0000FE0F\U000020E3",
		"ᄀ" + strings.Repeat("\U00000336", 5) + "ᅡ",
		"\xff\xfe invalid \xc3\x28",
	}
	for _, in := range inputs {
		first := normalize.Text(in)
		second := normalize.Text(first.Norm)
		if second.Norm != first.Norm {
			t.Errorf("Text(Text(%+q).Norm).Norm = %+q, want %+q (normalization is idempotent, so Norm is a stable matching key, DESIGN §7.2)", in, second.Norm, first.Norm)
		}
		if second.Stripped != 0 {
			t.Errorf("Text(Text(%+q).Norm).Stripped = %d, want 0 (Norm has no invisible characters or floods left to strip, DESIGN §7.2 step 2)", in, second.Stripped)
		}
	}
}

func TestLargeHostileInputLinear(t *testing.T) {
	const n = 256 * 1024
	cases := []struct {
		name         string
		in, plain    string
		wantStripped int
	}{
		{"mark flood", "a" + strings.Repeat("\U00000336", n), "a", n},
		{"zero-width between letters", strings.Repeat("a\U0000200B", n/2), strings.Repeat("a", n/2), n / 2},
		{"tag run", "a" + strings.Repeat("\U000E0041", n), "a", n},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			got := normalize.Text(tc.in)
			elapsed := time.Since(start)
			if elapsed > 3*time.Second {
				t.Errorf("Text(%s of %d runes) took %v, want under 3s (normalization runs in the T1 fast lane and must be linear on hostile input, DESIGN §7.1, §7.2)", tc.name, utf8.RuneCountInString(tc.in), elapsed)
			}
			if want := normalize.Text(tc.plain).Norm; got.Norm != want {
				t.Errorf("Text(%s).Norm has %d runes, want %d, the Norm of the visible text only (the hostile characters are stripped, DESIGN §7.2 step 2)", tc.name, utf8.RuneCountInString(got.Norm), utf8.RuneCountInString(want))
			}
			if got.Stripped != tc.wantStripped {
				t.Errorf("Text(%s).Stripped = %d, want %d (each removed input character is counted once, none the normalizer added itself, DESIGN §7.2 step 2)", tc.name, got.Stripped, tc.wantStripped)
			}
		})
	}
}
