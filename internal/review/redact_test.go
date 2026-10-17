package review

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMaskNumber(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"+966512345612", "+966 5•• ••• •12"},
		{"+447700900123", "+44 7 ••• ••• •23"},
		{"+12025550147", "+1 2 ••• ••• •47"},
		{"+201001234567", "+20 1 ••• ••• •67"},
		{"0551234567", "0 ••• ••• •67"},
		{"+٩٦٦٥١٢٣٤٥٦١٢", "+966 5•• ••• •12"},
		{"12", "••"},
	}
	for _, tt := range tests {
		if got := maskNumber(tt.in); got != tt.want {
			t.Errorf("maskNumber(%q) = %q, want %q (only the first national digit and the last two stay visible)", tt.in, got, tt.want)
		}
	}
}

func TestCallingCodeLen(t *testing.T) {
	tests := map[string]int{
		"1202": 1, "7495": 1, "2010": 2, "2783": 2, "2126": 3, "3069": 2, "3519": 3, "4477": 2, "4206": 3,
		"5511": 2, "5021": 3, "6012": 2, "6712": 3, "8613": 2, "8801": 3, "9055": 2, "9665": 3, "9715": 3, "9891": 2,
	}
	for in, want := range tests {
		if got := callingCodeLen(in); got != want {
			t.Errorf("callingCodeLen(%q) = %d, want %d (ITU E.164 zones)", in, got, want)
		}
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		name, in   string
		gone, kept []string
	}{
		{"international number", "call +966 55 123 4567 now", []string{"123 4567", "551234567"}, []string{"call", "now", "67"}},
		{"00 prefix", "واتساب 00966551234567", []string{"551234"}, []string{"واتساب"}},
		{"arabic digits", "رقمي ٠٥٥١٢٣٤٥٦٧", []string{"٠٥٥١٢٣٤٥٦٧"}, []string{"رقمي"}},
		{"email", "mail ahmed.k@gmail.com please", []string{"ahmed.k"}, []string{"@gmail.com", "please"}},
		{"price stays", "only 3000 riyal, 50% off", nil, []string{"3000", "50%"}},
		{"short code stays", "code 123456", nil, []string{"123456"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redact(tt.in)
			for _, g := range tt.gone {
				if strings.Contains(got, g) {
					t.Errorf("redact(%q) = %q still contains %q (third-party contacts are masked, DESIGN §9.5)", tt.in, got, g)
				}
			}
			for _, k := range tt.kept {
				if !strings.Contains(got, k) {
					t.Errorf("redact(%q) = %q lost %q (only contacts are masked)", tt.in, got, k)
				}
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("اربح ", 200)
	got := truncate(long, MaxEvidenceText)
	if n := utf8.RuneCountInString(got); n > MaxEvidenceText {
		t.Errorf("truncate gave %d runes, want at most %d", n, MaxEvidenceText)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate(%q…) = %q, want a trailing … when text was cut", long[:20], got[len(got)-10:])
	}
	if got := truncate("a\n\nb   c", 10); got != "a b c" {
		t.Errorf("truncate collapses whitespace: got %q, want %q", got, "a b c")
	}
}

var longDigits = regexp.MustCompile(`[0-9]{8,}`)

// FuzzRedact checks that no run of eight or more digits survives redaction, whatever the
// separators or digit scripts.
func FuzzRedact(f *testing.F) {
	for _, s := range []string{"+966 55 123 4567", "٠٥٥١٢٣٤٥٦٧", "a@b.co", "1-2-3-4-5-6-7-8-9", "00966551234567"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := redact(s)
		if !utf8.ValidString(s) {
			return
		}
		if m := longDigits.FindString(digitsOnlyRuns(got)); m != "" {
			t.Fatalf("redact(%q) = %q leaves the digit run %q (phone numbers must be masked)", s, got, m)
		}
	})
}

// digitsOnlyRuns folds digits of every script to ASCII and drops the separators a phone number
// may use, so the check sees the number a reader would.
func digitsOnlyRuns(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')' || r == '+':
		default:
			b.WriteRune('x')
		}
	}
	return b.String()
}
