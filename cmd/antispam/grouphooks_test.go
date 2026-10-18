package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const (
	hookGroup  wa.JID = "120363000000000001@g.us"
	otherGroup wa.JID = "120363000000000002@g.us"
	hookBot    wa.JID = "966500000000@s.whatsapp.net"
	listedPN   wa.JID = "966551234567@s.whatsapp.net"
	listedLID  wa.JID = "200000000000001@lid"
	otherLID   wa.JID = "200000000000002@lid"
	hookAdmin  wa.JID = "300@lid"
)

// modes is enforcingGroups over a map: groups missing from it aren't acted in.
type modes map[wa.JID]bool

func (m modes) Enforcing(_ context.Context, g wa.JID) (bool, error) { return m[g], nil }

type failingModes struct{}

func (failingModes) Enforcing(context.Context, wa.JID) (bool, error) {
	return false, errors.New("settings unavailable")
}

func listedService(t *testing.T, members ...blacklist.Member) (*blacklist.Service, *reviewtest.Actions) {
	t.Helper()
	groups := &blacklisttest.Groups{}
	groups.SetMembers(hookGroup, members...)
	acts := &reviewtest.Actions{}
	svc := blacklist.NewService(blacklist.Deps{
		List: blacklist.NewMemoryList(), Groups: groups, Actions: acts,
		Settings: &blacklisttest.Settings{Default: blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAuto}},
	})
	if _, err := svc.Add(context.Background(), blacklist.AddRequest{Group: hookGroup, Numbers: []string{"+966551234567"}, By: hookAdmin}); err != nil {
		t.Fatal(err)
	}
	acts.Reset()
	return svc, acts
}

// TestJoinHookBansListedJoiners: a listed person joining (matched through the event's LID↔PN
// pair) is banned; the bot itself and unlisted joiners are left alone (DESIGN §9.8). Groups the
// bot doesn't act in (shadow, paused, bot not admin, mode unknown) are left alone entirely (§8.4).
func TestJoinHookBansListedJoiners(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups enforcingGroups
		bans   int
	}{
		{"enforcing", modes{hookGroup: true}, 1},
		{"shadow, paused or not admin", modes{}, 0},
		{"mode unknown", failingModes{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, acts := listedService(t)
			h := &groupHooks{bl: svc, groups: tc.groups, log: zerolog.Nop()}
			h.groupEvent(context.Background(), nil, &wa.GroupEvent{
				Bot: hookBot, Group: hookGroup,
				Join:  []wa.JID{listedLID, otherLID, hookBot},
				Pairs: []wa.JIDPair{{LID: listedLID, PN: listedPN}},
			})
			bans := reviewtest.Of[review.Ban](acts)
			if len(bans) != tc.bans {
				t.Fatalf("bans = %+v, want %d", bans, tc.bans)
			}
			if tc.bans > 0 && (bans[0].Target != listedLID || bans[0].Via != "blacklist") {
				t.Errorf("ban = %+v, want the listed joiner banned by the blacklist", bans[0])
			}
		})
	}
}

type recordSink struct{ calls int }

func (r *recordSink) Handle(context.Context, inbox.Item, wa.MessageInfo, inbox.Decision) error {
	r.calls++
	return nil
}

// TestBlacklistSinkDeletesListedSenders: a listed member's message is deleted at P1 and they're
// banned; edits and revokes are left alone (their IDs aren't the message on screen), and the fast
// lane still sees every message.
func TestBlacklistSinkDeletesListedSenders(t *testing.T) {
	svc, acts := listedService(t, blacklist.Member{JID: listedLID, LID: listedLID, Phone: listedPN})
	next := &recordSink{}
	s := blacklistSink{next: next, bl: svc}
	ctx := context.Background()
	for _, info := range []wa.MessageInfo{
		{Chat: hookGroup, IsGroup: true, Sender: listedLID, SenderAlt: listedPN, ID: "M1"},
		{Chat: hookGroup, IsGroup: true, Sender: listedLID, SenderAlt: listedPN, ID: "E1", Edit: "1"},
		{Chat: hookGroup, IsGroup: true, Sender: listedLID, SenderAlt: listedPN, ID: "R1", Edit: "7"},
		{Chat: hookGroup, IsGroup: true, Sender: otherLID, ID: "M2"},
		{Chat: hookGroup, IsGroup: true, Sender: hookBot, IsFromMe: true, ID: "M3"},
	} {
		if err := s.Handle(ctx, inbox.Item{}, info, inbox.Decision{}); err != nil {
			t.Fatal(err)
		}
	}
	if next.calls != 5 {
		t.Errorf("fast lane saw %d messages, want all 5", next.calls)
	}
	dels := reviewtest.Of[review.Delete](acts)
	if len(dels) != 1 || dels[0].Message.ID != "M1" || dels[0].Priority != review.P1 {
		t.Errorf("deletes = %+v, want only the listed sender's message at P1", dels)
	}
	if bans := reviewtest.Of[review.Ban](acts); len(bans) != 1 || bans[0].Target != listedLID {
		t.Errorf("bans = %+v, want the listed sender banned", bans)
	}
}

// TestBlacklistSinkReturnsErrors: a failed blacklist check fails the item so the inbox retries
// it; otherwise the listed sender's message would never be deleted.
func TestBlacklistSinkReturnsErrors(t *testing.T) {
	svc, acts := listedService(t, blacklist.Member{JID: listedLID, LID: listedLID, Phone: listedPN})
	acts.Err = errors.New("queue unavailable")
	next := &recordSink{}
	s := blacklistSink{next: next, bl: svc}
	err := s.Handle(context.Background(), inbox.Item{}, wa.MessageInfo{Chat: hookGroup, IsGroup: true, Sender: listedLID, SenderAlt: listedPN, ID: "M1"}, inbox.Decision{})
	if err == nil {
		t.Error("Handle = nil, want the blacklist error so the inbox retries the item")
	}
	if next.calls != 0 {
		t.Errorf("fast lane ran %d times, want 0 until the retry", next.calls)
	}
}

// TestJoinRequestPollRejectsListed: in approval-mode groups with a connected bot, pending
// requests from listed people are rejected; other groups aren't read.
func TestJoinRequestPollRejectsListed(t *testing.T) {
	svc, acts := listedService(t)
	groups := identity.NewMemoryGroupStore()
	ctx := context.Background()
	for _, g := range []identity.Group{
		{JID: hookGroup, Bot: hookBot, JoinApproval: true, Status: identity.GroupActive},
		{JID: otherGroup, Bot: hookBot, Status: identity.GroupActive},
	} {
		if err := groups.UpsertGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	c := fake.New()
	c.SetJoinRequests(hookGroup, []wa.JoinRequest{{JID: listedPN}, {JID: otherLID}})
	c.SetJoinRequests(otherGroup, []wa.JoinRequest{{JID: listedPN}})
	h := &groupHooks{bl: svc, log: zerolog.Nop()}
	h.joinRequests(ctx, groups, func(bot wa.JID) (wa.WAClient, bool) { return c, bot == hookBot })
	rej := reviewtest.Of[review.RejectJoin](acts)
	if len(rej) != 1 || rej[0].Group != hookGroup || !slices.Equal(rej[0].Targets, []wa.JID{listedPN}) {
		t.Errorf("rejections = %+v, want only the listed requester in the approval-mode group", rej)
	}
	acts.Reset()
	h.joinRequests(ctx, groups, func(wa.JID) (wa.WAClient, bool) { return nil, false })
	if all := acts.All(); len(all) != 0 {
		t.Errorf("actions without a connected bot = %+v, want none", all)
	}
}

type onboardCall struct{ kind, bot, group, who wa.JID }

type recordOnboarding struct{ calls []onboardCall }

func (r *recordOnboarding) OnPromoted(_ context.Context, bot, group, by wa.JID) error {
	r.calls = append(r.calls, onboardCall{"promoted", bot, group, by})
	return nil
}

func (r *recordOnboarding) OnNotAdmin(_ context.Context, bot, group, addedBy wa.JID) error {
	r.calls = append(r.calls, onboardCall{"not-admin", bot, group, addedBy})
	return nil
}

// TestOnboardingHooks: queued events reach the onboarding DMs in order, off the receive loop.
// The promoting admin hears when the bot is promoted; whoever added the bot without admin rights
// gets the reminder; an admin bot and other people's promotions trigger nothing (§13.6).
func TestOnboardingHooks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recordOnboarding{}
		h := &groupHooks{onboard: rec, events: make(chan any, hookBacklog), log: zerolog.Nop()}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { h.run(ctx, nil); close(done) }()
		for _, evt := range []any{
			&wa.GroupEvent{Bot: hookBot, Group: hookGroup, Sender: hookAdmin, Promote: []wa.JID{hookBot}},
			&wa.GroupEvent{Bot: hookBot, Group: hookGroup, Sender: hookAdmin, Promote: []wa.JID{otherLID}},
			&wa.JoinedGroupEvent{Bot: hookBot, Sender: hookAdmin, Info: wa.GroupInfo{JID: otherGroup, Participants: []wa.Participant{{JID: hookBot}}}},
			&wa.JoinedGroupEvent{Bot: hookBot, Sender: hookAdmin, Info: wa.GroupInfo{JID: hookGroup, Participants: []wa.Participant{{JID: hookBot, IsAdmin: true}}}},
		} {
			h.enqueue(ctx, evt)
		}
		synctest.Wait()
		cancel()
		<-done
		want := []onboardCall{{"promoted", hookBot, hookGroup, hookAdmin}, {"not-admin", hookBot, otherGroup, hookAdmin}}
		if !slices.Equal(rec.calls, want) {
			t.Errorf("onboarding calls = %+v, want %+v", rec.calls, want)
		}
	})
}

func TestPersonFromPairs(t *testing.T) {
	pairs := []wa.JIDPair{{LID: listedLID, PN: listedPN}}
	if p := person(listedPN, pairs); p.LID != listedLID || p.Phone != listedPN {
		t.Errorf("person(phone) = %+v, want both identities", p)
	}
	if p := person(listedLID, pairs); p.LID != listedLID || p.Phone != listedPN {
		t.Errorf("person(LID) = %+v, want both identities", p)
	}
	if p := person(otherLID, nil); p.LID != otherLID || p.Phone != "" {
		t.Errorf("person(unpaired LID) = %+v", p)
	}
}
