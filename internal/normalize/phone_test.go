package normalize

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func phoneE164s(ps []Phone) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.E164)
	}
	return out
}

func TestFindPhonesForms(t *testing.T) {
	const want = "+966551234567"
	for _, in := range []string{
		"+966 55 123 4567",
		"00966551234567",
		"0551234567",
		"(055) 123-4567",
		"055.123.4567",
		"0 5 5 1 2 3 4 5 6 7",
		"０５５１２３４５６７",
		"٠٥٥١٢٣٤٥٦٧",
		"۰۵۵۱۲۳۴۵۶۷",
		"055\u200B123\u200C4567",
		"wa.me/966551234567",
		"https://WA.ME/966551234567?text=hi",
		"zero five five one two three four five six seven",
		"Zero 5 five 1234567",
		"صفر خمسة خمسة واحد اثنين ثلاثة أربعة خمسة ستة سبعة",
		"صفر خمسه خمسه واحد اتنين تلاته اربعه خمسه سته سبعه",
		"call me now: 055 123 4567!",
		"راسلني على ٠٥٥ ١٢٣ ٤٥٦٧ الآن",
	} {
		got := FindPhones(in, "SA")
		if len(got) != 1 || got[0].E164 != want {
			t.Errorf("FindPhones(%+q, SA) = %v, want one %s", in, got, want)
		}
	}
}

func TestFindPhonesRejects(t *testing.T) {
	for _, in := range []string{
		"3000 ريال",
		"1,000,000",
		"price 1000000",
		"2026-10-17",
		"17/10/2026",
		"1.2.2026",
		"12:41",
		"2026",
		"in 1999 and 2026",
		"code 9999",
		"order #12345678",
		"version 1.2.3",
		"I have one two three things",
	} {
		if got := FindPhones(in, "SA"); len(got) != 0 {
			t.Errorf("FindPhones(%q, SA) = %v, want none (prices, dates and short numbers are not phones)", in, got)
		}
	}
}

func TestFindPhonesSplitsGluedNumbers(t *testing.T) {
	for _, in := range []string{
		"055 123 4567 or 056 765 4321",
		"0551234567 0567654321",
		"0551234567, 0567654321",
	} {
		got := phoneE164s(FindPhones(in, "SA"))
		if want := []string{"+966551234567", "+966567654321"}; !slices.Equal(got, want) {
			t.Errorf("FindPhones(%q) = %v, want %v (two numbers must not merge)", in, got, want)
		}
	}
}

func TestFindPhonesRegion(t *testing.T) {
	tests := []struct {
		in, region string
		want       []string
	}{
		{"0551234567", "", nil},
		{"0551234567", "XX", nil},
		{"0551234567", "sa", []string{"+966551234567"}},
		{"+966551234567", "", []string{"+966551234567"}},
		{"00966551234567", "", []string{"+966551234567"}},
		{"00966551234567", "US", []string{"+966551234567"}},
		{"wa.me/966551234567", "", []string{"+966551234567"}},
		{"(415) 555-2671", "US", []string{"+14155552671"}},
		{"0501234567", "AE", []string{"+971501234567"}},
		{"0551234567", "US", nil},
	}
	for _, tt := range tests {
		if got := phoneE164s(FindPhones(tt.in, tt.region)); !slices.Equal(got, tt.want) {
			t.Errorf("FindPhones(%q, %q) = %v, want %v (national numbers need a known region)", tt.in, tt.region, got, tt.want)
		}
	}
}

func TestFindPhonesSpans(t *testing.T) {
	tests := []struct{ in, span string }{
		{"call +966 55 123 4567 now", "+966 55 123 4567"},
		{"x (055) 123-4567.", "(055) 123-4567"},
		{"a 055\u200B1234567 b", "055\u200B1234567"},
		{"zero five five one two three four five six seven!", "zero five five one two three four five six seven"},
		{"wa.me/966551234567?x", "966551234567"},
	}
	for _, tt := range tests {
		got := FindPhones(tt.in, "SA")
		if len(got) != 1 {
			t.Fatalf("FindPhones(%+q) = %v, want one", tt.in, got)
		}
		if s := tt.in[got[0].Start:got[0].End]; s != tt.span {
			t.Errorf("span of %+q = %+q, want %+q (redaction masks the whole written number)", tt.in, s, tt.span)
		}
	}
}

func TestParsePhone(t *testing.T) {
	good := []string{
		"+966551234567", " 055 123 4567 ", "٠٥٥١٢٣٤٥٦٧", "00966551234567", "(055) 123-4567",
		"wa.me/966551234567", "https://wa.me/966551234567", "https://wa.me/+966551234567?text=hi",
		"https://api.whatsapp.com/send?phone=966551234567&text=x",
	}
	for _, s := range good {
		if got, err := ParsePhone(s, "SA"); err != nil || got != "+966551234567" {
			t.Errorf("ParsePhone(%q) = %q, %v; want +966551234567", s, got, err)
		}
	}
	bad := []string{
		"", "abc", "12345", "0551234567 0567654321", "call 0551234567", "+966551234567x", "3000",
		"evil.com/966551234567", "wa.me/966551234567 and more",
	}
	for _, s := range bad {
		if got, err := ParsePhone(s, "SA"); !errors.Is(err, ErrInvalidPhone) {
			t.Errorf("ParsePhone(%q) = %q, %v; want ErrInvalidPhone (invalid input is reported, not dropped)", s, got, err)
		}
	}
	if _, err := ParsePhone("0551234567", ""); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("ParsePhone national without region: err = %v, want ErrInvalidPhone", err)
	}
}

func TestFindPhonesNotHiddenByNoise(t *testing.T) {
	var noise strings.Builder
	for i := range 2000 {
		noise.WriteByte(byte('0' + (i*7+3)%10))
		noise.WriteByte(' ')
	}
	for _, tail := range []string{
		"call +966 55 123 4567",
		"zero five five one two three four five six seven",
		"wa.me/966551234567",
	} {
		got := phoneE164s(FindPhones(noise.String()+tail, "SA"))
		if !slices.Contains(got, "+966551234567") {
			t.Errorf("FindPhones(4 KB of digit noise + %q) = %v, want +966551234567 (noise must not hide a marked number)", tail, got)
		}
	}
}

func TestFindPhonesHostileInputBounded(t *testing.T) {
	for name, in := range map[string]string{
		"spaced digits": strings.Repeat("1 ", 32<<10),
		"varied digits": strings.Repeat("5 0 9 3 7 2 8 4 6 1 ", 3<<10),
		"dashes":        strings.Repeat("12-", 21<<10),
		"words":         strings.Repeat("five ", 13<<10),
		"plus":          strings.Repeat("+9", 32<<10),
	} {
		start := time.Now()
		FindPhones(in, "SA")
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("FindPhones(%s) took %v, want under 3s", name, d)
		}
	}
}

var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

func FuzzFindPhones(f *testing.F) {
	for _, s := range []string{
		"+966 55 123 4567", "0 5 5 1 2 3 4 5 6 7", "zero five five", "٠٥٥١٢٣٤٥٦٧", "wa.me/966551234567",
		"055 123 4567 or 056 765 4321", "2026-10-17", "\xff+00",
	} {
		f.Add(s, "SA")
	}
	f.Fuzz(func(t *testing.T, in, region string) {
		ps := FindPhones(in, region)
		last := 0
		for _, p := range ps {
			if p.Start < last || p.End <= p.Start || p.End > len(in) {
				t.Fatalf("FindPhones(%+q) span %d..%d out of order or bounds (last end %d)", in, p.Start, p.End, last)
			}
			if !e164Re.MatchString(p.E164) {
				t.Fatalf("FindPhones(%+q) E164 = %q is not E.164", in, p.E164)
			}
			last = p.End
		}
		if e, err := ParsePhone(in, region); err == nil && !e164Re.MatchString(e) {
			t.Fatalf("ParsePhone(%+q) = %q is not E.164", in, e)
		}
	})
}
