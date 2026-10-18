package identity

import (
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// checkCommunityStore runs the community part of the GroupStore contract against a store.
func checkCommunityStore(t *testing.T, gs GroupStore) {
	t.Helper()
	ctx := t.Context()
	const (
		community wa.JID = "120363200@g.us"
		cars      wa.JID = "120363201@g.us"
		phones    wa.JID = "120363202@g.us"
		news      wa.JID = "120363203@g.us"
	)
	g := Group{JID: cars, Name: "Cars", Status: GroupActive, Kind: KindGroup, Community: community, CommunityName: "Riyadh Market"}
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := gs.UpsertGroup(ctx, Group{JID: community, Name: "Riyadh Market", Status: GroupPendingAdmin, Kind: KindCommunity}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := gs.Group(ctx, cars); got.Community != community || got.CommunityName != "Riyadh Market" || got.Kind != KindGroup {
		t.Errorf("stored group = %+v, want its community, the community's name and kind group", got)
	}
	if got, _, _ := gs.Group(ctx, community); got.Kind != KindCommunity || got.IsChat() {
		t.Errorf("community row = %+v, want kind community (not a chat)", got)
	}

	steps := []struct {
		name string
		do   func() error
		want []wa.SubGroup
	}{
		{"set", func() error {
			return gs.SetCommunityGroups(ctx, community, []wa.SubGroup{{JID: phones, Name: "Phones"}, {JID: cars, Name: "Cars"}, {JID: news, Name: "News", IsAnnouncement: true}})
		}, []wa.SubGroup{{JID: cars, Name: "Cars"}, {JID: phones, Name: "Phones"}, {JID: news, Name: "News", IsAnnouncement: true}}},
		{"a group listed twice is stored once", func() error {
			return gs.SetCommunityGroups(ctx, community, []wa.SubGroup{{JID: cars, Name: "Cars"}, {JID: cars, Name: "Cars"}})
		}, []wa.SubGroup{{JID: cars, Name: "Cars"}}},
		{"replace drops the missing", func() error {
			return gs.SetCommunityGroups(ctx, community, []wa.SubGroup{{JID: cars, Name: "Cars 2"}})
		}, []wa.SubGroup{{JID: cars, Name: "Cars 2"}}},
		{"link keeps a known name", func() error {
			return gs.LinkCommunityGroup(ctx, community, wa.SubGroup{JID: cars})
		}, []wa.SubGroup{{JID: cars, Name: "Cars 2"}}},
		{"link adds", func() error {
			return gs.LinkCommunityGroup(ctx, community, wa.SubGroup{JID: phones, Name: "Phones"})
		}, []wa.SubGroup{{JID: cars, Name: "Cars 2"}, {JID: phones, Name: "Phones"}}},
		{"unlink", func() error { return gs.UnlinkCommunityGroup(ctx, community, cars) }, []wa.SubGroup{{JID: phones, Name: "Phones"}}},
		{"empty list", func() error { return gs.SetCommunityGroups(ctx, community, nil) }, []wa.SubGroup{}},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		got, err := gs.CommunityGroups(ctx, community)
		if err != nil || (!slices.Equal(got, s.want) && (len(got) != 0 || len(s.want) != 0)) {
			t.Errorf("%s: list = %v, %v; want %v", s.name, got, err, s.want)
		}
	}
}

func TestMemoryGroupStoreCommunities(t *testing.T) { checkCommunityStore(t, NewMemoryGroupStore()) }
