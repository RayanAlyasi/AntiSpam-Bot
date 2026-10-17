package actions

import (
	"errors"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestPrepare(t *testing.T) {
	g := wa.JID("1203@g.us")
	target := wa.MessageRef{Sender: "5@lid", ID: "M1"}
	ok := Action{Bot: "1@s.whatsapp.net", Chat: g, Kind: KindRevoke, Priority: P1, DedupeKey: "k", Payload: Payload{Target: target}}
	with := func(f func(*Action)) Action { a := ok; f(&a); return a }
	cases := []struct {
		name    string
		a       Action
		invalid bool
		dropped bool
	}{
		{"valid revoke", ok, false, false},
		{"no dedupe key", with(func(a *Action) { a.DedupeKey = "" }), true, false},
		{"no bot", with(func(a *Action) { a.Bot = "" }), true, false},
		{"unknown kind", with(func(a *Action) { a.Kind = "ban" }), true, false},
		{"priority P5", with(func(a *Action) { a.Priority = 5 }), true, false},
		{"negative priority", with(func(a *Action) { a.Priority = -1 }), true, false},
		{"revoke without sender", with(func(a *Action) { a.Payload.Target.Sender = "" }), true, false},
		{"kick without JIDs", with(func(a *Action) { a.Kind = KindKick }), true, false},
		{"kick in a DM", with(func(a *Action) { a.Kind, a.Chat, a.Payload.JIDs = KindKick, "9@s.whatsapp.net", []wa.JID{"5@lid"} }), true, false},
		{"lockdown in a DM", with(func(a *Action) { a.Kind, a.Chat = KindAnnounce, "9@s.whatsapp.net" }), true, false},
		{"empty send", with(func(a *Action) { a.Kind, a.Payload = KindSend, Payload{} }), true, false},
		{"edit without text", with(func(a *Action) { a.Kind = KindEdit }), true, false},
		{"deadline passed", with(func(a *Action) { a.Deadline = t0.Add(-time.Second) }), false, true},
		{"delete older than the window", with(func(a *Action) { a.MessageTime = t0.Add(-RevokeWindow) }), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dropped, err := prepare(tc.a, t0)
			if errors.Is(err, ErrInvalid) != tc.invalid {
				t.Fatalf("err = %v, want invalid=%v", err, tc.invalid)
			}
			if (dropped != "") != tc.dropped {
				t.Fatalf("dropped = %q, want dropped=%v", dropped, tc.dropped)
			}
			if tc.invalid || tc.dropped {
				return
			}
			if got.Status != StatusPending || !got.NotBefore.Equal(t0) || !got.CreatedAt.Equal(t0) {
				t.Errorf("prepared %+v, want pending, not_before and created_at = now", got)
			}
			if !got.Deadline.Equal(t0.Add(RevokeWindow)) {
				t.Errorf("deadline %s, want now + 48 h for a delete without a message time", got.Deadline)
			}
		})
	}
}

func TestDefaultDeadline(t *testing.T) {
	sent := t0.Add(-10 * time.Hour)
	cases := []struct {
		a    Action
		want time.Time
	}{
		{Action{Kind: KindRevoke, MessageTime: sent}, sent.Add(48 * time.Hour)},
		{Action{Kind: KindRevoke}, t0.Add(48 * time.Hour)},
		{Action{Kind: KindEdit}, t0.Add(15 * time.Minute)},
		{Action{Kind: KindSend, Payload: Payload{Purpose: PurposeWarning}}, t0.Add(10 * time.Minute)},
		{Action{Kind: KindSend, Payload: Payload{Purpose: PurposeReview}}, t0.Add(24 * time.Hour)},
		{Action{Kind: KindAnnounce}, t0.Add(time.Hour)},
		{Action{Kind: KindKick}, t0.Add(24 * time.Hour)},
	}
	for _, tc := range cases {
		if got := DefaultDeadline(tc.a, t0); !got.Equal(tc.want) {
			t.Errorf("DefaultDeadline(%s %s) = %s, want %s", tc.a.Kind, tc.a.Payload.Purpose, got, tc.want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v, want nil", err)
	}
	for name, change := range map[string]func(*Config){
		"zero ceiling":         func(c *Config) { c.CeilingPerMin = 0 },
		"zero burst":           func(c *Config) { c.Burst = 0 },
		"min rate above cap":   func(c *Config) { c.MinRatePerMin = 50 },
		"zero kick batch":      func(c *Config) { c.MaxKickBatch = 0 },
		"negative jitter":      func(c *Config) { c.Jitter = -1 },
		"unknown breaker kind": func(c *Config) { c.BreakerAllows = []Kind{"ban"} },
	} {
		c := DefaultConfig()
		change(&c)
		if c.Validate() == nil {
			t.Errorf("%s: Validate() = nil, want an error", name)
		}
	}
}

func TestMaskJID(t *testing.T) {
	for in, want := range map[wa.JID]string{
		"966512345612@s.whatsapp.net": "966•••••••12@s.whatsapp.net",
		"1234@s.whatsapp.net":         "••••@s.whatsapp.net",
		"120363@g.us":                 "120363@g.us",
		"5123@lid":                    "5123@lid",
	} {
		if got := maskJID(in); got != want {
			t.Errorf("maskJID(%s) = %s, want %s (phone numbers are personal data)", in, got, want)
		}
	}
}
