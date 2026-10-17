package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestParse(t *testing.T) {
	tests := []struct {
		text, prefix string
		name         string
		args         []string
		arabic, ok   bool
	}{
		{".status", "", "status", nil, false, true},
		{"  .SET new_members.just_joined.window 48h", "", "set", []string{"new_members.just_joined.window", "48h"}, false, true},
		{".حالة", "", "status", nil, true, true},
		{"\u200f.حظر spam", "", "ban", []string{"spam"}, true, true},
		{".الإعدادات", "", "settings", nil, true, true},
		{".القائمة_السوداء add +966551234567", "", "blacklist", []string{"add", "+966551234567"}, true, true},
		{"!ban other", "!", "ban", []string{"other"}, false, true},
		{".ban other", "!", "", nil, false, false},
		{".unknowncmd", "", "", nil, false, false},
		{"status", "", "", nil, false, false},
		{"hello .status", "", "", nil, false, false},
		{".", "", "", nil, false, false},
		{".word\nblock free money", "", "word", []string{"block", "free", "money"}, false, true},
	}
	for _, tt := range tests {
		c, ok := chat.Parse(tt.text, tt.prefix)
		if ok != tt.ok || c.Name != tt.name || c.Arabic != tt.arabic || strings.Join(c.Args, "|") != strings.Join(tt.args, "|") {
			t.Errorf("Parse(%q, %q) = %+v, %v; want name %q args %q arabic %v ok %v (DESIGN §13.7)",
				tt.text, tt.prefix, c, ok, tt.name, tt.args, tt.arabic, tt.ok)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{".status", ".حظر spam", ".set a b", ". ", ".\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, ok := chat.Parse(s, ".")
		if !ok {
			return
		}
		if c.Name == "" || strings.Join(strings.Fields(c.Rest), " ") != strings.Join(c.Args, " ") {
			t.Fatalf("Parse(%q) = %+v: a command needs a name, and Args must be the fields of Rest", s, c)
		}
	})
}

// TestGate covers F-079: DMs only into threads the person opened, plus one command reply a day.
func TestGate(t *testing.T) {
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	g := chat.NewGate(chat.NewMemoryThreads(), c)
	const p wa.JID = "1@lid"
	allow := func(purpose chat.Purpose) bool {
		ok, err := g.Allow(ctx, p, purpose)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if allow(chat.PurposeNotice) {
		t.Error("notice to someone who never wrote: want refused (inbound-first, DESIGN §13.6)")
	}
	if !allow(chat.PurposeCommandReply) {
		t.Error("first command reply: want allowed (the one exception, DESIGN §13.7)")
	}
	if allow(chat.PurposeCommandReply) {
		t.Error("second command reply the same day: want refused (one per day)")
	}
	c.Advance(24 * time.Hour)
	if !allow(chat.PurposeCommandReply) {
		t.Error("command reply a day later: want allowed")
	}
	if err := g.Inbound(ctx, p); err != nil {
		t.Fatal(err)
	}
	if !allow(chat.PurposeNotice) || !allow(chat.PurposeCommandReply) || !allow(chat.PurposeCommandReply) {
		t.Error("after an inbound DM: want every DM allowed")
	}
}
