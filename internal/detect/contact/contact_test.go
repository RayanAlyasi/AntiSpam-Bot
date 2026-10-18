package contact

import (
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

func body(s string) []envelope.TextSurface {
	return []envelope.TextSurface{{Source: "body", Raw: s, Norm: s}}
}

func TestOffers(t *testing.T) {
	tests := []struct {
		name string
		e    envelope.Envelope
		want []string
	}{
		{"e164 phone", envelope.Envelope{Phones: []envelope.PhoneRef{{E164: "+966551234567"}}}, []string{offerPhone}},
		{"national number in text", envelope.Envelope{Texts: body("call 0551234567 for details")}, []string{offerPhone}},
		{"spaced national number", envelope.Envelope{Texts: body("واتساب 055 123 4567")}, []string{offerPhone}},
		{"contact card", envelope.Envelope{Contacts: []envelope.ContactRef{{DisplayName: "x"}}}, []string{offerCard}},
		{"wa.me link", envelope.Envelope{Texts: body("order here wa.me/966551234567")}, []string{offerChat}},
		{"send link in a preview", envelope.Envelope{URLs: []envelope.URLRef{{URL: "https://api.whatsapp.com/send?phone=966551234567", Source: "preview"}}}, []string{offerChat}},
		{"group invite link", envelope.Envelope{Texts: body("https://chat.whatsapp.com/AbCdEf123456")}, []string{offerInvite}},
		{"invite message", envelope.Envelope{Invites: []envelope.InviteRef{{Code: "x", Group: "1@g.us"}}}, []string{offerInvite}},
		{"channel link", envelope.Envelope{Texts: body("https://whatsapp.com/channel/0029VaExample")}, []string{offerChannel}},
		{"channel invite", envelope.Envelope{Channels: []envelope.ChannelRef{{JID: "1@newsletter", Source: "invite"}}}, []string{offerChannel}},
		{"telegram", envelope.Envelope{Texts: body("t.me/somechannel")}, []string{offerTelegram}},
		{"telegram with www", envelope.Envelope{Texts: body("https://www.t.me/somechannel")}, []string{offerTelegram}},
		{"number glued to punctuation and a domain", envelope.Envelope{Texts: body("تواصل:0551234567,example.com")}, []string{offerPhone}},
		{"country code without plus", envelope.Envelope{Texts: body("واتس 966551234567")}, []string{offerPhone}},
		{"dashed national number", envelope.Envelope{Texts: body("call 055-123-4567")}, []string{offerPhone}},
		{"several, in order", envelope.Envelope{Texts: body("t.me/x or 0551234567"), Contacts: []envelope.ContactRef{{}}}, []string{offerPhone, offerCard, offerTelegram}},

		{"ordinary links", envelope.Envelope{Texts: body("slides https://www.ksu.edu.sa/x and https://youtu.be/abc and https://maps.google.com/?q=1")}, nil},
		{"meeting id in a link", envelope.Envelope{Texts: body("zoom https://zoom.us/j/1234567890 at 7")}, nil},
		{"channel forward", envelope.Envelope{Channels: []envelope.ChannelRef{{JID: "1@newsletter", Source: "forwarded"}}}, nil},
		{"mention", envelope.Envelope{Texts: body("thanks @966551234567")}, nil},
		{"prices, dates and times", envelope.Envelope{Texts: body("50 ريال، الاختبار 12/5/2026 الساعة 10:30")}, nil},
		{"the word whatsapp", envelope.Envelope{Texts: body("message me on whatsapp or telegram")}, nil},
		{"date and time", envelope.Envelope{Texts: body("new exam date 2026-05-12 10:30")}, nil},
		{"dotted date", envelope.Envelope{Texts: body("الاختبار 05-12-2026 10")}, nil},
		{"lecture schedule", envelope.Envelope{Texts: body("lectures at 10.30 11.30 12.30")}, nil},
		{"list of years", envelope.Envelope{Texts: body("years 2024 2025 2026")}, nil},
		{"student id", envelope.Envelope{Texts: body("my id is 441234567")}, nil},
		{"order number inside a word", envelope.Envelope{Texts: body("order A1234567890123")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Offers(&tt.e); !slices.Equal(got, tt.want) {
				t.Fatalf("offers %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnalyzer(t *testing.T) {
	a := New[struct{}]()
	if a.Name() != "contact" || a.Tier() != detect.T0 || !a.Applies(&envelope.Envelope{}, nil) {
		t.Fatal("the contact analyzer must be named contact, run at T0 and apply to every message")
	}
	e := &envelope.Envelope{Texts: body("اطلب الآن 0551234567 wa.me/966551234567")}
	sigs, err := a.Analyze(t.Context(), e, &detect.State{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 || sigs[0].Code != CodeAdvertised || sigs[0].Weight != Weight || sigs[0].Known || sigs[0].Hard {
		t.Fatalf("signals %+v, want one weighted contact.advertised", sigs)
	}
	if strings.Contains(sigs[0].Evidence, "0551234567") || strings.Contains(sigs[0].Evidence, "966") {
		t.Fatalf("evidence %q shows the number: evidence never carries phone numbers (GO-003)", sigs[0].Evidence)
	}
	v := scoring.Classify(scoring.Score(scoring.DefaultWeights(), sigs), scoring.DefaultThresholds())
	if v.Band != scoring.BandClean {
		t.Fatalf("band %s, want clean: an offered contact alone never reaches T_review (DET-002)", v.Band)
	}
	if sigs, _ := a.Analyze(t.Context(), &envelope.Envelope{Texts: body("see you tomorrow")}, &detect.State{}); len(sigs) != 0 {
		t.Fatalf("ordinary text gave %+v, want no signal", sigs)
	}
}

func FuzzOffers(f *testing.F) {
	for _, s := range []string{"", "0551234567", "wa.me/1", "@1234567 https://zoom.us/j/123456789", "٠٥٥١٢٣٤٥٦٧"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Offers(&envelope.Envelope{Texts: body(s)})
		if len(got) > len(offerOrder) {
			t.Fatalf("Offers(%q) = %q: more kinds than exist", s, got)
		}
	})
}
