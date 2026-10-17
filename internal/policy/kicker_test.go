package policy

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const bot wa.JID = "966500000000@s.whatsapp.net"

func TestLocalBanThroughTheQueue(t *testing.T) {
	ctx := context.Background()
	c := clock.NewFake(t0)
	q := actions.NewMemStore(c)
	woken := 0
	k := QueueKicker{Queue: q, BotFor: func(context.Context, wa.JID) (wa.JID, error) { return bot, nil }, Wake: func() { woken++ }}
	bans := NewBans(&memBans{}, k, c)

	req := BanRequest{Group: grp, PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: DecidedAuto, RejoinBlock: true}
	if _, err := bans.Ban(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := bans.Ban(ctx, req); err != nil { // the same decision again
		t.Fatal(err)
	}
	pending, err := q.Pending(ctx, bot, grp, actions.KindKick)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Priority != actions.P2 || len(pending[0].Payload.JIDs) != 1 || pending[0].Payload.JIDs[0] != member {
		t.Fatalf("queued kicks = %+v, want one P2 removal of the member (a repeated decision kicks once, §10.4)", pending)
	}

	c.Advance(time.Hour)
	if kicked, err := bans.OnJoin(ctx, Join{Group: grp, PersonID: 7, Member: member, At: c.Now()}); err != nil || !kicked {
		t.Fatalf("rejoin = %v, %v", kicked, err)
	}
	if _, err := bans.OnJoin(ctx, Join{Group: grp, PersonID: 7, Member: member, At: c.Now()}); err != nil {
		t.Fatal(err) // a duplicate join event
	}
	pending, _ = q.Pending(ctx, bot, grp, actions.KindKick)
	p0 := 0
	for _, a := range pending {
		if a.Priority == actions.P0 {
			p0++
		}
	}
	if len(pending) != 2 || p0 != 1 {
		t.Fatalf("queued kicks after a rejoin = %+v, want one more at P0 (keeping them out, §9.6)", pending)
	}
	if woken != 2 {
		t.Errorf("executor woken %d times, want once per new action", woken)
	}
}

func TestQueueKickerErrors(t *testing.T) {
	ctx := context.Background()
	noBot := QueueKicker{Queue: actions.NewMemStore(clock.NewFake(t0)), BotFor: func(context.Context, wa.JID) (wa.JID, error) {
		return "", errors.New("group has no bot")
	}}
	if err := noBot.Kick(ctx, grp, member, P2, "k"); err == nil || errors.Is(err, ErrInvalidBan) {
		t.Errorf("no bot for the group: %v, want the lookup error", err)
	}
	k := QueueKicker{Queue: actions.NewMemStore(clock.NewFake(t0)), BotFor: func(context.Context, wa.JID) (wa.JID, error) { return bot, nil }}
	if err := k.Kick(ctx, grp, "", P2, "k"); !errors.Is(err, ErrInvalidBan) {
		t.Errorf("kick without a member: %v, want ErrInvalidBan", err)
	}
	dropped := QueueKicker{Queue: dropQueue{}, BotFor: k.BotFor}
	if err := dropped.Kick(ctx, grp, member, P2, "k"); !errors.Is(err, ErrKickDropped) {
		t.Errorf("queue dropped the kick: %v, want ErrKickDropped", err)
	}
}

type dropQueue struct{}

func (dropQueue) Enqueue(context.Context, actions.Action) (actions.EnqueueResult, error) {
	return actions.EnqueueResult{Dropped: "deadline passed"}, nil
}

func (dropQueue) Get(context.Context, int64) (actions.Action, error) { return actions.Action{}, nil }

func TestReBanAfterADeadKickKicksAgain(t *testing.T) {
	end := map[actions.Status]func(ctx context.Context, q *actions.MemStore, c *clock.Fake, id int64) error{
		actions.StatusFailed: func(ctx context.Context, q *actions.MemStore, _ *clock.Fake, id int64) error {
			if _, _, err := q.Claim(ctx, id); err != nil {
				return err
			}
			return q.Finish(ctx, id, actions.StatusFailed, "403 not admin", actions.Result{})
		},
		actions.StatusExpired: func(ctx context.Context, q *actions.MemStore, c *clock.Fake, _ int64) error {
			c.Advance(actions.DefaultTTL + time.Minute)
			_, err := q.ExpireDue(ctx, bot, c.Now())
			return err
		},
		actions.StatusCancelled: func(ctx context.Context, q *actions.MemStore, _ *clock.Fake, id int64) error {
			a, err := q.Get(ctx, id)
			if err != nil {
				return err
			}
			if ok, err := q.Cancel(ctx, a.DedupeKey, "test"); err != nil || !ok {
				return fmt.Errorf("cancel %s: %v, %w", a.DedupeKey, ok, err)
			}
			return nil
		},
		actions.StatusDone: func(ctx context.Context, q *actions.MemStore, _ *clock.Fake, id int64) error {
			if _, _, err := q.Claim(ctx, id); err != nil {
				return err
			}
			return q.Finish(ctx, id, actions.StatusDone, "", actions.Result{})
		},
	}
	for status, finish := range end {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			c := clock.NewFake(t0)
			q := actions.NewMemStore(c)
			bans := NewBans(&memBans{}, QueueKicker{Queue: q, BotFor: func(context.Context, wa.JID) (wa.JID, error) { return bot, nil }}, c)
			req := BanRequest{Group: grp, PersonID: 7, Member: member, Reason: BanSpam, DecidedBy: DecidedAdmin, RejoinBlock: true}
			for round := range 15 { // more dead kicks than any fixed retry budget
				if _, err := bans.Ban(ctx, req); err != nil {
					t.Fatal(err)
				}
				p, err := q.Pending(ctx, bot, grp, actions.KindKick)
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if status == actions.StatusDone && round > 0 {
					want = 0 // already kicked: banning again changes nothing
				}
				if len(p) != want {
					t.Fatalf("round %d after a %s kick: %d pending kicks, want %d (a dead kick must be retried)", round, status, len(p), want)
				}
				if want == 1 {
					if err := finish(ctx, q, c, p[0].ID); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

// stuckQueue always reports the same dead action, as a broken store might.
type stuckQueue struct{}

func (stuckQueue) Enqueue(context.Context, actions.Action) (actions.EnqueueResult, error) {
	return actions.EnqueueResult{ID: 5}, nil
}

func (stuckQueue) Get(context.Context, int64) (actions.Action, error) {
	return actions.Action{ID: 5, Status: actions.StatusFailed}, nil
}

func TestQueueKickerStopsWithoutProgress(t *testing.T) {
	k := QueueKicker{Queue: stuckQueue{}, BotFor: func(context.Context, wa.JID) (wa.JID, error) { return bot, nil }}
	if err := k.Kick(context.Background(), grp, member, P2, "k"); err == nil {
		t.Error("a queue that keeps returning the same dead action: nil, want an error instead of looping")
	}
}
