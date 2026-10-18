package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	market wa.JID = "120363000000000100@g.us" // the community "Riyadh Market"
	phones wa.JID = "120363000000000101@g.us" // in it; the bot isn't an admin there
	bikes  wa.JID = "120363000000000102@g.us" // in it; the bot isn't in it
)

// newCommunityRig: Sara manages Riyadh Cars Market and Phones, both in the community Riyadh
// Market, and the standalone Shop. The community also has Bikes, which the bot isn't in.
func newCommunityRig(t *testing.T) (*rig, blacklist.List) {
	t.Helper()
	r := newRigNoSite(t)
	r.groups.Protect(phones, "Phones")
	r.groups.InCommunity(cars, market, "Riyadh Market")
	r.groups.InCommunity(phones, market, "Riyadh Market")
	r.groups.SetCommunityGroups(market,
		chat.CommunityGroup{JID: bikes, Name: "Bikes"},
		chat.CommunityGroup{JID: phones, Name: "Phones", Member: true},
		chat.CommunityGroup{JID: cars, Name: "Riyadh Cars Market", Member: true, Admin: true},
	)
	r.admins.Set(phones, sara, true)
	r.admins.Set(shop, sara, true)
	list := blacklist.NewMemoryList()
	bl := blacklist.NewService(blacklist.Deps{
		List: list, Groups: &blacklisttest.Groups{}, Actions: r.acts,
		Settings: &blacklisttest.Settings{Default: blacklist.GroupSettings{Mode: review.ModeAuto}},
	})
	r.h = chat.New(chat.Deps{
		Groups: r.groups, Communities: r.groups, Admins: r.admins, Actions: r.acts, Reviews: r.reviews,
		Settings: r.settings, Lists: r.lists, Recent: r.recent, Track: chattest.Track{}, Blacklist: bl,
		Stats: chattest.Stats{Day: chat.DayStats{Deleted: 4, Banned: 1}}, Joiner: r.joiner, Audit: r.audit, Clock: r.clock,
	})
	return r, list
}

const communityList = "1. *Riyadh Market* (community, 2 groups)\n   2. Phones\n   3. Riyadh Cars Market\n4. Shop"

// TestCommunityGroupsAndUse: .groups lists each community with its groups indented under it and
// the standalone groups after; .use takes a community by number, name or part of the name.
func TestCommunityGroupsAndUse(t *testing.T) {
	r, _ := newCommunityRig(t)
	r.dm(t, sara, ".groups")
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, communityList) {
		t.Fatalf(".groups = %q, want:\n%s", got, communityList)
	}
	for _, tt := range []struct {
		use       string
		community bool   // the community is selected
		group     wa.JID // or this group
	}{
		{"1", true, ""},
		{"riyadh market", true, ""},
		{"market", false, ""}, // in "Riyadh Market" and "Riyadh Cars Market": ambiguous
		{"3", false, cars},
		{"4", false, shop},
		{"phon", false, phones},
	} {
		t.Run(".use "+tt.use, func(t *testing.T) {
			r, _ := newCommunityRig(t)
			r.dm(t, sara, ".use "+tt.use)
			reply := r.lastReply(t).Message.Text
			if tt.community && (!strings.Contains(reply, "*Riyadh Market*") || !strings.Contains(reply, "Bikes: I'm not in it") ||
				!strings.Contains(reply, "Phones: I'm not an admin there")) {
				t.Errorf(".use %s = %q, want the community picked and Bikes and Phones named as unprotected", tt.use, reply)
			}
			r.dm(t, sara, ".mode shadow")
			for _, g := range []wa.JID{cars, phones, shop} {
				want := g == tt.group || (tt.community && (g == cars || g == phones))
				if got := r.value(g, "mode") == "shadow"; got != want {
					t.Errorf(".use %s then .mode shadow: %s changed = %v, want %v", tt.use, g, got, want)
				}
			}
		})
	}
}

// TestCommunitySettings: with a community picked, each settings command changes every group of
// it the sender administers, is stored as the community's defaults, and is answered once with
// the count and the groups the bot can't protect.
func TestCommunitySettings(t *testing.T) {
	tests := []struct {
		cmd       string
		key, want string
	}{
		{".mode shadow", "mode", "shadow"},
		{".preset strict", "preset", "strict"},
		{".askadmins before", "enforcement.mode", "ask_before_action"},
		{".block videos", "content.videos", "block"},
		{".blocknew links", "content.links", "block_for_new"},
		{".allow stickers", "content.stickers", "allow"},
		{".set review.timeout 2h", "review.timeout", "2h"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			r, _ := newCommunityRig(t)
			r.dm(t, sara, ".use 1")
			sent := len(reviewtest.Of[review.Send](r.acts))
			r.dm(t, sara, tt.cmd)
			if n := len(reviewtest.Of[review.Send](r.acts)) - sent; n != 1 {
				t.Errorf("%s sent %d replies, want one", tt.cmd, n)
			}
			changed := map[wa.JID]bool{}
			for _, ch := range r.settings.Changes() {
				if ch.Key == tt.key && ch.Value == tt.want {
					changed[ch.Group] = true
				}
			}
			for g, want := range map[wa.JID]bool{cars: true, phones: true, market: true, shop: false} {
				if changed[g] != want {
					t.Errorf("%s: %s %s set = %v, want %v (the community's groups and its defaults only)", tt.cmd, g, tt.key, changed[g], want)
				}
			}
			reply := r.lastReply(t).Message.Text
			for _, s := range []string{"changed in 2 of your 2 groups", tt.cmd, "Bikes: I'm not in it", "Phones: I'm not an admin there"} {
				if !strings.Contains(reply, s) {
					t.Errorf("%s reply = %q, want it to say %q", tt.cmd, reply, s)
				}
			}
		})
	}
}

func TestCommunityScopeLimits(t *testing.T) {
	t.Run("admin checks stay per group", func(t *testing.T) {
		r, _ := newCommunityRig(t)
		r.dm(t, sara, ".use 1")
		r.admins.Set(phones, sara, false)
		r.dm(t, sara, ".mode shadow")
		if r.value(cars, "mode") != "shadow" || r.value(phones, "mode") != "enforce" {
			t.Errorf("after losing admin in Phones: cars %s, phones %s; want only cars changed", r.value(cars, "mode"), r.value(phones, "mode"))
		}
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, "changed in 1 of your 1 groups") {
			t.Errorf("reply = %q, want 1 of 1", got)
		}
	})
	t.Run("one answer for a usage error", func(t *testing.T) {
		r, _ := newCommunityRig(t)
		r.dm(t, sara, ".use 1")
		r.dm(t, sara, ".mode sideways")
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, "Choose a mode") || r.value(cars, "mode") != "enforce" {
			t.Errorf(".mode sideways = %q, want the usage once and nothing changed", got)
		}
	})
	t.Run("commands for one group ask for one", func(t *testing.T) {
		r, _ := newCommunityRig(t)
		r.dm(t, sara, ".use 1")
		r.dm(t, sara, ".silent off")
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, "one group at a time") || !strings.Contains(got, communityList) ||
			r.value(cars, "silent") != "true" {
			t.Errorf(".silent off on a community = %q, want a request to pick one group and nothing changed", got)
		}
	})
	t.Run("blacklist add and remove", func(t *testing.T) {
		r, list := newCommunityRig(t)
		r.dm(t, sara, ".use 1")
		r.dm(t, sara, ".blacklist add +966551234567 scam")
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, "changed in 2 of your 2 groups") {
			t.Errorf(".blacklist add = %q, want 2 of 2", got)
		}
		for g, want := range map[wa.JID]int{cars: 1, phones: 1, shop: 0, market: 0} {
			if got, _ := list.ForGroup(context.Background(), g); len(got) != want {
				t.Errorf("%s blacklist has %d entries, want %d", g, len(got), want)
			}
		}
		r.dm(t, sara, ".blacklist remove +966551234567")
		for _, g := range []wa.JID{cars, phones} {
			if got, _ := list.ForGroup(context.Background(), g); len(got) != 0 {
				t.Errorf("%s blacklist after remove = %v, want empty", g, got)
			}
		}
	})
	t.Run("status", func(t *testing.T) {
		r, _ := newCommunityRig(t)
		r.dm(t, sara, ".use 1")
		r.dm(t, sara, ".status")
		got := r.lastReply(t).Message.Text
		for _, s := range []string{"*Phones:* enforce · standard · auto", "*Riyadh Cars Market:* enforce", "4 deleted", "Bikes: I'm not in it"} {
			if !strings.Contains(got, s) {
				t.Errorf(".status = %q, want %q", got, s)
			}
		}
	})
	t.Run("no group of the community", func(t *testing.T) {
		r, _ := newCommunityRig(t)
		r.dm(t, sara, ".use 1")
		r.admins.Set(phones, sara, false)
		r.admins.Set(cars, sara, false)
		r.dm(t, sara, ".mode shadow")
		if got := r.lastReply(t).Message.Text; !strings.Contains(got, "don't manage any group of *Riyadh Market*") {
			t.Errorf("reply = %q, want that the sender manages none of its groups", got)
		}
	})
}
