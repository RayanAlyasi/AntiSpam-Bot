package blacklist

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeNumber(t *testing.T) {
	tests := []struct {
		in, country, want string
	}{
		{"+966 55 123 4567", "", "+966551234567"},
		{"0551234567", "SA", "+966551234567"},
		{"00966551234567", "", "+966551234567"},
		{"٠٥٥١٢٣٤٥٦٧", "SA", "+966551234567"},
		{"+٩٦٦٥٥١٢٣٤٥٦٧", "", "+966551234567"},
		{"wa.me/966551234567", "", "+966551234567"},
		{"https://wa.me/+966551234567?text=hi", "", "+966551234567"},
		{"https://api.whatsapp.com/send?phone=966551234567", "", "+966551234567"},
		{"966551234567", "SA", "+966551234567"},
		{"+966 0551234567", "", "+966551234567"},
		{"(202) 555-0147", "US", "+12025550147"},
		{"07700 900123", "GB", "+447700900123"},
		{"\u200e+20 100 123 4567", "", "+201001234567"},
	}
	for _, tt := range tests {
		got, err := normalizeNumber(tt.in, tt.country)
		if err != nil || got != tt.want {
			t.Errorf("normalize(%q, %q) = %q, %v; want %q (DESIGN §9.8 accepted formats)", tt.in, tt.country, got, err, tt.want)
		}
	}
}

func TestNormalizeNumberInvalid(t *testing.T) {
	for _, tt := range []struct{ in, country string }{
		{"", "SA"},
		{"+12", ""},
		{"abc", "SA"},
		{"0551234567", ""},
		{"12345", "SA"},
		{"0412345678", "SA"},
		{"+1234567890123456", ""},
		{"hello 0551234567", "SA"},
	} {
		if got, err := normalizeNumber(tt.in, tt.country); !errors.Is(err, ErrInvalid) {
			t.Errorf("normalize(%q, %q) = %q, %v; want ErrInvalid (invalid numbers are reported, not dropped)", tt.in, tt.country, got, err)
		}
	}
}

func FuzzNormalizeNumber(f *testing.F) {
	for _, s := range []string{"+966 55 123 4567", "0551234567", "wa.me/1", "٠٥٥", "00+"} {
		f.Add(s, "SA")
	}
	f.Fuzz(func(t *testing.T, in, country string) {
		got, err := normalizeNumber(in, country)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("normalize(%q) error %v doesn't wrap ErrInvalid", in, err)
			}
			return
		}
		if !strings.HasPrefix(got, "+") || len(got) < 9 || len(got) > 16 || got[1] == '0' {
			t.Fatalf("normalize(%q) = %q, want E.164 (+, 8–15 digits, no leading 0)", in, got)
		}
		for _, r := range got[1:] {
			if r < '0' || r > '9' {
				t.Fatalf("normalize(%q) = %q contains %q", in, got, r)
			}
		}
		if again, err := normalizeNumber(got, ""); err != nil || again != got {
			t.Fatalf("normalize is not idempotent: %q → %q → %q, %v", in, got, again, err)
		}
	})
}
