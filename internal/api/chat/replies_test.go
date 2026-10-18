package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
)

// newRigNoSite is the rig without a website URL, as in v0.1 where the frontend is deferred.
func newRigNoSite(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.h = chat.New(chat.Deps{
		Groups: r.groups, Admins: r.admins, Actions: r.acts, Reviews: r.reviews, Settings: r.settings,
		Lists: r.lists, Recent: r.recent, Track: chattest.Track{}, Stats: chattest.Stats{Day: chat.DayStats{Deleted: 4, Banned: 1}},
		Joiner: r.joiner, Audit: r.audit, Clock: r.clock,
	})
	return r
}

// noSiteWords must never appear in a reply when no website is configured.
var noSiteWords = []string{"website", "dashboard", "http", "log in here", "{", "}", "الموقع", "**", " yet", "later", "for now", "بعد.", "لاحقاً", "حالياً"}

// TestRepliesWithoutWebsite: with no public URL, onboarding and the commands that used to link the
// website describe where uncertain cases really go (.pending, .reviewgroup here) and never
// mention a website, a dashboard, a login link or an empty {url}.
func TestRepliesWithoutWebsite(t *testing.T) {
	ctx := context.Background()
	r := newRigNoSite(t)
	if err := r.h.OnPromoted(ctx, cars, sara); err != nil {
		t.Fatal(err)
	}
	r.dm(t, sara, "START")
	sends := reviewtest.Of[review.Send](r.acts)
	if len(sends) != 2 {
		t.Fatalf("after START: %d sends, want the promotion notice and the setup steps", len(sends))
	}
	for i, want := range []string{"Protection is on in *Riyadh Cars Market*", "Three steps"} {
		got := sends[i].Message.Text
		if !strings.Contains(got, want) || !strings.Contains(got, ".pending") || !strings.Contains(got, ".reviewgroup here") {
			t.Errorf("onboarding %d = %q, want %q and both ways to see uncertain cases (.pending, .reviewgroup here)", i, got, want)
		}
		checkNoSite(t, "onboarding", got)
	}
	if steps := sends[1].Message.Text; !strings.Contains(steps, "\n1. ") || !strings.Contains(steps, "\n3. ") || strings.Contains(steps, "invite link") {
		t.Errorf("setup steps = %q, want numbered steps on separate lines and no invite-link step (joining by link isn't wired in v0.1)", steps)
	}

	for _, tt := range []struct{ cmd, want string }{
		{".login", "isn't part of this version"},
		{".settings", "*review.destination:* no review group (cases wait for .pending)"},
		{".status", "*review.destination:* no review group"},
		{".حالة", "تنتظر .معلق"},
		{".review dashboard", ".pending"},
		{".review dm", ".pending"},
		{".reviewgroup off", ".pending"},
		{".help", "*Reviews*"},
		{".help reviews", ".pending"},
		{".دخول", "ليس جزءاً من هذا الإصدار"},
		{".مراجعة dashboard", ".معلق"},
	} {
		r.dm(t, sara, tt.cmd)
		got := r.lastReply(t).Message.Text
		if !strings.Contains(got, tt.want) {
			t.Errorf("%s = %q, want it to contain %q", tt.cmd, got, tt.want)
		}
		checkNoSite(t, tt.cmd, got)
	}
	if r.value(cars, "review.destination") != "dashboard" {
		t.Error(".review dashboard without a website: want review.destination still set (it stops posting to a review group)")
	}
}

func checkNoSite(t *testing.T, what, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, w := range noSiteWords {
		if strings.Contains(lower, w) {
			t.Errorf("%s = %q mentions %q, want no website, link or unfilled placeholder when none is configured", what, text, w)
		}
	}
}

// TestRepliesWithWebsite: with a public URL, the same replies link it.
func TestRepliesWithWebsite(t *testing.T) {
	r := newRig(t)
	for _, cmd := range []string{"START", ".settings", ".login"} {
		r.dm(t, sara, cmd)
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, "https://bot.example") {
			t.Errorf("%s = %q, want the website link", cmd, got)
		}
	}
}

// TestHelpMenuAndTopics: .help is a short formatted menu with the four sections, and each topic
// it advertises has its own page in English and Arabic; an unknown topic gets a friendly pointer.
func TestHelpMenuAndTopics(t *testing.T) {
	r := newRigNoSite(t)
	r.dm(t, sara, ".help")
	menu := r.lastReply(t).Message.Text
	for _, h := range []string{"*Moderation*", "*Reviews*", "*Settings*", "*Blacklist*", ".help reviews"} {
		if !strings.Contains(menu, h) {
			t.Errorf(".help = %q, want %q", menu, h)
		}
	}
	if strings.Contains(menu, "|") || strings.Count(menu, "\n") < 10 {
		t.Errorf(".help = %q, want one command per line and no a|b notation", menu)
	}

	pages := map[string]string{}
	for _, tt := range []struct{ cmd, heading string }{
		{".help moderation", "*Moderation*"},
		{".help Reviews", "*Reviews*"},
		{".help settings", "*Settings*"},
		{".help blacklist", "*Blacklist*"},
		{".مساعدة الإشراف", "*الإشراف*"},
		{".مساعدة المراجعات", "*المراجعات*"},
		{".مساعدة الاعدادات", "*الإعدادات*"},
		{".مساعدة القائمة_السوداء", "*القائمة السوداء*"},
	} {
		r.dm(t, sara, tt.cmd)
		got := r.lastReply(t).Message.Text
		if !strings.HasPrefix(got, tt.heading) || got == menu {
			t.Errorf("%s = %q, want its own page starting with %s", tt.cmd, got, tt.heading)
		}
		if prev, dup := pages[got]; dup {
			t.Errorf("%s and %s got the same page, want one page per topic", tt.cmd, prev)
		}
		pages[got] = tt.cmd
		checkNoSite(t, tt.cmd, got)
	}

	r.dm(t, sara, ".help cooking")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "cooking") || !strings.Contains(got, ".help moderation") {
		t.Errorf(".help cooking = %q, want the unknown topic named and the real topics listed", got)
	}
}

// TestPendingInDMWithoutReviewGroup: with no review group, cases wait in the store, and an admin
// can list them by DM (after .use when they have several groups) and decide one by code.
func TestPendingInDMWithoutReviewGroup(t *testing.T) {
	ctx := context.Background()
	r := newRigNoSite(t)
	r.admins.Set(shop, sara, true)
	it, err := r.reviews.Open(ctx, review.NewItem{Group: cars, Kind: review.KindUncertain, Subject: review.Subject{Person: member, Name: "Ahmed"}})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(reviewtest.Of[review.Send](r.acts)); n != 0 {
		t.Fatalf("opening a case without a review group sent %d messages, want none (it waits for .pending)", n)
	}
	r.dm(t, sara, ".pending")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "Which group?") {
		t.Fatalf(".pending with two groups and no .use = %q, want a 'which group' reply", got)
	}
	r.dm(t, sara, ".use riyadh")
	r.dm(t, sara, ".pending")
	got := r.lastReply(t).Message.Text
	for _, want := range []string{"*#" + it.Code + "*", "have a look", "Ahmed", "(1)", ".review <code> ban"} {
		if !strings.Contains(got, want) {
			t.Errorf(".pending = %q, want %q", got, want)
		}
	}
	r.dm(t, sara, ".review "+it.Code+" ok")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, it.Code) || !strings.Contains(got, "not spam") {
		t.Errorf(".review %s ok by DM = %q, want it decided as not spam", it.Code, got)
	}
	r.dm(t, sara, ".pending")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "Nothing is waiting") {
		t.Errorf(".pending after deciding = %q, want nothing left", got)
	}
}
