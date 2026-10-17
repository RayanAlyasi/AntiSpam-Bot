package normalize_test

import (
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

func FuzzText(f *testing.F) {
	seeds := []string{
		"",
		"buy crypto now",
		"c\U0000200Br\U0000202Ey\U000000ADp\U0000FE0Ft\U000E0041o",
		"\U0000FF42\U0000FF55\U0000FF59 \U0001D41C\U0001D42B\U0001D432\U0001D429\U0001D42D\U0001D428",
		zalgo("crypto", 9),
		zalgo("crypto", 3),
		"cafe\U0000200B\U00000301",
		"\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F",
		"\U0001F3F4\U000E0067\U000E0062",
		"\U0001F468\U0000200D\U0001F469\U0000200D\U0001F467",
		"1\U0000FE0F\U000020E3",
		"\U00000645\U000006CC\U0000200C\U0000062E\U00000648\U00000627\U00000647\U00000645",
		"\U00001100\U00000336\U00000336\U00000336\U00000336\U00000336\U00001161",
		"\U0000FDFA\U0000FB01\U00002122\U0000FE0F",
		"\U00000301\U00000301\U00000301\U00000301\U00000301",
		"\xff\xfe\xc3\x28",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := normalize.Text(in)
		if got.Raw != in {
			t.Fatalf("Text(%+q).Raw = %+q, want the input unchanged (Raw is kept for evidence, DESIGN §7.2)", in, got.Raw)
		}
		if !utf8.ValidString(got.Norm) {
			t.Fatalf("Text(%+q).Norm = %+q is not valid UTF-8, want valid UTF-8 (matchers and storage need well-formed text, DESIGN §7.2)", in, got.Norm)
		}
		if !norm.NFKC.IsNormalString(got.Norm) {
			t.Fatalf("Text(%+q).Norm = %+q is not NFKC-normal, want NFKC (step 1 of DESIGN §7.2)", in, got.Norm)
		}
		if r, ok := firstInvisible(got.Norm); ok {
			t.Fatalf("Text(%+q).Norm contains %U, want every listed invisible character removed (DESIGN §7.2 step 2)", in, r)
		}
		if n := utf8.RuneCountInString(in); got.Stripped < 0 || got.Stripped > n {
			t.Fatalf("Text(%+q).Stripped = %d, want between 0 and %d, the input's rune count (only removed input characters are counted, DESIGN §7.2 step 2)", in, got.Stripped, n)
		}
		again := normalize.Text(got.Norm)
		if again.Norm != got.Norm || again.Stripped != 0 {
			t.Fatalf("Text(Text(%+q).Norm) = {Norm:%+q Stripped:%d}, want {Norm:%+q Stripped:0} (normalization is idempotent, DESIGN §7.2)", in, again.Norm, again.Stripped, got.Norm)
		}
	})
}
