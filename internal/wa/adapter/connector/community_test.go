package connector

import (
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestCommunityConversion: community flags reach GroupInfo, and a link change becomes a
// CommunityLink naming the community and the group, whichever side the event is about.
func TestCommunityConversion(t *testing.T) {
	community := types.NewJID("120363100", types.GroupServer)
	group := types.NewJID("120363101", types.GroupServer)

	info := groupInfo(&types.GroupInfo{
		JID: group, GroupLinkedParent: types.GroupLinkedParent{LinkedParentJID: community},
		GroupIsDefaultSub: types.GroupIsDefaultSub{IsDefaultSubGroup: true},
	})
	if info.LinkedParent != jid(community) || !info.IsAnnouncement || info.IsCommunity {
		t.Errorf("announcement group info = %+v, want its parent and IsAnnouncement", info)
	}
	if info := groupInfo(&types.GroupInfo{JID: community, GroupParent: types.GroupParent{IsParent: true}}); !info.IsCommunity {
		t.Errorf("community info = %+v, want IsCommunity", info)
	}

	target := func(j types.JID, name string, sub bool) types.GroupLinkTarget {
		return types.GroupLinkTarget{JID: j, GroupName: types.GroupName{Name: name}, GroupIsDefaultSub: types.GroupIsDefaultSub{IsDefaultSubGroup: sub}}
	}
	tests := []struct {
		name       string
		evt        *events.GroupInfo
		wantLink   *wa.CommunityLink
		wantUnlink *wa.CommunityLink
	}{
		{"group linked to its community", &events.GroupInfo{JID: group, Link: &types.GroupLinkChange{
			Type: types.GroupLinkChangeTypeParent, Group: target(community, "Riyadh Market", false),
		}}, &wa.CommunityLink{Community: jid(community), Group: jid(group), Name: "Riyadh Market"}, nil},
		{"community gained a group", &events.GroupInfo{JID: community, Link: &types.GroupLinkChange{
			Type: types.GroupLinkChangeTypeSub, Group: target(group, "Cars", true),
		}}, &wa.CommunityLink{Community: jid(community), Group: jid(group), Name: "Cars", IsAnnouncement: true}, nil},
		{"group unlinked", &events.GroupInfo{JID: group, Unlink: &types.GroupLinkChange{
			Type: types.GroupLinkChangeTypeParent, Group: target(community, "", false),
		}}, nil, &wa.CommunityLink{Community: jid(community), Group: jid(group)}},
		{"sibling link ignored", &events.GroupInfo{JID: group, Link: &types.GroupLinkChange{
			Type: types.GroupLinkChangeTypeSibling, Group: target(community, "x", false),
		}}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := groupEvent(t.Context(), bot, tt.evt, nil)
			if !reflect.DeepEqual(got.Link, tt.wantLink) || !reflect.DeepEqual(got.Unlink, tt.wantUnlink) {
				t.Errorf("link %+v, unlink %+v; want %+v, %+v", got.Link, got.Unlink, tt.wantLink, tt.wantUnlink)
			}
		})
	}
}
