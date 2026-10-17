package logging

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		// Saudi Arabia in every accepted format.
		{"+966512345612", "+966 5•• ••• •12"},
		{"966512345612", "+966 5•• ••• •12"},
		{"+966 51 234 5612", "+966 5•• ••• •12"},
		{"00966512345612", "+966 5•• ••• •12"},
		{"+966-51-234-5612", "+966 5•• ••• •12"},
		{"966.51.234.5612", "+966 5•• ••• •12"},
		{"+966 (51) 234-5612", "+966 5•• ••• •12"},
		{"  +966512345612\n", "+966 5•• ••• •12"},
		// One-digit codes.
		{"+1 202 555 0123", "+1 2•• ••• ••2 3"},
		{"+7 912 345 67 89", "+7 9•• ••• ••8 9"},
		// Two-digit codes.
		{"+20 10 1234 5678", "+20 1•• ••• ••7 8"},
		{"+44 7911 123456", "+44 7•• ••• ••5 6"},
		{"+49 1512 3456789", "+49 1•• ••• ••• 89"},
		{"+55 11 91234 5678", "+55 1•• ••• ••• 78"},
		{"+81 90 1234 5678", "+81 9•• ••• ••7 8"},
		{"+86 138 0013 8000", "+86 1•• ••• ••• 00"},
		{"+90 532 123 45 67", "+90 5•• ••• ••6 7"},
		{"+91 98765 43210", "+91 9•• ••• ••1 0"},
		{"+92 300 1234567", "+92 3•• ••• ••6 7"},
		{"+98 912 345 6789", "+98 9•• ••• ••8 9"},
		// Three-digit codes, including ones whose first two digits are a two-digit code's
		// neighbours (97x vs 98, 96x vs 95).
		{"+971 50 123 4567", "+971 5•• ••• •67"},
		{"+965 5123 4567", "+965 5•• ••• 67"},
		{"+973 3612 3456", "+973 3•• ••• 56"},
		{"+234 803 123 4567", "+234 8•• ••• ••6 7"},
		{"+299 12 34 56", "+299 1•• •56"},
		// Never echoed.
		{"", ""},
		{"   ", Masked},
		{"hello", Masked},
		{"12345", Masked},
		{"+299 1234 5", Masked},                 // national number too short to hide anything
		{"+966 5123abc", Masked},                // letters
		{"0512345678", Masked},                  // trunk prefix, no country code
		{"+0966512345612", Masked},              // no country code starts with 0
		{"++966512345612", Masked},              // "+" only at the start
		{"966+512345612", Masked},               // "+" only at the start
		{"9665123456123456", Masked},            // 16 digits, over the E.164 limit
		{"966512345612@s.whatsapp.net", Masked}, // a JID, not a phone: use MaskJID
	}
	for _, tt := range tests {
		if got := MaskPhone(tt.in); got != tt.want {
			t.Errorf("MaskPhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMaskJID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"966512345612@s.whatsapp.net", "+966 5•• ••• •12@s.whatsapp.net"},
		{"966512345612:12@s.whatsapp.net", "+966 5•• ••• •12:12@s.whatsapp.net"},
		{"966512345612.1:3@s.whatsapp.net", "+966 5•• ••• •12.1:3@s.whatsapp.net"},
		{"966512345612@c.us", "+966 5•• ••• •12@c.us"},
		{"966512345612:2@hosted", "+966 5•• ••• •12:2@hosted"},
		{"966512345612", "+966 5•• ••• •12"},
		{"", ""},
		// Not phone numbers: readable.
		{"123456789012345@lid", "123456789012345@lid"},
		{"123456789012345:4@lid", "123456789012345:4@lid"},
		{"120363012345678901@g.us", "120363012345678901@g.us"},
		{"status@broadcast", "status@broadcast"},
		{"120363098765432101@newsletter", "120363098765432101@newsletter"},
		// Old-style group JIDs embed the creator's phone.
		{"966512345612-1600000000@g.us", "+966 5•• ••• •12-1600000000@g.us"},
		// Malformed: never echoed.
		{"abc@s.whatsapp.net", Masked + "@s.whatsapp.net"},
		{"966512345612:abc@s.whatsapp.net", Masked + "@s.whatsapp.net"},
		{"abc:966512345612@s.whatsapp.net", Masked + "@s.whatsapp.net"},
		{"966512345612:1234@s.whatsapp.net", Masked + "@s.whatsapp.net"},
		{"x-966512345612@g.us", Masked + "@g.us"},
		{"call +966512345612@lid", Masked + "@lid"},
		{"966512345612@example.com", Masked},
		{"966512345612@lid@s.whatsapp.net", Masked},
	}
	for _, tt := range tests {
		if got := MaskJID(tt.in); got != tt.want {
			t.Errorf("MaskJID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPhoneAndJIDMaskWhenLogged(t *testing.T) {
	const raw = "966512345612"
	const masked = "+966 5•• ••• •12"
	tests := []struct {
		name string
		log  func(zerolog.Logger)
	}{
		{"Stringer phone", func(l zerolog.Logger) { l.Info().Stringer("phone", Phone("+"+raw)).Msg("") }},
		{"Interface phone", func(l zerolog.Logger) { l.Info().Interface("phone", Phone(raw)).Msg("") }},
		{"Stringer jid", func(l zerolog.Logger) { l.Info().Stringer("jid", JID(raw+"@s.whatsapp.net")).Msg("") }},
		{"Interface jid", func(l zerolog.Logger) { l.Info().Interface("jid", JID(raw+":3@s.whatsapp.net")).Msg("") }},
		{"Msgf phone", func(l zerolog.Logger) { l.Info().Msgf("from %v", Phone(raw)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.log(zerolog.New(&buf))
			out := buf.String()
			if strings.Contains(out, raw) || strings.Contains(out, "51234") {
				t.Fatalf("log line %q contains the phone number; it must be masked (personal data)", out)
			}
			if !strings.Contains(out, masked) {
				t.Fatalf("log line %q doesn't contain %q", out, masked)
			}
		})
	}
	if got := fmt.Sprint(Phone(raw)); got != masked {
		t.Errorf("fmt.Sprint(Phone) = %q, want %q", got, masked)
	}
}

// digitRuns returns the maximal runs of ASCII digits in s.
func digitRuns(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
}

func FuzzMaskPhone(f *testing.F) {
	for _, s := range []string{
		"+966512345612", "966512345612", "+966 51 234 5612", "00966512345612", "+1 202 555 0123",
		"+7 912 345 67 89", "+49 1512 3456789", "+299 12 34 56", "123456789012345", "0512345678",
		"hello", "", "+", "00", "1", "+44 (0) 7911 123456",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := MaskPhone(in)
		if out == "" {
			if in != "" {
				t.Fatalf("MaskPhone(%q) = \"\", want a mask (only empty input stays empty)", in)
			}
			return
		}
		if out == Masked {
			return
		}
		for _, r := range out {
			if r != '+' && r != ' ' && r != '•' && (r < '0' || r > '9') {
				t.Fatalf("MaskPhone(%q) = %q has %q; output must be only +, digits, spaces and • (no input echoed)", in, out, r)
			}
		}
		runs := digitRuns(out)
		if !strings.HasPrefix(out, "+"+runs[0]+" ") || len(runs[0]) > 3 {
			t.Fatalf("MaskPhone(%q) = %q, want it to start with \"+<1-3 digit country code> \"", in, out)
		}
		visible := 0
		for _, run := range runs[1:] {
			if len(run) > 2 {
				t.Fatalf("MaskPhone(%q) = %q shows %q; no more than two national digits may be adjacent", in, out, run)
			}
			visible += len(run)
		}
		if visible > 3 {
			t.Fatalf("MaskPhone(%q) = %q shows %d national digits, want at most 3 (first and last two)", in, out, visible)
		}
	})
}

func FuzzMaskJID(f *testing.F) {
	for _, s := range []string{
		"966512345612@s.whatsapp.net", "966512345612:12@s.whatsapp.net", "966512345612.1:3@s.whatsapp.net",
		"123456789012345@lid", "966512345612-1600000000@g.us", "abc:966512345612@s.whatsapp.net",
		"966512345612", "@", "@s.whatsapp.net",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := MaskJID(in)
		user, server, hasServer := strings.Cut(out, "@")
		switch {
		case !hasServer, server == "s.whatsapp.net", server == "c.us", server == "hosted":
		default:
			return // opaque IDs are meant to stay readable
		}
		for _, run := range digitRuns(user) {
			if len(run) > 3 {
				t.Fatalf("MaskJID(%q) = %q shows %q; a phone-number JID must never show 4+ adjacent digits", in, out, run)
			}
		}
	})
}
