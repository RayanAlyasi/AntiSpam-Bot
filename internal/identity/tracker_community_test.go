package identity

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const (
	market   = wa.JID("120363100@g.us") // a community
	phones   = wa.JID("120363101@g.us") // one of its groups the bot isn't in
	marketAn = wa.JID("120363102@g.us") // its announcement group
)

// fakeSource reads communities from a fake WAClient, whichever bot asks.
type fakeSource struct{ c *fake.Client }

func (f fakeSource) GroupInfo(ctx context.Context, _, group wa.JID) (wa.GroupInfo, error) {
	return f.c.GroupInfo(ctx, group)
}

func (f fakeSource) SubGroups(ctx context.Context, _, community wa.JID) ([]wa.SubGroup, error) {
	return f.c.SubGroups(ctx, community)
}

// communityTracker is a tracker whose bot is in the community "Riyadh Market" with Cars (grp),
// and whose list also has Phones (bot not in it) and the announcement group.
func communityTracker(t *testing.T) (*trackerEnv, *fake.Client, *[]Group) {
	t.Helper()
	env := newTracker(t)
	wac := fake.New()
	wac.SetGroup(wa.GroupInfo{JID: market, Name: "Riyadh Market", IsCommunity: true})
	cars := groupInfo(false)
	cars.LinkedParent = market
	wac.SetGroup(cars)
	wac.SetSubGroup(market, wa.SubGroup{JID: phones, Name: "Phones"})
	wac.SetSubGroup(market, wa.SubGroup{JID: marketAn, Name: "Riyadh Market", IsAnnouncement: true})
	var started []Group
	env.tr.WithCommunities(fakeSource{wac}, func(_ context.Context, g Group) error {
		started = append(started, g)
		return nil
	})
	return env, wac, &started
}

// TestTrackerRecordsCommunities: a group's community (parent JID and name) and the community's
// own list of groups are stored on join; the list is refetched at most once per refresh period;
// joining or being made admin in a community group starts it from the community (§12.4).
func TestTrackerRecordsCommunities(t *testing.T) {
	ctx := t.Context()
	inMarket := func(admin bool) wa.GroupInfo {
		info := groupInfo(admin, alice)
		info.LinkedParent = market
		return info
	}
	tests := []struct {
		name        string
		events      func(env *trackerEnv) error
		wantStatus  GroupStatus
		wantStarted int
	}{
		{"join without admin", func(env *trackerEnv) error {
			return env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: inMarket(false)})
		}, GroupPendingAdmin, 1},
		{"join, then promoted", func(env *trackerEnv) error {
			if err := env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: inMarket(false)}); err != nil {
				return err
			}
			return env.tr.HandleEvent(ctx, &wa.GroupEvent{Bot: botPN, Group: grp, Sender: alice.JID, Promote: []wa.JID{botLID}})
		}, GroupActive, 2},
		{"promoted while away (refresh)", func(env *trackerEnv) error {
			if err := env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: inMarket(false)}); err != nil {
				return err
			}
			return env.tr.Refresh(ctx, botPN, inMarket(true))
		}, GroupActive, 2},
		{"refresh while already admin", func(env *trackerEnv) error {
			if err := env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: inMarket(true)}); err != nil {
				return err
			}
			return env.tr.Refresh(ctx, botPN, inMarket(true))
		}, GroupActive, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, _, started := communityTracker(t)
			if err := tt.events(env); err != nil {
				t.Fatal(err)
			}
			g, ok, _ := env.groups.Group(ctx, grp)
			if !ok || g.Community != market || g.CommunityName != "Riyadh Market" || g.Kind != KindGroup || g.Status != tt.wantStatus {
				t.Errorf("group = %+v, want community %s named Riyadh Market, kind group, status %s", g, market, tt.wantStatus)
			}
			if len(*started) != tt.wantStarted {
				t.Errorf("start hook ran %d times, want %d (on join and when made admin)", len(*started), tt.wantStarted)
			}
			subs, _ := env.groups.CommunityGroups(ctx, market)
			if got := jidsOf(subs); !slices.Equal(got, []wa.JID{phones, marketAn, grp}) {
				t.Errorf("community list = %v, want Cars, Phones and the announcement group", got)
			}
		})
	}
}

func jidsOf(subs []wa.SubGroup) []wa.JID {
	out := make([]wa.JID, len(subs))
	for i, s := range subs {
		out[i] = s.JID
	}
	return out
}

func TestTrackerCommunityFetchesArePaced(t *testing.T) {
	ctx := t.Context()
	env, wac, _ := communityTracker(t)
	info := groupInfo(true)
	info.LinkedParent = market
	for range 3 {
		if err := env.tr.Refresh(ctx, botPN, info); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(wac.CallsTo(fake.MethodSubGroups)); n != 1 {
		t.Errorf("SubGroups called %d times in one refresh period, want 1", n)
	}
	wac.SetGroup(wa.GroupInfo{JID: market, Name: "Riyadh Market 2", IsCommunity: true})
	env.clk.Advance(GroupRefreshEvery + time.Minute)
	if err := env.tr.Refresh(ctx, botPN, info); err != nil {
		t.Fatal(err)
	}
	if n := len(wac.CallsTo(fake.MethodSubGroups)); n != 2 {
		t.Errorf("SubGroups called %d times after the period, want 2", n)
	}
	if g, _, _ := env.groups.Group(ctx, grp); g.CommunityName != "Riyadh Market 2" {
		t.Errorf("community name = %q after a refetch, want the new name", g.CommunityName)
	}

	// A failed fetch keeps the name already known and is tried again on the next snapshot.
	env.clk.Advance(GroupRefreshEvery + time.Minute)
	wac.FailNext(fake.MethodSubGroups, wa.ErrTimeout)
	if err := env.tr.Refresh(ctx, botPN, info); err != nil {
		t.Fatal(err)
	}
	if g, _, _ := env.groups.Group(ctx, grp); g.CommunityName != "Riyadh Market 2" {
		t.Errorf("community name = %q after a failed fetch, want the known one kept", g.CommunityName)
	}
	if err := env.tr.Refresh(ctx, botPN, info); err != nil {
		t.Fatal(err)
	}
	if n := len(wac.CallsTo(fake.MethodSubGroups)); n != 4 {
		t.Errorf("SubGroups called %d times, want a retry after the failure (4)", n)
	}
}

func TestTrackerCommunityKindsAndLinks(t *testing.T) {
	ctx := t.Context()
	env, _, started := communityTracker(t)

	// The community itself and its announcement group are tracked with their kind.
	if err := env.tr.SyncJoined(ctx, botPN, []wa.GroupInfo{
		{JID: market, Name: "Riyadh Market", IsCommunity: true},
		{JID: marketAn, Name: "Riyadh Market", LinkedParent: market, IsAnnouncement: true},
		groupInfo(true),
	}); err != nil {
		t.Fatal(err)
	}
	for j, want := range map[wa.JID]GroupKind{market: KindCommunity, marketAn: KindAnnouncement, grp: KindGroup} {
		if g, _, _ := env.groups.Group(ctx, j); g.Kind != want {
			t.Errorf("%s kind = %q, want %q", j, g.Kind, want)
		}
	}
	if g, _, _ := env.groups.Group(ctx, grp); g.Community != "" {
		t.Fatalf("Cars without a parent: community %q, want none", g.Community)
	}
	*started = nil

	tests := []struct {
		name          string
		evt           *wa.GroupEvent
		wantCommunity wa.JID
		wantName      string
		wantListed    bool
	}{
		{"linked (event on the group)", &wa.GroupEvent{Bot: botPN, Group: grp,
			Link: &wa.CommunityLink{Community: market, Group: grp, Name: "Riyadh Market"}}, market, "Riyadh Market", true},
		{"unlinked (event on the group)", &wa.GroupEvent{Bot: botPN, Group: grp,
			Unlink: &wa.CommunityLink{Community: market, Group: grp}}, "", "", false},
		{"linked (event on the community)", &wa.GroupEvent{Bot: botPN, Group: market,
			Link: &wa.CommunityLink{Community: market, Group: grp, Name: "Cars"}}, market, "Riyadh Market", true},
		{"unlinked (event on the community)", &wa.GroupEvent{Bot: botPN, Group: market,
			Unlink: &wa.CommunityLink{Community: market, Group: grp, Name: "Cars"}}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := env.tr.HandleEvent(ctx, tt.evt); err != nil {
				t.Fatal(err)
			}
			g, _, _ := env.groups.Group(ctx, grp)
			if g.Community != tt.wantCommunity || g.CommunityName != tt.wantName || g.Status != GroupActive || g.Name != "Cars" {
				t.Errorf("Cars = %+v, want community %q named %q, still active and named", g, tt.wantCommunity, tt.wantName)
			}
			subs, _ := env.groups.CommunityGroups(ctx, market)
			if got := slices.Contains(jidsOf(subs), grp); got != tt.wantListed {
				t.Errorf("Cars in the community's list = %v, want %v", got, tt.wantListed)
			}
		})
	}
	if len(*started) != 0 {
		t.Errorf("start hook ran %d times for link changes, want only on join or promotion", len(*started))
	}
}
