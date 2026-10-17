package textnorm

import (
	"fmt"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

func TestPrepareFillsEverySurface(t *testing.T) {
	raw := "Ｆｒｅｅ fr\u200b33 bitcоin 💰 call 055 123 4567"
	e := envelope.Envelope{Texts: []envelope.TextSurface{
		{Source: envelope.SourceBody, Raw: raw},
		{Source: envelope.SourceCaption, Raw: "مدرسة"},
		{Source: envelope.SourcePollOption, Raw: ""},
	}}
	Prepare(&e, "SA")
	b := e.Texts[0]
	if b.Raw != raw {
		t.Fatalf("Raw changed to %q, want it kept byte for byte for evidence", b.Raw)
	}
	if b.Norm == "" || b.Norm == raw {
		t.Errorf("Norm = %q, want the normalized text", b.Norm)
	}
	if b.Stripped == 0 {
		t.Error("Stripped = 0, want the zero-width space counted")
	}
	if b.Skeleton == "" || b.Leet == "" || b.Loose == "" {
		t.Errorf("variants not filled: %+v", b)
	}
	if len(b.EmojiTokens) == 0 {
		t.Error("EmojiTokens empty, want the money bag token")
	}
	if e.Texts[1].Loose == e.Texts[1].Norm {
		t.Errorf("Arabic Loose = Norm = %q, want ة folded in Loose only", e.Texts[1].Norm)
	}
	if got := fmt.Sprint(e.Phones); got != "[{+966551234567 body}]" {
		t.Errorf("Phones = %s, want the body's number", got)
	}
}

func TestPrepareContactNumbers(t *testing.T) {
	e := envelope.Envelope{
		Texts: []envelope.TextSurface{{Source: "body", Raw: "+966551234567"}},
		Contacts: []envelope.ContactRef{
			{WAIDs: []string{"966551234567", "12025550123", "garbage"}, Phones: []string{"056 123 4567", "+1 202-555-0123", "12"}},
		},
	}
	Prepare(&e, "SA")
	want := "[{+966551234567 body} {+12025550123 vcard} {+966561234567 vcard}]"
	if got := fmt.Sprint(e.Phones); got != want {
		t.Errorf("Phones = %s, want %s (each number once, invalid ones skipped)", got, want)
	}
}

func TestPrepareIsIdempotent(t *testing.T) {
	e := envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: "call 0551234567 f.r.e.e"}}}
	Prepare(&e, "SA")
	first := fmt.Sprintf("%+v", e)
	Prepare(&e, "SA")
	if second := fmt.Sprintf("%+v", e); second != first {
		t.Errorf("second Prepare changed the envelope:\n%s\n%s", first, second)
	}
}

func TestPrepareWithoutRegionFindsOnlyInternational(t *testing.T) {
	e := envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: "0551234567 or +966561234567"}}}
	Prepare(&e, "")
	if got := fmt.Sprint(e.Phones); got != "[{+966561234567 body}]" {
		t.Errorf("Phones = %s, want only the international number (no region, no guessing)", got)
	}
}

func TestText(t *testing.T) {
	s := envelope.TextSurface{Norm: "a", Skeleton: "b", Leet: "c", Loose: "d"}
	if got := Text(&s); got.Norm != "a" || got.Skeleton != "b" || got.Leet != "c" || got.Loose != "d" {
		t.Errorf("Text = %+v", got)
	}
}

func TestPrepareFindsCompatibilityDigits(t *testing.T) {
	for _, raw := range []string{"call ⓪⑤⑤①②③④⑤⑥⑦", "call ⁰⁵⁵¹²³⁴⁵⁶⁷", "call ０５５１２３４５６７"} {
		e := envelope.Envelope{Texts: []envelope.TextSurface{{Source: "body", Raw: raw}}}
		Prepare(&e, "SA")
		if got := fmt.Sprint(e.Phones); got != "[{+966551234567 body}]" {
			t.Errorf("Phones(%q) = %s, want the number (circled/superscript digits must not evade number checks)", raw, got)
		}
	}
}
