package chat_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	cars   wa.JID = "120363000000000001@g.us"
	shop   wa.JID = "120363000000000002@g.us"
	admins wa.JID = "120363000000000099@g.us"
	sara   wa.JID = "300000000000001@lid"
	omar   wa.JID = "300000000000002@lid"
	member wa.JID = "200000000000001@lid"
)

type rig struct {
	h        *chat.Handler
	acts     *reviewtest.Actions
	admins   *reviewtest.Admins
	groups   *chattest.Groups
	settings *chattest.Settings
	lists    *chattest.Lists
	recent   *chattest.Recent
	audit    *chattest.Audit
	clock    *clock.Fake
	joiner   *fakeJoiner
	reviews  *review.Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{
		acts: &reviewtest.Actions{}, admins: &reviewtest.Admins{}, lists: &chattest.Lists{},
		recent: &chattest.Recent{Counts: map[string]int{"free money": 3}}, audit: &chattest.Audit{},
		clock: clock.NewFake(chattest.Start), joiner: &fakeJoiner{},
	}
	r.groups = chattest.NewGroups(r.admins)
	r.groups.Protect(cars, "Riyadh Cars Market")
	r.groups.Protect(shop, "Shop")
	r.groups.SetName(admins, "Admins")
	r.admins.Set(cars, sara, true)
	r.admins.Set(shop, omar, true)
	r.settings = chattest.NewSettings(map[string]string{
		"preset": "standard", "mode": "enforce", "silent": "true", "enforcement.mode": "auto",
		"review.destination": "dashboard", "review.review_group": "", "review.timeout": "6h",
		"review.on_timeout": "keep_deleted_no_ban", "review.language": "en", "policy.action": "delete_only",
		"content.videos": "allow", "content.stickers": "allow", "content.links": "allow",
		"new_members.just_joined.enabled": "true", "ladder.enabled": "false",
		"enforcement.autoban_matrix.videos.action": "auto_ban", "enforcement.autoban_matrix.videos.require_source": "known",
		"enforcement.autoban_matrix.links.action": "auto_ban", "enforcement.autoban_matrix.links.require_source": "known",
	})
	r.settings.Presets = map[string]map[string]string{"strict": {"enforcement.autoban_matrix.videos.action": "auto_ban"}}
	r.reviews = review.NewService(review.Deps{
		Store: review.NewMemoryStore(), Settings: &reviewtest.Settings{}, Admins: r.admins, Actions: r.acts, Clock: r.clock,
	})
	r.h = chat.New(chat.Deps{
		Groups: r.groups, Admins: r.admins, Actions: r.acts, Reviews: r.reviews, Settings: r.settings,
		Lists: r.lists, Recent: r.recent, Track: chattest.Track{}, Stats: chattest.Stats{Day: chat.DayStats{Deleted: 4, Banned: 1}},
		Joiner: r.joiner, Audit: r.audit, Clock: r.clock, WebsiteURL: "https://bot.example",
	})
	return r
}

func (r *rig) dm(t *testing.T, from wa.JID, text string) {
	t.Helper()
	if err := r.h.Handle(context.Background(), chat.Message{Chat: from, Sender: from, ID: wa.MessageID("D" + text), Text: text, At: r.clock.Now()}); err != nil {
		t.Fatalf("Handle(%q): %v", text, err)
	}
}

func (r *rig) in(t *testing.T, group, from wa.JID, text string) {
	t.Helper()
	if err := r.h.Handle(context.Background(), chat.Message{Chat: group, Sender: from, ID: wa.MessageID("G" + text), Text: text, At: r.clock.Now()}); err != nil {
		t.Fatalf("Handle(%q): %v", text, err)
	}
}

func (r *rig) lastReply(t *testing.T) review.Send {
	t.Helper()
	s := reviewtest.Of[review.Send](r.acts)
	if len(s) == 0 {
		t.Fatal("no reply sent")
	}
	return s[len(s)-1]
}

func (r *rig) value(group wa.JID, key string) string {
	v, _ := r.settings.Get(context.Background(), group, key)
	return v
}

// TestSettingsCommands covers F-081 and F-082: every friendly command writes the same dotted
// keys as .set (DESIGN §13.7, §12.1).
func TestSettingsCommands(t *testing.T) {
	tests := []struct {
		cmd       string
		key, want string
	}{
		{".set review.timeout 2h", "review.timeout", "2h"},
		{".mode shadow", "mode", "shadow"},
		{".mode pause", "mode", "paused"},
		{".silent off", "silent", "false"},
		{".newsignal off", "new_members.just_joined.enabled", "false"},
		{".policy ban", "policy.action", "ban"},
		{".block videos", "content.videos", "block"},
		{".allow stickers", "content.stickers", "allow"},
		{".blocknew links", "content.links", "block_for_new"},
		{".autoban videos ask", "enforcement.autoban_matrix.videos.action", "ask"},
		{".autoban links auto known", "enforcement.autoban_matrix.links.require_source", "known"},
		{".askadmins before", "enforcement.mode", "ask_before_action"},
		{".preset strict", "preset", "strict"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			r := newRig(t)
			r.in(t, cars, sara, tt.cmd)
			if got := r.value(cars, tt.key); got != tt.want {
				t.Fatalf("%s: %s = %q, want %q", tt.cmd, tt.key, got, tt.want)
			}
			for _, ch := range r.settings.Changes() {
				if ch.Group != cars {
					t.Errorf("%s changed %s in %s, want only the command's group", tt.cmd, ch.Key, ch.Group)
				}
			}
			if reply := r.lastReply(t); reply.Chat != sara {
				t.Errorf("reply went to %s, want a DM to the admin (DESIGN §9.7)", reply.Chat)
			}
			if len(r.audit.Entries()) == 0 {
				t.Error("no audit entry for a settings change (DESIGN §13.7)")
			}
		})
	}
}

func TestSettingsErrorsAndReads(t *testing.T) {
	r := newRig(t)
	r.dm(t, sara, ".get review.timeout")
	if got := r.lastReply(t).Message.Text; got != "review.timeout = 6h" {
		t.Errorf(".get reply = %q, want the value", got)
	}
	r.dm(t, sara, ".set no.such.key 1")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "no.such.key") {
		t.Errorf(".set unknown key reply = %q, want it explained", got)
	}
	r.settings.Validate = func(key, value string) error {
		if key == "review.timeout" && value == "forever" {
			return errInvalid
		}
		return nil
	}
	r.dm(t, sara, ".set review.timeout forever")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "isn't a valid value") || r.value(cars, "review.timeout") != "6h" {
		t.Errorf(".set invalid = %q (value %q), want it refused and explained", got, r.value(cars, "review.timeout"))
	}
	r.dm(t, sara, ".autoban links auto known,ai")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "track record") || r.value(cars, "enforcement.autoban_matrix.links.require_source") != "known" {
		t.Errorf(".autoban … auto ai without a track record = %q, want refused (DESIGN §9.2)", got)
	}
	r.dm(t, sara, ".status")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "4 deleted") || !strings.Contains(got, "standard") {
		t.Errorf(".status = %q, want preset and today's stats", got)
	}
	r.dm(t, sara, ".حالة")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "اليوم") {
		t.Errorf("Arabic alias reply = %q, want Arabic", got)
	}
}

type invalidErr struct{}

func (invalidErr) Error() string { return "not a duration" }

var errInvalid = invalidErr{}

// TestDMContext covers F-080: in a DM the admin picks a group with .use for one hour; with one
// group it's implicit; non-admins are ignored.
func TestDMContext(t *testing.T) {
	r := newRig(t)
	r.admins.Set(shop, sara, true)
	r.dm(t, sara, ".mode shadow")
	if !strings.Contains(r.lastReply(t).Message.Text, "Which group?") || r.value(cars, "mode") != "enforce" {
		t.Fatal("two groups and no .use: want a 'which group' reply and no change")
	}
	r.dm(t, sara, ".use shop")
	r.dm(t, sara, ".mode shadow")
	if r.value(shop, "mode") != "shadow" || r.value(cars, "mode") != "enforce" {
		t.Fatal(".use shop then .mode: want only Shop changed")
	}
	r.clock.Advance(61 * time.Minute)
	r.dm(t, sara, ".mode pause")
	if r.value(shop, "mode") != "shadow" {
		t.Error("context used after an hour: want it expired (DESIGN §13.7)")
	}
	r.dm(t, sara, ".groups")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "Shop") || !strings.Contains(got, "Riyadh Cars Market") {
		t.Errorf(".groups = %q, want both groups", got)
	}
	before := len(r.acts.All())
	r.dm(t, member, ".groups")
	r.dm(t, member, ".mode shadow")
	if len(r.acts.All()) != before {
		t.Error("a non-admin's DM commands: want no reply at all (DESIGN §13.7)")
	}
}

// TestProtectedGroupIsSilent: an admin command in a protected group is deleted and answered by
// DM; nothing ever goes to the group, and other messages there aren't ours.
func TestProtectedGroupIsSilent(t *testing.T) {
	r := newRig(t)
	r.in(t, cars, sara, ".help")
	r.in(t, cars, member, "hello everyone")
	r.in(t, cars, member, ".help")
	for _, a := range r.acts.All() {
		if s, ok := a.(review.Send); ok && s.Chat == cars {
			t.Fatalf("sent %q into the protected group (DESIGN §9.7)", s.Message.Text)
		}
	}
	if d := reviewtest.Of[review.Delete](r.acts); len(d) != 1 || d[0].Message.Sender != sara {
		t.Errorf("deletes = %+v, want only the admin's command deleted", d)
	}
}

// TestWordDryRunThenSave covers F-084.
func TestWordDryRunThenSave(t *testing.T) {
	r := newRig(t)
	r.dm(t, sara, ".word block free money")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "matched 3 recent messages") || len(r.lists.All()) != 0 {
		t.Fatalf("first .word block = %q, lists %v; want a dry-run count and nothing saved (DESIGN §8.4)", got, r.lists.All())
	}
	r.dm(t, sara, ".word block free money")
	if got := r.lists.All(); !slices.Equal(got, []string{"block_word free money"}) {
		t.Fatalf("repeated .word block saved %v, want the phrase blocked", got)
	}
	r.dm(t, sara, ".word unblock free money")
	r.dm(t, sara, ".domain block https://www.Bad.example/path")
	r.dm(t, sara, ".domain allow youtube.com")
	want := []string{"block_word free money", "unblock_word free money", "block_domain bad.example", "allow_domain youtube.com"}
	if got := r.lists.All(); !slices.Equal(got, want) {
		t.Errorf("list edits = %v, want %v", got, want)
	}
	r.dm(t, sara, ".word block spam phrase")
	r.clock.Advance(11 * time.Minute)
	r.dm(t, sara, ".word block spam phrase")
	if slices.Contains(r.lists.All(), "block_word spam phrase") {
		t.Error("confirmation after 10 minutes: want a fresh dry run instead of saving")
	}
}

// TestReviewGroupHere covers F-087.
func TestReviewGroupHere(t *testing.T) {
	r := newRig(t)
	r.groups.SetParticipants(admins, sara, member)
	r.in(t, cars, sara, ".reviewgroup here")
	if r.value(cars, "review.review_group") != "" {
		t.Fatal(".reviewgroup here in a protected group: want refused (DESIGN §9.5)")
	}
	r.in(t, admins, sara, ".reviewgroup here")
	if r.value(cars, "review.review_group") != string(admins) || r.value(cars, "review.destination") != "group" {
		t.Fatalf("review group = %q destination = %q, want this chat and group", r.value(cars, "review.review_group"), r.value(cars, "review.destination"))
	}
	replies := reviewtest.Of[review.Send](r.acts)
	warned := false
	for _, s := range replies {
		if s.Chat == admins && strings.Contains(s.Message.Text, "1 people here aren't admins") {
			warned = true
		}
	}
	if !warned {
		t.Error("review group with a non-admin: want a warning (DESIGN §9.5)")
	}
	r.groups.Serve(admins, cars)
	r.in(t, admins, sara, ".reviewgroup off")
	if r.value(cars, "review.review_group") != "" || r.value(cars, "review.destination") != "dashboard" {
		t.Error(".reviewgroup off: want the review group cleared and the dashboard back")
	}
	before := len(r.acts.All())
	r.in(t, admins, member, ".status")
	if len(r.acts.All()) != before {
		t.Error("a review-group member who isn't an admin: want ignored (DESIGN §9.5)")
	}
}

// TestPendingAndDecideByCode covers F-116's commands.
func TestPendingAndDecideByCode(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it, err := r.reviews.Open(ctx, review.NewItem{Group: cars, Kind: review.KindBanDecision, Subject: review.Subject{Person: member, Name: "Ahmed"}})
	if err != nil {
		t.Fatal(err)
	}
	r.dm(t, sara, ".pending")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "#"+it.Code) {
		t.Fatalf(".pending = %q, want #%s listed", got, it.Code)
	}
	r.dm(t, sara, ".review "+strings.ToLower(it.Code)+" ban")
	if bans := reviewtest.Of[review.Ban](r.acts); len(bans) != 1 || bans[0].Target != member {
		t.Fatalf("bans = %+v, want the item's subject banned", bans)
	}
	r.dm(t, sara, ".review ZZZZ ban")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "No review #ZZZZ") {
		t.Errorf("unknown code reply = %q", got)
	}
}

type fakeJoiner struct {
	info   wa.GroupInfo
	joined []string
	left   []wa.JID
}

func (f *fakeJoiner) GroupFromLink(_ context.Context, _ string) (wa.GroupInfo, error) {
	return f.info, nil
}

func (f *fakeJoiner) JoinWithLink(_ context.Context, code string) (wa.JID, error) {
	f.joined = append(f.joined, code)
	return f.info.JID, nil
}

func (f *fakeJoiner) Leave(_ context.Context, g wa.JID) error { f.left = append(f.left, g); return nil }

// TestOnboarding covers F-085.
func TestOnboarding(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if err := r.h.OnPromoted(ctx, cars, sara); err != nil {
		t.Fatal(err)
	}
	if n := len(reviewtest.Of[review.Send](r.acts)); n != 0 {
		t.Fatalf("promotion by an admin without a thread sent %d DMs, want none until they write (inbound-first, DESIGN §13.6)", n)
	}
	r.dm(t, sara, "START")
	sends := reviewtest.Of[review.Send](r.acts)
	if len(sends) != 2 || !strings.Contains(sends[0].Message.Text, "Protection is on") || !strings.Contains(sends[0].Message.Text, "Riyadh Cars Market") || !strings.Contains(sends[1].Message.Text, "Three steps") {
		t.Fatalf("after START: %d sends, want the held promotion notice then the setup steps", len(sends))
	}
	r.acts.Reset()
	for range 2 {
		if err := r.h.OnNotAdmin(ctx, shop, sara); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(reviewtest.Of[review.Send](r.acts)); n != 1 {
		t.Errorf("not-admin reminders = %d, want one per day", n)
	}
	if err := r.h.OnNotAdmin(ctx, shop, omar); err != nil {
		t.Fatal(err)
	}
	if n := len(reviewtest.Of[review.Send](r.acts)); n != 1 {
		t.Error("not-admin reminder to someone without a thread: want none")
	}

	r.acts.Reset()
	r.joiner.info = wa.GroupInfo{JID: "120363000000000077@g.us", Name: "New", Participants: []wa.Participant{{JID: member, IsAdmin: true}}}
	r.dm(t, sara, "https://chat.whatsapp.com/AbCdEfGhIjKlMnOpQrStUv")
	if len(r.joiner.joined) != 0 || !strings.Contains(r.lastReply(t).Message.Text, "need to be an admin") {
		t.Fatal("invite link from a non-admin of that group: want no join and an explanation")
	}
	r.joiner.info.Participants = append(r.joiner.info.Participants, wa.Participant{JID: sara, IsAdmin: true})
	r.dm(t, sara, "chat.whatsapp.com/AbCdEfGhIjKlMnOpQrStUv")
	if len(r.joiner.joined) != 1 {
		t.Fatal("invite link from an admin of that group: want joined")
	}
	r.joiner.info.Participants = nil
	r.dm(t, sara, "chat.whatsapp.com/ZbCdEfGhIjKlMnOpQrStUv")
	if len(r.joiner.left) != 1 {
		t.Error("joined a group whose participants weren't listed and the sender isn't admin: want the bot to leave")
	}
}
