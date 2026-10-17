package adapter

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const businessVCard = "BEGIN:VCARD\r\nVERSION:3.0\r\nN:;Crypto Gains;;;\r\nFN:Crypto Gains\r\nORG:Crypto Gains;Sales\r\n" +
	"TITLE:Investment advisor\r\nitem1.TEL;type=CELL;waid=966501234567:+966 50 123 4567\r\nitem1.X-ABLabel:Mobile\r\n" +
	"X-WA-BIZ-NAME:Crypto Gains Official\r\nX-WA-BIZ-DESCRIPTION:Earn 500% weekly\\, guaranteed!\\nDM now\r\n" +
	"URL:https://cryptogains.example\r\nEND:VCARD"

func TestParseVCardBusinessCard(t *testing.T) {
	c := ParseVCard(businessVCard)
	tests := []struct{ name, want string }{
		{"FN", "Crypto Gains"},
		{"N", "Crypto Gains"},
		{"ORG", "Crypto Gains Sales"},
		{"TITLE", "Investment advisor"},
		{"X-WA-BIZ-NAME", "Crypto Gains Official"},
		{"X-WA-BIZ-DESCRIPTION", "Earn 500% weekly, guaranteed!\nDM now"},
		{"URL", "https://cryptogains.example"},
		{"x-ablabel", "Mobile"},
		{"VERSION", ""}, // envelope lines are not properties
	}
	for _, tt := range tests {
		if got := c.First(tt.name); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, got, tt.want)
		}
	}
	phones, waids := c.Phones()
	if !slices.Equal(phones, []string{"+966 50 123 4567"}) || !slices.Equal(waids, []string{"966501234567"}) {
		t.Errorf("Phones() = %q, %q; want the TEL value and its waid (the advertised number)", phones, waids)
	}
}

func TestParseVCardTolerant(t *testing.T) {
	tests := []struct {
		name, in, prop, want string
	}{
		{"folded line", "FN:Very long\r\n  name\r\nEND:VCARD", "FN", "Very long name"},
		{"no envelope", "FN:Bare", "FN", "Bare"},
		{"lf only", "BEGIN:VCARD\nFN:Unix\nEND:VCARD", "FN", "Unix"},
		{"lower case name", "fn:lower", "FN", "lower"},
		{"quoted param with colon", `TEL;X-LABEL="a:b";waid=1:+1`, "TEL", "+1"},
		{"quoted-printable", "NOTE;ENCODING=QUOTED-PRINTABLE:Caf=C3=A9 =\r\nnow", "NOTE", "Café now"},
		{"bad qp escape", "NOTE;ENCODING=QUOTED-PRINTABLE:100=ZZ", "NOTE", "100=ZZ"},
		{"garbage line skipped", "no colon here\nFN:ok", "FN", "ok"},
		{"empty name skipped", ":value\nFN:ok", "FN", "ok"},
		{"second card ignored", "BEGIN:VCARD\nFN:one\nEND:VCARD\nBEGIN:VCARD\nFN:two\nEND:VCARD", "FN", "one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseVCard(tt.in).First(tt.prop); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.prop, got, tt.want)
			}
		})
	}
}

func TestVCardWAIDsDeduplicated(t *testing.T) {
	c := ParseVCard("TEL;waid=1-23:+123\nTEL;type=WORK;waid=123:+1 23\nTEL:+999")
	phones, waids := c.Phones()
	if len(phones) != 3 || !slices.Equal(waids, []string{"123"}) {
		t.Errorf("Phones() = %q, %q; want 3 phones and one waid", phones, waids)
	}
}

func TestParseVCardBounded(t *testing.T) {
	c := ParseVCard(strings.Repeat("NOTE:x\n", 10_000))
	if len(c.Properties) != MaxVCardProperties {
		t.Errorf("kept %d properties, want the %d cap (a crafted card must not cost more)", len(c.Properties), MaxVCardProperties)
	}
}

func TestParseVCardLongFolds(t *testing.T) {
	// Thousands of continuation lines must join correctly (and in linear time).
	folded := "NOTE:" + strings.Repeat("ab\r\n ", 5000) + "end"
	qp := "NOTE;ENCODING=QUOTED-PRINTABLE:" + strings.Repeat("ab=\r\n", 5000) + "end"
	for _, in := range []string{folded, qp} {
		if got := ParseVCard(in).First("NOTE"); got != strings.Repeat("ab", 5000)+"end" {
			t.Errorf("NOTE has %d bytes, want %d", len(got), 10003)
		}
	}
}

func FuzzParseVCard(f *testing.F) {
	f.Add(businessVCard)
	f.Add("NOTE;ENCODING=QUOTED-PRINTABLE:=\n=\n=")
	f.Add("TEL;waid=\"x:y\":1\\")
	f.Fuzz(func(t *testing.T, s string) {
		c := ParseVCard(s)
		if len(c.Properties) > MaxVCardProperties {
			t.Fatalf("%d properties, cap is %d", len(c.Properties), MaxVCardProperties)
		}
		for _, p := range c.Properties {
			if p.Name == "" || p.Name != strings.ToUpper(p.Name) {
				t.Fatalf("bad property name %q", p.Name)
			}
			if utf8.ValidString(s) && len(p.Value) > len(s) {
				t.Fatalf("value longer than the input: %q", p.Value)
			}
		}
		c.Phones()
	})
}
