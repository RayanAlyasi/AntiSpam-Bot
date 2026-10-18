package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
)

// newRigAsk is the no-website rig with settings that also know the ask-before keys and every
// preset.
func newRigAsk(t *testing.T) *rig {
	t.Helper()
	r := newRigNoSite(t)
	r.settings = chattest.NewSettings(map[string]string{
		"preset": "standard", "mode": "enforce", "silent": "true", "enforcement.mode": "auto",
		"enforcement.ask_before.timeout": "15m", "enforcement.ask_before.on_timeout": "delete_no_ban",
		"review.destination": "dashboard", "review.review_group": "", "review.timeout": "6h",
		"review.on_timeout": "keep_deleted_no_ban", "review.language": "en",
	})
	r.settings.Presets = map[string]map[string]string{"relaxed": {}, "standard": {}, "strict": {}, "ask_admins": {"enforcement.mode": "ask_delete_first"}}
	r.h = chat.New(chat.Deps{
		Groups: r.groups, Admins: r.admins, Actions: r.acts, Reviews: r.reviews, Settings: r.settings,
		Stats: chattest.Stats{Day: chat.DayStats{Deleted: 4, Banned: 1}}, Audit: r.audit, Clock: r.clock,
	})
	return r
}

// TestStatusSaysWhenIAskTheAdmins: .status shows enforcement.mode in plain words, and a preset
// switch on a group that asks its admins says that asking stays on.
func TestStatusSaysWhenIAskTheAdmins(t *testing.T) {
	for _, tt := range []struct {
		mode, onTimeout, want string
	}{
		{"auto", "", "*Before acting:* act automatically"},
		{"ask_delete_first", "", "*Before acting:* delete spam, ask the admins before banning"},
		{"ask_before_action", "delete_no_ban", "*Before acting:* ask the admins (15m, then delete without a ban)"},
		{"ask_before_action", "leave", "*Before acting:* ask the admins (15m, then leave the message)"},
	} {
		r := newRigAsk(t)
		r.dm(t, sara, ".set enforcement.mode "+tt.mode)
		if tt.onTimeout != "" {
			r.dm(t, sara, ".set enforcement.ask_before.on_timeout "+tt.onTimeout)
		}
		r.dm(t, sara, ".status")
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, tt.want) || strings.Contains(got, "enforcement.mode") {
			t.Errorf("%s/%s: .status = %q, want %q instead of the raw key", tt.mode, tt.onTimeout, got, tt.want)
		}
	}

	// The fake applies a preset's own keys only; the keep-asking rule itself is tested in policy.
	for _, tt := range []struct {
		mode, preset, want string
	}{
		{"ask_before_action", "strict", "*Before acting:* ask the admins (15m, then delete without a ban)"},
		{"ask_delete_first", "relaxed", "*Before acting:* delete spam, ask the admins before banning"},
		{"ask_before_action", "ask", "*Before acting:* delete spam, ask the admins before banning"},
		{"auto", "strict", "*Before acting:* act automatically"},
	} {
		r := newRigAsk(t)
		r.dm(t, sara, ".set enforcement.mode "+tt.mode)
		r.dm(t, sara, ".preset "+tt.preset)
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, tt.want) {
			t.Errorf("%s then .preset %s = %q, want it to say how I act now: %q", tt.mode, tt.preset, got, tt.want)
		}
	}
}

// TestProtectionOnFollowsTheMode: the protection-on DM says what happens to flagged messages in
// the group's mode, and .status shows stored durations the way admins type them.
func TestProtectionOnFollowsTheMode(t *testing.T) {
	for _, tt := range []struct {
		mode, onTimeout, want string
	}{
		{"auto", "delete_no_ban", "When I'm not sure about a message"},
		{"ask_before_action", "delete_no_ban", "If nobody decides in 15m, I delete the message without a ban"},
		{"ask_before_action", "leave", "If nobody decides in 15m, I leave the message as it is"},
		{"ask_delete_first", "delete_no_ban", "I delete spam right away and ask you before banning"},
	} {
		r := newRigAsk(t)
		r.dm(t, sara, ".set enforcement.mode "+tt.mode)
		r.dm(t, sara, ".set enforcement.ask_before.on_timeout "+tt.onTimeout)
		r.dm(t, sara, ".set enforcement.ask_before.timeout 15m0s")
		if err := r.h.OnPromoted(context.Background(), cars, sara); err != nil {
			t.Fatal(err)
		}
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, tt.want) || !strings.Contains(got, "Protection is on") {
			t.Errorf("%s: protection on = %q, want %q", tt.mode, got, tt.want)
		}
	}

	r := newRigAsk(t)
	r.dm(t, sara, ".set review.timeout 6h0m0s")
	r.dm(t, sara, ".status")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "*review.timeout:* 6h\n") {
		t.Errorf(".status = %q, want review.timeout shown as 6h", got)
	}
	r.dm(t, sara, ".set enforcement.mode ask_before_action")
	r.dm(t, sara, ".status")
	if got := r.lastReply(t).Message.Text; strings.Contains(got, "review.timeout") || strings.Contains(got, "review.on_timeout") {
		t.Errorf(".status in ask_before_action = %q, want the review timeout lines left out (that mode has its own)", got)
	}
}
