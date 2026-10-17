package redact

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

var key = []byte("test-key")

func TestTextMasks(t *testing.T) {
	o := Options{Region: "SA", Names: []string{"Ahmed", "سارة", "x"}}
	tests := []struct{ in, want string }{
		{"call 0551234567 now", "call [phone] now"},
		{"call +966 55 123 4567", "call [phone]"},
		{"call ٠٥٥١٢٣٤٥٦٧", "call [phone]"},
		{"zero five five one two three four five six seven ok", "[phone] ok"},
		{"call 055\u200B1234567", "call [phone]"},
		{"uk +44 20 7946 0958", "uk [phone]"},
		{"id 123456789012", "id [phone]"},
		{"id 123-456-789", "id [phone]"},
		{"id ١٢٣٤٥٦٧٨٩٠١", "id [phone]"},
		{"mail ali@example.com.", "mail [email]."},
		{"mailto:ali@example.com", "[email]"},
		{"to a.b+c@mail.example.co.uk, thanks", "to [email], thanks"},
		{"user@localhost stays", "user@localhost stays"},
		{"ａｌｉ@ｅｘａｍｐｌｅ.ｃｏｍ", "[email]"},
		{"ali\u200B@example.com", "[email]"},
		{"hi @966551234567 and @12345678", "hi [mention] and [mention]"},
		{"Ahmed and AHMED and ahmed's", "[name] and [name] and [name]'s"},
		{"Ahmedov stays", "Ahmedov stays"},
		{"مرحبا سارة", "مرحبا [name]"},
		{"x marks", "x marks"},
		{"nine nine nine one two three four five six seven", "[phone]"},
		{"one two three things", "one two three things"},
		{"call [phone:966551234567] now", "call [phone] now"},
		{"(055) 123-4567", "[phone]"},
	}
	for _, tt := range tests {
		if got := Text(tt.in, o); got != tt.want {
			t.Errorf("Text(%+q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTextKeeps(t *testing.T) {
	for _, in := range []string{
		"3000 ريال",
		"2026-10-17",
		"17/10/2026",
		"code 1234",
		"price 1,000,000",
		"see bit.ly/x and https://example.com/a",
		"12:41 today",
		"[phone] [email] [mention] [name] [phone:0123456789ab]",
	} {
		if got := Text(in, Options{Region: "SA", Names: []string{"phone", "name"}}); got != in {
			t.Errorf("Text(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestAdvertisedNumbersHashed(t *testing.T) {
	o := Options{Region: "SA", Key: key, Advertised: []string{"+966551234567"}}
	a := Text("buy now 0551234567", o)
	b := Text("buy now +966 55 123 4567", o)
	id := PhoneHash(key, "+966551234567")[:12]
	if a != "buy now [phone:"+id+"]" || a != b {
		t.Errorf("advertised number = %q and %q, want both %q (same number, same token)", a, b, "buy now [phone:"+id+"]")
	}
	other := Text("buy now 0551234567", Options{Region: "SA", Key: []byte("other"), Advertised: o.Advertised})
	if other == a {
		t.Errorf("different keys gave the same token %q", other)
	}
	if got := Text("buy 0551234567 or 0567654321", o); got != "buy [phone:"+id+"] or [phone]" {
		t.Errorf("third-party number = %q, want it masked as [phone]", got)
	}
	if got := Text("buy 0551234567", Options{Region: "SA", Advertised: o.Advertised}); got != "buy [phone]" {
		t.Errorf("empty key = %q, want [phone] (a hash under an empty key is reversible)", got)
	}
}

func TestPhoneHash(t *testing.T) {
	h := PhoneHash(key, "+966551234567")
	if len(h) != 64 || strings.ToLower(h) != h || h == PhoneHash([]byte("k2"), "+966551234567") {
		t.Errorf("PhoneHash = %q, want 64 lowercase hex chars that depend on the key", h)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"hello world", 0, "hello world"},
		{"hello world", 11, "hello world"},
		{"hello world", 6, "hello…"},
		{"hello world", 1, "…"},
		{"call 0551234567 now", 8, "call …"},
		{"call 0551234567 now", 13, "call [phone]…"},
		{"cafe\u0301 au lait", 6, "cafe\u0301…"},
		{"cafe\u0301 au lait", 4, "caf…"},
		{"ابجد هوز", 4, "ابج…"},
	}
	for _, tt := range tests {
		got := Text(tt.in, Options{Region: "SA", MaxRunes: tt.max})
		if got != tt.want {
			t.Errorf("Text(%+q, MaxRunes %d) = %+q, want %+q", tt.in, tt.max, got, tt.want)
		}
		if tt.max > 0 && utf8.RuneCountInString(got) > tt.max {
			t.Errorf("Text(%+q, MaxRunes %d) has %d runes", tt.in, tt.max, utf8.RuneCountInString(got))
		}
	}
}

func TestTextBounded(t *testing.T) {
	for name, in := range map[string]string{
		"digits":  strings.Repeat("1 ", 128<<10),
		"emails":  strings.Repeat("a@b.co ", 36<<10),
		"names":   strings.Repeat("Ahmed ", 42<<10),
		"at":      strings.Repeat("@", 256<<10),
		"letters": strings.Repeat("Ahmedx", 42<<10),
	} {
		start := time.Now()
		Text(in, Options{Region: "SA", Names: []string{"Ahmed", "Ahmedx"}})
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("Text(%s) took %v, want under 3s", name, d)
		}
	}
}

func FuzzText(f *testing.F) {
	for _, s := range []string{
		"call 0551234567", "ali@example.com", "@966551234567", "Ahmed", "٠٥٥١٢٣٤٥٦٧", "[phone] 123",
		"a\xffb 055 123 4567", "zero five five one two three four five six seven", "1.2.3.4.5.6.7.8.9",
	} {
		f.Add(s, 20)
	}
	f.Fuzz(func(t *testing.T, in string, maxRunes int) {
		maxRunes = max(0, maxRunes%200)
		o := Options{Region: "SA", Key: key, Advertised: []string{"+966551234567"}, Names: []string{"Ahmed"}, MaxRunes: maxRunes}
		got := Text(in, o)
		if !utf8.ValidString(got) {
			t.Fatalf("Text(%+q) = %+q is not valid UTF-8", in, got)
		}
		if maxRunes > 0 && utf8.RuneCountInString(got) > maxRunes {
			t.Fatalf("Text(%+q, %d) = %+q is too long", in, maxRunes, got)
		}
		if again := Text(got, o); again != got {
			t.Fatalf("Text is not idempotent: %+q then %+q", got, again)
		}
		marks := placeholderRe.FindAllStringIndex(got, -1)
		inside := func(a, b int) bool {
			for _, m := range marks {
				if a >= m[0] && b <= m[1] {
					return true
				}
			}
			return false
		}
		for _, p := range normalize.FindPhones(got, o.Region) {
			if !inside(p.Start, p.End) {
				t.Fatalf("Text(%+q) = %+q leaves phone %s unmasked", in, got, p.E164)
			}
		}
		for _, sp := range newView(got).emailSpans() {
			if !inside(sp.start, sp.end) {
				t.Fatalf("Text(%+q) = %+q leaves an email unmasked", in, got)
			}
		}
	})
}
