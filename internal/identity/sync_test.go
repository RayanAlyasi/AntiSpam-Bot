package identity

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestSyncJoined(t *testing.T) {
	env := newTracker(t)
	ctx := t.Context()
	other := wa.JID("777@g.us")
	gone := wa.JID("888@g.us")
	_ = env.groups.UpsertGroup(ctx, Group{JID: gone, Bot: botPN, Status: GroupActive})
	_ = env.groups.UpsertGroup(ctx, Group{JID: "999@g.us", Bot: "966511111111@s.whatsapp.net", Status: GroupActive})
	_ = env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: groupInfo(true, alice)})

	offline := groupInfo(false, bob)
	offline.JID = other // the bot was added to this group while it was offline
	if err := env.tr.SyncJoined(ctx, botPN, []wa.GroupInfo{groupInfo(true, alice), offline}); err != nil {
		t.Fatal(err)
	}
	p := mustMatch(t, env.svc, bob.JID)
	m, ok, _ := env.svc.Member(ctx, other, p.ID)
	if !ok || !m.PreExisting || !m.Established {
		t.Errorf("member of a group first seen by sync = %+v: want pre-existing (they were there before the bot, §8.1)", m)
	}
	if g, _, _ := env.groups.Group(ctx, gone); g.Status != GroupLeft {
		t.Errorf("group missing from the bot's list: status %s, want left", g.Status)
	}
	if g, _, _ := env.groups.Group(ctx, "999@g.us"); g.Status != GroupActive {
		t.Errorf("another bot's group: status %s, want untouched", g.Status)
	}
	if g, _, _ := env.groups.Group(ctx, grp); g.Status != GroupActive || !g.LastInfoRefresh.Equal(t0) {
		t.Errorf("known group %+v: want refreshed and still active", g)
	}
}

func TestGroupInfoFromMembers(t *testing.T) {
	env := newTracker(t)
	ctx := t.Context()
	_ = env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: groupInfo(true, alice, bob)})
	info, err := env.svc.GroupInfoFromMembers(ctx, grp)
	if err != nil || len(info.Participants) != 2 {
		t.Fatalf("GroupInfoFromMembers = %+v, %v; want both members", info, err)
	}
	parts, _ := NewParticipants(env.svc.GroupInfoFromMembers, env.clk, 10, 0)
	defer parts.Close()
	if ok, _ := parts.IsAdmin(ctx, grp, "9661@s.whatsapp.net"); !ok {
		t.Error("superadmin by PN not an admin in the members-backed cache")
	}
	if ok, _ := parts.IsAdmin(ctx, grp, bob.JID); ok {
		t.Error("plain member reported as admin")
	}
}
