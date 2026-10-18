package blacklist_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestInactiveGroupsAreLeftAlone: in shadow or paused mode the blacklist acts on nothing, like
// the rest of moderation (DESIGN §8.4).
func TestInactiveGroupsAreLeftAlone(t *testing.T) {
	ctx := context.Background()
	const (
		g    wa.JID = "1@g.us"
		lid  wa.JID = "7@lid"
		pn   wa.JID = "966551234567@s.whatsapp.net"
		op   wa.JID = "9@lid"
		msgs        = "M1"
	)
	groups := &blacklisttest.Groups{}
	groups.SetMembers(g, blacklist.Member{JID: lid, LID: lid, Phone: pn})
	acts := &reviewtest.Actions{}
	svc := blacklist.NewService(blacklist.Deps{
		List: blacklist.NewMemoryList(), Groups: groups, Actions: acts, Reviews: &blacklisttest.Reviews{},
		Settings: &blacklisttest.Settings{Default: blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAuto, Inactive: true}},
	})
	if _, err := svc.Add(ctx, blacklist.AddRequest{Group: g, Numbers: []string{"+966551234567"}, By: op}); err != nil {
		t.Fatal(err)
	}
	p := blacklist.Person{JID: lid, LID: lid, Phone: pn}
	if _, err := svc.OnJoin(ctx, g, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OnJoinRequests(ctx, g, []blacklist.Person{p}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OnMessage(ctx, g, wa.MessageRef{Sender: lid, ID: msgs}, p); err != nil {
		t.Fatal(err)
	}
	if got := acts.All(); len(got) != 0 {
		t.Errorf("an inactive group got %+v, want no action at all", got)
	}
}
