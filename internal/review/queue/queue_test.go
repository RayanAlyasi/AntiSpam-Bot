package queue_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/queue"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	bot    wa.JID = "966500000000@s.whatsapp.net"
	group  wa.JID = "120363000000000001@g.us"
	rgroup wa.JID = "120363000000000099@g.us"
	spam   wa.JID = "200000000000001@lid"
	admin  wa.JID = "300000000000001@lid"
)

var start = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// banStore is an in-memory policy.BanStore.
type banStore struct {
	mu   sync.Mutex
	bans []policy.GroupBan
}

func (s *banStore) CreateBan(_ context.Context, b policy.GroupBan) (policy.GroupBan, bool, error) {
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

func (s *banStore) ActiveBan(_ context.Context, group wa.JID, pid int64) (policy.GroupBan, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.bans {
		if x.Group == group && x.PersonID == pid && x.Active {
			return x, true, nil
		}
	}
	return policy.GroupBan{}, false, nil
}

func (s *banStore) LiftBan(_ context.Context, group wa.JID, pid int64, by string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.bans {
		if x.Group == group && x.PersonID == pid && x.Active {
			s.bans[i].Active, s.bans[i].LiftedBy, s.bans[i].LiftedAt = false, by, at
			return true, nil
		}
	}
	return false, nil
}

type persons map[wa.JID]int64

func (p persons) PersonID(_ context.Context, j wa.JID) (int64, error) { return p[j], nil }

type rig struct {
	q     *queue.Queue
	store *actions.MemStore
	bans  *banStore
	posts []review.Posted
}

func newRig() *rig {
	c := clock.NewFake(start)
	r := &rig{store: actions.NewMemStore(c), bans: &banStore{}}
	botFor := func(context.Context, wa.JID) (wa.JID, error) { return bot, nil }
	r.q = &queue.Queue{
		Queue: r.store, BotFor: botFor, BanStore: r.bans,
		Bans:    policy.NewBans(r.bans, policy.QueueKicker{Queue: r.store, BotFor: botFor}, c),
		Persons: persons{spam: 7, admin: 9}, Marks: &queue.MemoryMarks{}, Clock: c,
		Posted: func(_ context.Context, p review.Posted) error { r.posts = append(r.posts, p); return nil },
	}
	return r
}

func (r *rig) kinds() []string {
	var out []string
	for _, a := range r.store.All() {
		out = append(out, string(a.Kind)+"@"+a.Priority.String())
	}
	return out
}

func TestEnqueueMapsEveryAction(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	msg := wa.MessageRef{Sender: spam, ID: "M1"}
	for _, a := range []review.Action{
		review.Delete{Group: group, Message: msg, Priority: review.P1},
		review.Ban{Group: group, Target: spam, Reason: review.ReasonSpam, Actor: admin, Via: "command"},
		review.Kick{Group: group, Target: spam},
		review.RejectJoin{Group: group, Targets: []wa.JID{spam, "1@lid"}, Reason: "blacklist"},
		review.Send{Chat: rgroup, Message: wa.OutgoingMessage{Text: "review"}, ReviewID: 4, Purpose: "review"},
		review.Edit{Chat: rgroup, ID: "BOT1", Text: "handled"},
		review.React{Chat: rgroup, Target: msg, Emoji: "👌"},
	} {
		if err := r.q.Enqueue(ctx, a); err != nil {
			t.Fatalf("Enqueue(%T): %v", a, err)
		}
	}
	got := strings.Join(r.kinds(), " ")
	want := "revoke@P1 kick@P2 kick@P2 reject_join@P2 reject_join@P2 send@P3 edit@P3 react@P3"
	if got != want {
		t.Errorf("queued %q, want %q (priorities per DESIGN §10.2)", got, want)
	}
	if b, ok, _ := r.bans.ActiveBan(ctx, group, 7); !ok || b.Reason != policy.BanSpam || b.DecidedBy != policy.DecidedAdmin || !b.RejoinBlock {
		t.Errorf("ban = %+v, %v; want an active spam ban by an admin with a rejoin block", b, ok)
	}
	for _, a := range r.store.All() {
		if a.Kind == actions.KindSend && a.Payload.Purpose != actions.PurposeReview {
			t.Errorf("review send purpose = %s, want review (its own sub-budget, §9.5 Pacing)", a.Payload.Purpose)
		}
	}
	// The same delete twice is one action.
	if err := r.q.Enqueue(ctx, review.Delete{Group: group, Message: msg, Priority: review.P1}); err != nil {
		t.Fatal(err)
	}
	if n := len(r.store.All()); n != 8 {
		t.Errorf("repeating a delete made %d actions, want 8 (deduplicated)", n)
	}
}

// TestBlacklistUnbanLiftsOnlyItsOwnBans: §9.8 Undo lifts the blacklist's block, never an
// admin's own ban.
func TestBlacklistUnbanLiftsOnlyItsOwnBans(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	if err := r.q.Enqueue(ctx, review.Ban{Group: group, Target: spam, Reason: review.ReasonSpam, Actor: admin, Via: "command"}); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Enqueue(ctx, review.Unban{Group: group, Target: spam, Via: "blacklist"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.bans.ActiveBan(ctx, group, 7); !ok {
		t.Fatal("a blacklist removal lifted an admin's .ban, want it kept")
	}
	if err := r.q.Enqueue(ctx, review.Unban{Group: group, Target: spam, Via: "command"}); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Enqueue(ctx, review.Ban{Group: group, Target: spam, Reason: review.ReasonOther, Via: "blacklist"}); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Enqueue(ctx, review.Unban{Group: group, Target: spam, Via: "blacklist"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.bans.ActiveBan(ctx, group, 7); ok {
		t.Error("a blacklist removal kept the blacklist's own ban, want it lifted")
	}
}

func TestSweepRecordsSentReviews(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	if err := r.q.Enqueue(ctx, review.Send{Chat: rgroup, Message: wa.OutgoingMessage{Text: "review"}, ReviewID: 4, Purpose: "review"}); err != nil {
		t.Fatal(err)
	}
	a := r.store.All()[0]
	if err := r.q.Sweep(ctx); err != nil || len(r.posts) != 0 {
		t.Fatalf("sweep before the send: %v, %d posts; want nothing yet", err, len(r.posts))
	}
	if _, ok, err := r.store.Claim(ctx, a.ID); !ok || err != nil {
		t.Fatal("claim", err)
	}
	if err := r.store.Finish(ctx, a.ID, actions.StatusDone, "", actions.Result{MessageID: "BOT9"}); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.posts) != 1 || r.posts[0].ReviewID != 4 || r.posts[0].ID != "BOT9" || r.posts[0].Chat != rgroup {
		t.Fatalf("posts = %+v, want review 4 mapped to BOT9 in the review group", r.posts)
	}
	if err := r.q.Sweep(ctx); err != nil || len(r.posts) != 1 {
		t.Errorf("a second sweep reported it again (%d posts), want once", len(r.posts))
	}
}
