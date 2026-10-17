package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	botPN  = wa.JID("966500000000@s.whatsapp.net")
	botLID = wa.JID("900@lid")
	grp    = wa.JID("120363@g.us")
)

type trackerEnv struct {
	svc      *Service
	groups   GroupStore
	parts    *Participants
	tr       *Tracker
	clk      *clock.Fake
	fetches  int
	info     wa.GroupInfo
	fetchErr error
}

func newTracker(t *testing.T) *trackerEnv {
	t.Helper()
	env := &trackerEnv{clk: clock.NewFake(t0), groups: NewMemoryGroupStore()}
	env.svc = New(NewMemoryStore(), nil, nil, env.clk)
	parts, err := NewParticipants(func(_ context.Context, g wa.JID) (wa.GroupInfo, error) {
		env.fetches++
		return env.info, env.fetchErr
	}, env.clk, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(parts.Close)
	env.parts = parts
	self := func(wa.JID) (wa.JID, wa.JID) { return botPN, botLID }
	env.tr = NewTracker(env.svc, env.groups, parts, self, env.clk, zerolog.Nop())
	return env
}

func groupInfo(botAdmin bool, pts ...wa.Participant) wa.GroupInfo {
	all := append([]wa.Participant{{JID: botLID, PhoneNumber: botPN, IsAdmin: botAdmin}}, pts...)
	return wa.GroupInfo{JID: grp, Name: "Cars", Topic: "buy/sell", AddressingMode: "lid", Participants: all}
}

var (
	alice = wa.Participant{JID: "1@lid", PhoneNumber: "9661@s.whatsapp.net", IsSuperAdmin: true}
	bob   = wa.Participant{JID: "2@lid"}
)

func (env *trackerEnv) member(t *testing.T, j wa.JID) (Member, bool) {
	t.Helper()
	p, ok, err := env.svc.MatchPerson(t.Context(), j)
	if err != nil || !ok {
		return Member{}, false
	}
	m, ok, err := env.svc.Member(t.Context(), grp, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m, ok
}

func TestJoinedGroupSeedsPreExistingMembers(t *testing.T) {
	env := newTracker(t)
	ctx := t.Context()
	err := env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{
		Bot: botPN, Info: groupInfo(false, alice, bob),
		Sender: "1@lid", SenderPN: "9661@s.whatsapp.net",
	})
	if err != nil {
		t.Fatal(err)
	}
	g, ok, _ := env.groups.Group(ctx, grp)
	if !ok || g.Status != GroupPendingAdmin || g.BotIsAdmin || g.Name != "Cars" || g.Size != 3 || !g.LastInfoRefresh.Equal(t0) {
		t.Errorf("group %+v: want pending_admin (bot not admin), name, size 3 and refresh time", g)
	}
	for _, p := range []wa.Participant{alice, bob} {
		m, ok := env.member(t, p.JID)
		if !ok || !m.PreExisting || !m.Established || !m.JoinedAt.IsZero() {
			t.Errorf("member %s = %+v: want pre-existing and established (§8.1: history is not punished)", p.JID, m)
		}
	}
	if m, _ := env.member(t, alice.JID); m.Role != RoleSuperAdmin {
		t.Errorf("alice role %s, want superadmin", m.Role)
	}
	if _, ok, _ := env.svc.MatchPerson(ctx, botLID); ok {
		t.Error("the bot itself became a person/member, want it skipped")
	}
	ids, _ := env.svc.Identities(ctx, mustMatch(t, env.svc, "1@lid").ID)
	if len(ids) != 2 {
		t.Fatalf("alice identities %+v: want LID and PN", ids)
	}
	if role, found, ok := env.parts.CachedRole(grp, "9661@s.whatsapp.net"); !ok || !found || role != RoleSuperAdmin {
		t.Errorf("participant cache by PN: %s %v %v, want superadmin from the join snapshot", role, found, ok)
	}
}

func mustMatch(t *testing.T, svc *Service, j wa.JID) Person {
	t.Helper()
	p, ok, err := svc.MatchPerson(t.Context(), j)
	if err != nil || !ok {
		t.Fatalf("MatchPerson(%s): %v %v", j, ok, err)
	}
	return p
}

func TestGroupEvents(t *testing.T) {
	env := newTracker(t)
	ctx := t.Context()
	if err := env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: groupInfo(false, alice)}); err != nil {
		t.Fatal(err)
	}
	name := "Cars KSA"
	at := t0.Add(time.Hour)
	steps := []struct {
		name  string
		evt   wa.GroupEvent
		check func(t *testing.T)
	}{
		{"bot promoted activates protection", wa.GroupEvent{Promote: []wa.JID{botLID}}, func(t *testing.T) {
			if g, _, _ := env.groups.Group(ctx, grp); g.Status != GroupActive || !g.BotIsAdmin {
				t.Errorf("group %+v: want active and bot admin", g)
			}
		}},
		{"join records a new member", wa.GroupEvent{
			Join: []wa.JID{"3@lid"}, Timestamp: at,
			Pairs: []wa.JIDPair{{LID: "3@lid", PN: "9663@s.whatsapp.net"}}, Sender: "5@lid", SenderPN: "9665@s.whatsapp.net",
		}, func(t *testing.T) {
			m, ok := env.member(t, "9663@s.whatsapp.net")
			if !ok || m.PreExisting || m.Established || !m.JoinedAt.Equal(at) {
				t.Errorf("new member %+v: want joined at %v, not pre-existing (gets the just-joined signal)", m, at)
			}
			if g, _, _ := env.groups.Group(ctx, grp); g.Size != 3 {
				t.Errorf("size %d, want 3", g.Size)
			}
			ids, _ := env.svc.Identities(ctx, mustMatch(t, env.svc, "3@lid").ID)
			if len(ids) != 2 || ids[1].Source != SourceParticipant {
				t.Errorf("identities %+v: want the pair learned with source participant", ids)
			}
		}},
		{"promote member", wa.GroupEvent{Promote: []wa.JID{"3@lid"}}, func(t *testing.T) {
			if m, _ := env.member(t, "3@lid"); m.Role != RoleAdmin {
				t.Errorf("role %s, want admin", m.Role)
			}
			if ok, _ := env.parts.IsAdmin(ctx, grp, "9663@s.whatsapp.net"); !ok {
				t.Error("cache: promoted member not admin by PN")
			}
		}},
		{"demote member", wa.GroupEvent{Demote: []wa.JID{"3@lid"}}, func(t *testing.T) {
			if m, _ := env.member(t, "3@lid"); m.Role != RoleMember {
				t.Errorf("role %s, want member", m.Role)
			}
		}},
		{"rename", wa.GroupEvent{Name: &name}, func(t *testing.T) {
			if g, _, _ := env.groups.Group(ctx, grp); g.Name != name {
				t.Errorf("name %q, want %q", g.Name, name)
			}
		}},
		{"leave removes the member", wa.GroupEvent{Leave: []wa.JID{"3@lid"}}, func(t *testing.T) {
			if _, ok := env.member(t, "3@lid"); ok {
				t.Error("member still present after leaving")
			}
			if _, found, _ := env.parts.CachedRole(grp, "9663@s.whatsapp.net"); found {
				t.Error("cache still lists the member by PN")
			}
		}},
		{"bot demoted pauses protection", wa.GroupEvent{Demote: []wa.JID{botPN}}, func(t *testing.T) {
			if g, _, _ := env.groups.Group(ctx, grp); g.Status != GroupPendingAdmin || g.BotIsAdmin {
				t.Errorf("group %+v: want pending_admin", g)
			}
		}},
		{"bot removed", wa.GroupEvent{Leave: []wa.JID{"900:3@lid"}}, func(t *testing.T) {
			if g, _, _ := env.groups.Group(ctx, grp); g.Status != GroupLeft {
				t.Errorf("status %s, want left", g.Status)
			}
		}},
	}
	for _, s := range steps {
		s.evt.Bot, s.evt.Group = botPN, grp
		if err := env.tr.HandleEvent(ctx, &s.evt); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		t.Run(s.name, s.check)
	}
	if ids, _ := env.svc.Identities(ctx, mustMatch(t, env.svc, "5@lid").ID); len(ids) != 2 || ids[1].Source != SourceEvent {
		t.Errorf("change sender identities %+v: want PN learned from SenderPN with source event", ids)
	}
}

func TestRefreshPrunesAndDoesNotMarkNewcomersPreExisting(t *testing.T) {
	env := newTracker(t)
	ctx := t.Context()
	_ = env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: groupInfo(true, alice, bob)})
	env.clk.Advance(GroupRefreshEvery + time.Minute)
	carol := wa.Participant{JID: "4@lid", PhoneNumber: "9664@s.whatsapp.net"}
	env.info = groupInfo(true, alice, carol)
	n, err := env.tr.RefreshStale(ctx, func(_ context.Context, bot, g wa.JID) (wa.GroupInfo, error) {
		if bot != botPN || g != grp {
			t.Errorf("fetch(%s, %s), want the group's own bot", bot, g)
		}
		return env.info, nil
	})
	if err != nil || n != 1 {
		t.Fatalf("RefreshStale = %d, %v; want 1", n, err)
	}
	if _, ok := env.member(t, bob.JID); ok {
		t.Error("bob is gone from the group but still a member")
	}
	if m, ok := env.member(t, carol.JID); !ok || m.PreExisting || m.Established {
		t.Errorf("carol %+v: found by refresh without a join event, want not pre-existing (conservative)", m)
	}
	if m, _ := env.member(t, alice.JID); !m.PreExisting {
		t.Error("alice lost her pre-existing status on refresh")
	}
	if n, _ := env.tr.RefreshStale(ctx, nil); n != 0 {
		t.Errorf("second RefreshStale refreshed %d fresh groups, want 0", n)
	}
}

func TestRefreshStaleMarksVanishedGroupsLeft(t *testing.T) {
	env := newTracker(t)
	ctx := t.Context()
	_ = env.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: groupInfo(true, alice)})
	env.clk.Advance(GroupRefreshEvery)
	_, err := env.tr.RefreshStale(ctx, func(context.Context, wa.JID, wa.JID) (wa.GroupInfo, error) {
		return wa.GroupInfo{}, wa.ErrNotFound
	})
	if err != nil {
		t.Fatal(err)
	}
	if g, _, _ := env.groups.Group(ctx, grp); g.Status != GroupLeft {
		t.Errorf("status %s, want left for a group WhatsApp no longer shows the bot", g.Status)
	}
	// Errors other than not-found are reported and leave the group as it was.
	env2 := newTracker(t)
	_ = env2.tr.HandleEvent(ctx, &wa.JoinedGroupEvent{Bot: botPN, Info: groupInfo(true, alice)})
	env2.clk.Advance(GroupRefreshEvery)
	if _, err := env2.tr.RefreshStale(ctx, func(context.Context, wa.JID, wa.JID) (wa.GroupInfo, error) {
		return wa.GroupInfo{}, wa.ErrTimeout
	}); !errors.Is(err, wa.ErrTimeout) {
		t.Errorf("err = %v, want the timeout reported", err)
	}
	if g, _, _ := env2.groups.Group(ctx, grp); g.Status != GroupActive {
		t.Errorf("status %s after a timeout, want unchanged active", g.Status)
	}
}

func TestParticipantsRefreshWhenOlderThanMaxAge(t *testing.T) {
	env := newTracker(t)
	ctx := t.Context()
	env.info = groupInfo(true, alice)
	if ok, err := env.parts.IsAdmin(ctx, grp, "1:7@lid"); err != nil || !ok {
		t.Fatalf("IsAdmin(alice) = %v, %v; want true after fetching a missing group", ok, err)
	}
	env.clk.Advance(AdminMaxAge - time.Second)
	_, _ = env.parts.IsAdmin(ctx, grp, "1@lid")
	if env.fetches != 1 {
		t.Errorf("fetches = %d within the max age, want 1", env.fetches)
	}
	env.info = groupInfo(true, wa.Participant{JID: "1@lid"}) // alice demoted in WhatsApp
	env.clk.Advance(time.Second)
	if ok, _ := env.parts.IsAdmin(ctx, grp, "1@lid"); ok {
		t.Error("IsAdmin after 10 min still true, want a refresh to see the demotion (§13.3)")
	}
	if env.fetches != 2 {
		t.Errorf("fetches = %d, want 2", env.fetches)
	}
	env.fetchErr = wa.ErrTimeout
	env.clk.Advance(AdminMaxAge)
	if _, err := env.parts.IsAdmin(ctx, grp, "1@lid"); !errors.Is(err, wa.ErrTimeout) {
		t.Errorf("IsAdmin with failing fetch: %v, want the error (never trust a stale admin list)", err)
	}
	if ok, _ := env.parts.IsAdmin(ctx, grp, "1203@g.us"); ok {
		t.Error("a group JID is never an admin")
	}
}

func TestMembersCountMessages(t *testing.T) {
	svc, _, _ := newService(nil)
	ctx := t.Context()
	p, _ := svc.Ensure(ctx, "1@lid", SourceEvent)
	for _, clean := range []bool{true, true, false} {
		if err := svc.CountMessage(ctx, grp, p.ID, clean); err != nil {
			t.Fatal(err)
		}
	}
	m, ok, _ := svc.Member(ctx, grp, p.ID)
	if !ok || m.MsgCount != 3 || m.CleanCount != 2 || !m.FirstSeen.Equal(t0) {
		t.Errorf("member %+v: want 3 messages, 2 clean, first seen %v", m, t0)
	}
	if err := svc.CountMessage(ctx, "1@lid", p.ID, true); err == nil {
		t.Error("CountMessage on a non-group JID: nil error")
	}
}
