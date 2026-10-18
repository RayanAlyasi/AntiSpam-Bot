package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestChatCommunityGroups: a community's groups come from its own list and the tracked rows, with
// the bot's standing in each; the announcement group and other communities' groups are left out.
func TestChatCommunityGroups(t *testing.T) {
	ctx := t.Context()
	const (
		community wa.JID = "120363300@g.us"
		cars      wa.JID = "120363301@g.us"
		phones    wa.JID = "120363302@g.us"
		bikes     wa.JID = "120363303@g.us"
		news      wa.JID = "120363304@g.us"
		left      wa.JID = "120363305@g.us"
		other     wa.JID = "120363306@g.us"
	)
	groups := identity.NewMemoryGroupStore()
	for _, g := range []identity.Group{
		{JID: cars, Name: "Cars", Status: identity.GroupActive, Community: community},
		{JID: phones, Name: "Phones", Status: identity.GroupPendingAdmin, Community: community},
		{JID: news, Name: "News", Status: identity.GroupPendingAdmin, Community: community, Kind: identity.KindAnnouncement},
		{JID: left, Name: "Old", Status: identity.GroupLeft, Community: community},
		{JID: other, Name: "Elsewhere", Status: identity.GroupActive, Community: "120363399@g.us"},
	} {
		if err := groups.UpsertGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	if err := groups.SetCommunityGroups(ctx, community, []wa.SubGroup{
		{JID: bikes, Name: "Bikes"}, {JID: cars, Name: "Cars"}, {JID: news, Name: "News", IsAnnouncement: true},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := chatGroups{ws: &waStack{groups: groups}}.CommunityGroups(ctx, community)
	if err != nil {
		t.Fatal(err)
	}
	want := []chat.CommunityGroup{
		{JID: bikes, Name: "Bikes"},
		{JID: cars, Name: "Cars", Member: true, Admin: true},
		{JID: left, Name: "Old"},
		{JID: phones, Name: "Phones", Member: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("CommunityGroups = %+v\nwant %+v", got, want)
	}
}

// notReader is a WAClient without the community call.
type notReader struct{ wa.WAClient }

// TestCommunitySource: the tracker's community reads go through the bot's client, which must
// also be a wa.CommunityReader.
func TestCommunitySource(t *testing.T) {
	const community wa.JID = "120363400@g.us"
	wac := fake.New()
	wac.SetGroup(wa.GroupInfo{JID: community, Name: "Riyadh Market", IsCommunity: true})
	wac.SetSubGroup(community, wa.SubGroup{JID: "120363401@g.us", Name: "Cars"})
	tests := []struct {
		name    string
		client  wa.WAClient
		ok      bool
		wantErr error
	}{
		{"fake client", wac, true, nil},
		{"not connected", nil, false, nil},
		{"client without SubGroups", notReader{wac}, true, errors.ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := communitySource{func(wa.JID) (wa.WAClient, bool) { return tt.client, tt.ok }}
			subs, err := src.SubGroups(t.Context(), "966500000000@s.whatsapp.net", community)
			switch {
			case !tt.ok && err == nil:
				t.Error("SubGroups without a connected bot succeeded, want an error")
			case tt.ok && !errors.Is(err, tt.wantErr):
				t.Errorf("SubGroups error = %v, want %v", err, tt.wantErr)
			case tt.ok && tt.wantErr == nil && len(subs) != 1:
				t.Errorf("SubGroups = %v, want Cars", subs)
			}
		})
	}
}
