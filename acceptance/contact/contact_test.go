package contact_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/contact"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
)

const (
	group wa.JID = "120363000000000001@g.us"
	bot   wa.JID = "966500000000@s.whatsapp.net"
)

const vcard = "BEGIN:VCARD\nVERSION:3.0\nFN:Study Helper\nTEL;type=CELL;waid=966551234567:+966 55 123 4567\nEND:VCARD"

// msg builds a message through the real envelope builder and normalizer, as the fast lane does.
func msg(t *testing.T, text string, vcards ...string) *envelope.Envelope {
	t.Helper()
	e, err := synth.Envelope(synth.Spec{Chat: group, Sender: "200000000000009@lid", ID: "3EB0CONTACT", Time: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), Text: text, VCards: vcards}, bot)
	if err != nil {
		t.Fatalf("synth.Envelope: %v", err)
	}
	pipeline.NormalizeTexts(e)
	return e
}

// raw builds an envelope by hand for content the synthetic builder can't express, then normalizes it.
func raw(text string, edit func(e *envelope.Envelope)) func(t *testing.T) *envelope.Envelope {
	return func(t *testing.T) *envelope.Envelope {
		e := &envelope.Envelope{Group: group, Kind: envelope.KindText}
		if text != "" {
			e.Texts = []envelope.TextSurface{{Source: envelope.SourceBody, Raw: text}}
		}
		edit(e)
		pipeline.NormalizeTexts(e)
		return e
	}
}

func text(s string, vcards ...string) func(t *testing.T) *envelope.Envelope {
	return func(t *testing.T) *envelope.Envelope { return msg(t, s, vcards...) }
}

func run(t *testing.T, e *envelope.Envelope) []detect.Signal {
	t.Helper()
	a := contact.New[struct{}]()
	if !a.Applies(e, &struct{}{}) {
		return nil
	}
	sigs, err := a.Analyze(t.Context(), e, &detect.State{})
	if err != nil {
		t.Fatalf("Analyze: got error %v, want nil", err)
	}
	return sigs
}

func TestAnalyzerIdentity(t *testing.T) {
	a := contact.New[struct{}]()
	if a.Name() != "contact" {
		t.Errorf("got name %q, want \"contact\" (stable name for config flags and metrics, DESIGN §22.2)", a.Name())
	}
	if a.Tier() != detect.T0 {
		t.Errorf("got tier %v, want T0 (an envelope-only check with no I/O, DESIGN §7.1)", a.Tier())
	}
	if contact.CodeAdvertised != "contact.advertised" || contact.Weight != 1.0 {
		t.Errorf("got code %q weight %v, want contact.advertised 1.0 (contract holdout-recall)", contact.CodeAdvertised, contact.Weight)
	}
}

func TestAdvertisedFires(t *testing.T) {
	tests := []struct {
		name string
		env  func(t *testing.T) *envelope.Envelope
	}{
		{"international number", text("Need help with your assignments? Call +966 55 123 4567")},
		{"national number", text("للتواصل 0551234567")},
		{"national number with spaces", text("call 055 123 4567 for details")},
		{"arabic-indic digits", text("واتساب ٠٥٥١٢٣٤٥٦٧")},
		{"contact card", text("Study Helper", vcard)},
		{"group invite message", raw("Join our study group", func(e *envelope.Envelope) {
			e.Kind = envelope.KindGroupInvite
			e.Invites = []envelope.InviteRef{{Code: "AbCdEf1234567890XyZab", Group: "120363000000000077@g.us", GroupName: "Study group", Source: "invite_message"}}
		})},
		{"group invite link", text("Join our group https://chat.whatsapp.com/AbCdEf1234567890XyZab today")},
		{"channel invite", raw("Follow our channel", func(e *envelope.Envelope) {
			e.Kind = envelope.KindChannelInvite
			e.Channels = []envelope.ChannelRef{{JID: "120363000000000088@newsletter", Name: "Study tips", Source: "invite"}}
		})},
		{"channel shared as link ref", raw("Follow us", func(e *envelope.Envelope) {
			e.Channels = []envelope.ChannelRef{{JID: "120363000000000088@newsletter", Source: "link"}}
		})},
		{"channel link", text("Follow https://whatsapp.com/channel/0029VaAbCdEfGhIjKlMn0123 now")},
		{"wa.me message link", text("chat with us wa.me/message/ABCDEF1234567")},
		{"wa.me number link", text("chat with us wa.me/966551234567")},
		{"send link", text("https://api.whatsapp.com/send?phone=966551234567&text=hi")},
		{"telegram t.me", text("more on t.me/studyhelp")},
		{"telegram telegram.me", text("more on telegram.me/studyhelp")},
		{"fullwidth t.me", text("more on ｔ．ｍｅ/studyhelp")},
		{"link preview url only", raw("see this", func(e *envelope.Envelope) {
			e.URLs = []envelope.URLRef{{URL: "https://t.me/studyhelp", Source: "preview"}}
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs := run(t, tt.env(t))
			if len(sigs) != 1 {
				t.Fatalf("got %d signals %+v, want exactly 1 contact.advertised (the message offers a way to reach someone outside the group, contract holdout-recall)", len(sigs), sigs)
			}
			s := sigs[0]
			if s.Code != contact.CodeAdvertised || s.Weight != 1.0 {
				t.Errorf("got %s weight %v, want contact.advertised weight 1.0", s.Code, s.Weight)
			}
			if s.Known || s.Hard {
				t.Errorf("got known=%v hard=%v, want neither (a contact channel is a weighted hint, never known evidence or a policy block, DET-002)", s.Known, s.Hard)
			}
			if strings.TrimSpace(s.Evidence) == "" {
				t.Errorf("got empty evidence, want it to name what was offered (admins see why, DESIGN §7.3)")
			}
		})
	}
}

func TestAdvertisedDoesNotFire(t *testing.T) {
	tests := []struct {
		name string
		env  func(t *testing.T) *envelope.Envelope
	}{
		{"university site", text("Results are on https://www.ksu.edu.sa/en/news/2026/10/18")},
		{"youtube", text("Lecture recording https://www.youtube.com/watch?v=dQw4w9WgXcQ")},
		{"google maps", text("Meet here https://maps.app.goo.gl/AbCdEfGh123 or https://www.google.com/maps/place/24.7136,46.6753")},
		{"zoom with meeting id", text("lecture link https://us05web.zoom.us/j/81234567890?pwd=AbCdEf")},
		{"channel forward", raw("Daily tips from a channel I follow", func(e *envelope.Envelope) {
			e.IsForwarded = true
			e.Channels = []envelope.ChannelRef{{JID: "120363000000000088@newsletter", Name: "Tips", Source: "forwarded"}}
		})},
		{"mention", raw("@200000000000003 thanks for the notes", func(e *envelope.Envelope) {
			e.Mentions = []wa.JID{"200000000000003@lid"}
		})},
		{"prices dates short numbers", text("Price 1500 SAR, due 2026-10-18 at 5:30pm, room 204, 18/10/2026, total 12,500 riyals")},
		{"arabic dates and prices", text("الاختبار يوم 18/10/2026 الساعة 10 في القاعة 204 والرسوم 350 ريال")},
		{"the word whatsapp", text("send it on whatsapp please, or telegram")},
		{"plain question", text("Who has the physics notes from today?")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if sigs := run(t, tt.env(t)); len(sigs) != 0 {
				t.Errorf("got %+v, want no signal (ordinary links, forwards, mentions and numbers are not a contact offer; a false positive costs more than a miss, DET-002)", sigs)
			}
		})
	}
}

func TestAdvertisedOncePerMessage(t *testing.T) {
	e := msg(t, "Call +966 55 123 4567, wa.me/966551234567, https://chat.whatsapp.com/AbCdEf1234567890XyZab or t.me/studyhelp", vcard)
	sigs := run(t, e)
	if len(sigs) != 1 {
		t.Fatalf("got %d signals, want 1 (several channels are still one contact.advertised signal)", len(sigs))
	}
}

func TestEvidenceMasksNumber(t *testing.T) {
	for _, s := range []string{"Call +966 55 123 4567", "للتواصل 0551234567"} {
		sigs := run(t, msg(t, s))
		if len(sigs) != 1 {
			t.Fatalf("%q: got %d signals, want 1", s, len(sigs))
		}
		if ev := sigs[0].Evidence; strings.Contains(strings.ReplaceAll(ev, " ", ""), "551234567") {
			t.Errorf("got evidence %q, want the number masked (phone numbers are personal data, GO-003; blacklist evidence masks them too)", ev)
		}
	}
}
