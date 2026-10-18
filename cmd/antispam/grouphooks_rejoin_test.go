package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type memBans struct {
	mu   sync.Mutex
	bans []policy.GroupBan
}

func (s *memBans) CreateBan(_ context.Context, b policy.GroupBan) (policy.GroupBan, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.bans {
		if x.Group == b.Group && x.PersonID == b.PersonID && x.Active {
			return x, false, nil
		}
	}
	b.ID = int64(len(s.bans) + 1)
	s.bans = append(s.bans, b)
	return b, true, nil
}

func (s *memBans) ActiveBan(_ context.Context, g wa.JID, pid int64) (policy.GroupBan, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.bans {
		if x.Group == g && x.PersonID == pid && x.Active {
			return x, true, nil
		}
	}
	return policy.GroupBan{}, false, nil
}

func (s *memBans) LiftBan(_ context.Context, g wa.JID, pid int64, by string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.bans {
		if x.Group == g && x.PersonID == pid && x.Active {
			s.bans[i].Active, s.bans[i].LiftedBy, s.bans[i].LiftedAt = false, by, at
			return true, nil
		}
	}
	return false, nil
}

type onePerson struct{}

func (onePerson) PersonID(context.Context, wa.JID) (int64, error) { return 7, nil }

// localBan bans person 7 (listedLID) in hookGroup with a rejoin block, and counts the kicks
// enqueued after it.
func localBan(t *testing.T) (*policy.Bans, *memBans, *clock.Fake, func() int) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC))
	store := actions.NewMemStore(clk)
	mb := &memBans{}
	bans := policy.NewBans(mb, policy.QueueKicker{Queue: store, BotFor: func(context.Context, wa.JID) (wa.JID, error) { return hookBot, nil }}, clk)
	if _, err := bans.Ban(context.Background(), policy.BanRequest{Group: hookGroup, PersonID: 7, Member: listedLID, Reason: policy.BanSpam, DecidedBy: policy.DecidedAdmin, RejoinBlock: true}); err != nil {
		t.Fatal(err)
	}
	kicks := func() int {
		n := 0
		for _, a := range store.All() {
			if a.Kind == actions.KindKick {
				n++
			}
		}
		return n
	}
	return bans, mb, clk, kicks
}

// TestRejoinHookKicksBannedJoiners: a locally banned person who rejoins through a link is kicked
// again, but only in groups the bot acts in (shadow and paused do nothing, §8.4); one a current
// admin re-adds is not (DESIGN §9.6 "Keeping them out").
func TestRejoinHookKicksBannedJoiners(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups enforcingGroups
		kicks  int
	}{
		{"enforcing", modes{hookGroup: true}, 1},
		{"shadow, paused or not admin", modes{}, 0},
		{"mode unknown", failingModes{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			bans, _, clk, kicks := localBan(t)
			admins := &reviewtest.Admins{}
			admins.Set(hookGroup, hookAdmin, true)
			h := &groupHooks{bans: bans, persons: onePerson{}, admins: admins, groups: tc.groups, log: zerolog.Nop()}
			before := kicks()
			h.groupEvent(ctx, nil, &wa.GroupEvent{Bot: hookBot, Group: hookGroup, Join: []wa.JID{listedLID}, JoinReason: "invite", Timestamp: clk.Now()})
			if got := kicks() - before; got != tc.kicks {
				t.Fatalf("rejoin by link: %d new kicks, want %d (the rejoin block, F-077)", got, tc.kicks)
			}
			if tc.kicks == 0 {
				return
			}
			clk.Advance(time.Hour)
			before = kicks()
			h.groupEvent(ctx, nil, &wa.GroupEvent{Bot: hookBot, Group: hookGroup, Sender: hookAdmin, Join: []wa.JID{listedLID}, Timestamp: clk.Now()})
			if got := kicks() - before; got != 0 {
				t.Errorf("re-added by an admin: %d new kicks, want none (the admins' own decision)", got)
			}
		})
	}
}

// TestListedRejoinersKeepTheLocalBlock: the group's own rejoin block always applies to a listed
// person (§9.6), including when their entry only opens a review (an instance entry in an
// ask-admins group), and an admin's re-add doesn't lift it: the list wins, and removing the
// number is how admins let them back.
func TestListedRejoinersKeepTheLocalBlock(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     review.EnforcementMode
		instance bool
	}{
		{"auto, group entry", review.ModeAuto, false},
		{"ask-admins, instance entry", review.ModeAskBeforeAction, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			bans, mb, clk, kicks := localBan(t)
			acts := &reviewtest.Actions{}
			svc := blacklist.NewService(blacklist.Deps{
				List: blacklist.NewMemoryList(), Groups: &blacklisttest.Groups{}, Actions: acts,
				Settings: &blacklisttest.Settings{Default: blacklist.GroupSettings{UseInstance: true, Mode: tc.mode}},
			})
			if _, err := svc.Add(ctx, blacklist.AddRequest{Group: hookGroup, Numbers: []string{"+966551234567"}, By: hookAdmin, Operator: tc.instance, Instance: tc.instance}); err != nil {
				t.Fatal(err)
			}
			admins := &reviewtest.Admins{}
			admins.Set(hookGroup, hookAdmin, true)
			h := &groupHooks{bl: svc, bans: bans, persons: onePerson{}, admins: admins, groups: modes{hookGroup: true}, log: zerolog.Nop()}
			pairs := []wa.JIDPair{{LID: listedLID, PN: listedPN}}

			before := kicks()
			h.groupEvent(ctx, nil, &wa.GroupEvent{Bot: hookBot, Group: hookGroup, Join: []wa.JID{listedLID}, JoinReason: "invite", Pairs: pairs, Timestamp: clk.Now()})
			if got := kicks() - before; got != 1 {
				t.Errorf("listed rejoin by link: %d rejoin kicks, want 1 (the group's own block always applies)", got)
			}
			clk.Advance(time.Hour)
			h.groupEvent(ctx, nil, &wa.GroupEvent{Bot: hookBot, Group: hookGroup, Sender: hookAdmin, Join: []wa.JID{listedLID}, Pairs: pairs, Timestamp: clk.Now()})
			if _, active, err := mb.ActiveBan(ctx, hookGroup, 7); err != nil || !active {
				t.Errorf("local ban active after an admin re-add = %v (%v), want it kept: the list decides for listed people", active, err)
			}
		})
	}
}
