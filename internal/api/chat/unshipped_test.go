package chat_test

import (
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
)

// newRigLikeProduction wires the handler the way cmd/antispam does in v0.1: no website, no
// invite-link joining, no member allow-list, no undo history, no word or domain lists and no
// daily stats.
func newRigLikeProduction(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.h = chat.New(chat.Deps{
		Groups: r.groups, Admins: r.admins, Actions: r.acts, Reviews: r.reviews, Settings: r.settings,
		Track: chattest.Track{}, Audit: r.audit, Clock: r.clock,
	})
	return r
}

// TestNothingUnshippedIsAdvertised: help and onboarding list only commands that work in this
// build, and a command that doesn't gets a short neutral reply naming it, with no promise.
func TestNothingUnshippedIsAdvertised(t *testing.T) {
	r := newRigLikeProduction(t)
	unshipped := []string{".undo", ".unallow", ".domain", ".word", "invite link", ".login", "dashboard", "website", ".تراجع", ".نطاق", ".كلمة"}
	for _, cmd := range []string{
		"START", ".help", ".help moderation", ".help reviews", ".help settings", ".help blacklist",
		".مساعدة", ".مساعدة الإشراف", ".مساعدة المراجعات", ".مساعدة الإعدادات", ".مساعدة القائمة_السوداء",
	} {
		r.dm(t, sara, cmd)
		got := r.lastReply(t).Message.Text
		for _, u := range unshipped {
			if strings.Contains(got, u) {
				t.Errorf("%s = %q advertises %q, which doesn't work in this build", cmd, got, u)
			}
		}
	}

	for _, tt := range []struct{ cmd, want string }{
		{".undo", ".undo isn't available in this version."},
		{".domain block example.com", ".domain isn't available in this version."},
		{".word block", ".word isn't available in this version."},
		{".تراجع", "الأمر .undo غير متاح في هذا الإصدار."},
	} {
		r.dm(t, sara, tt.cmd)
		if got := r.lastReply(t).Message.Text; got != tt.want {
			t.Errorf("%s = %q, want %q", tt.cmd, got, tt.want)
		}
	}
	for _, cmd := range []string{".allow", ".allow sticker"} {
		r.dm(t, sara, cmd)
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, ".allow stickers") {
			t.Errorf("%s without the member allow-list = %q, want how to allow a message type", cmd, got)
		}
	}

	r.dm(t, sara, "https://chat.whatsapp.com/AbCdEfGhIjKlMnOpQrStUv")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "invite links in this version") || !strings.Contains(got, "Add me to the group") {
		t.Errorf("invite link without joining = %q, want a plain 'not in this version' and what to do instead", got)
	}

	r.dm(t, sara, ".status")
	if got := r.lastReply(t).Message.Text; strings.Contains(got, "–") || !strings.Contains(got, "*Waiting for review:* 0") {
		t.Errorf(".status without stats = %q, want only known facts (cases waiting), no placeholder dashes", got)
	}
}
