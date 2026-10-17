package actions_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// storeSuite checks the Store contract the executor relies on. It runs against MemStore here
// and against the Postgres store in the integration tests, so the two can't drift apart.
func storeSuite(t *testing.T, open func(t *testing.T) actions.Store) {
	ctx := context.Background()
	base := func(chat wa.JID, key string) actions.Action {
		a := revoke(chat, key, time.Now(), actions.P1)
		a.Bot = bot
		return a
	}

	t.Run("enqueue, dedupe and get", func(t *testing.T) {
		s := open(t)
		a := base(group(1), "m1")
		a.ReasonDetectionID = 42
		first, err := s.Enqueue(ctx, a)
		if err != nil || !first.Inserted {
			t.Fatalf("Enqueue = %+v, %v; want inserted", first, err)
		}
		again, err := s.Enqueue(ctx, a)
		if err != nil || again.Inserted || again.ID != first.ID {
			t.Errorf("second Enqueue = %+v, %v; want not inserted, ID %d (dedupe key)", again, err, first.ID)
		}
		got, err := s.Get(ctx, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != actions.StatusPending || got.Payload.Target.ID != "m1" || got.ReasonDetectionID != 42 ||
			got.Deadline.IsZero() || got.MessageTime.IsZero() {
			t.Errorf("stored %+v, want the action as enqueued, pending, with a deadline", got)
		}
		if _, err := s.Get(ctx, 9999); !errors.Is(err, actions.ErrNotFound) {
			t.Errorf("Get unknown = %v, want ErrNotFound", err)
		}
		late := base(group(1), "late")
		late.MessageTime = time.Now().Add(-49 * time.Hour)
		if res, err := s.Enqueue(ctx, late); err != nil || res.Inserted || res.Dropped == "" {
			t.Errorf("Enqueue past the delete window = %+v, %v; want dropped", res, err)
		}
		bad := base(group(1), "")
		if _, err := s.Enqueue(ctx, bad); !errors.Is(err, actions.ErrInvalid) {
			t.Errorf("Enqueue without a dedupe key = %v, want ErrInvalid", err)
		}
	})

	t.Run("claim, retry, finish and cancel", func(t *testing.T) {
		s := open(t)
		id := mustEnqueue(ctx, t, s, base(group(1), "m1"))
		a, ok, err := s.Claim(ctx, id)
		if err != nil || !ok || a.Status != actions.StatusRunning || a.Attempts != 1 {
			t.Fatalf("Claim = %+v, %v, %v; want running with 1 attempt", a, ok, err)
		}
		if _, ok, _ := s.Claim(ctx, id); ok {
			t.Error("a running action was claimed twice")
		}
		if err := s.Retry(ctx, id, actions.RetryInfo{NotBefore: time.Now(), Error: "held back"}); err != nil {
			t.Fatal(err)
		}
		if got := mustGet(ctx, t, s, id); got.Status != actions.StatusPending || got.Attempts != 0 {
			t.Errorf("after a retry that wasn't attempted: %s with %d attempts, want pending with 0", got.Status, got.Attempts)
		}
		mustClaim(ctx, t, s, id)
		res := actions.Result{Participants: []actions.ParticipantOutcome{{JID: member(1), Gone: true, Error: "gone"}}}
		if err := s.Retry(ctx, id, actions.RetryInfo{NotBefore: time.Now(), Called: true, Attempted: true, Result: res}); err != nil {
			t.Fatal(err)
		}
		if got := mustGet(ctx, t, s, id); got.Attempts != 1 || len(got.Result.Participants) != 1 || !got.Result.Participants[0].Gone {
			t.Errorf("after an attempted retry: %+v, want 1 attempt and the partial outcome kept", got)
		}
		mustClaim(ctx, t, s, id)
		if err := s.Finish(ctx, id, actions.StatusDone, "", actions.Result{SentText: "x"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(ctx, id, actions.StatusDone, "", actions.Result{}); err == nil {
			t.Error("finishing a done action succeeded, want an error")
		}
		if ok, err := s.Cancel(ctx, actions.RevokeKey(group(1), "m1"), "too late"); ok || err != nil {
			t.Errorf("Cancel of a done action = %v, %v; want false", ok, err)
		}
		other := mustEnqueue(ctx, t, s, base(group(1), "m2"))
		if ok, err := s.Cancel(ctx, actions.RevokeKey(group(1), "m2"), "sender deleted it"); !ok || err != nil {
			t.Fatalf("Cancel = %v, %v; want true", ok, err)
		}
		if got := mustGet(ctx, t, s, other); got.Status != actions.StatusCancelled {
			t.Errorf("cancelled action is %s", got.Status)
		}
		if n, err := s.WritesSince(ctx, bot, time.Now().Add(-time.Hour)); err != nil || n != 1 {
			t.Errorf("WritesSince = %d, %v; want 1 (the action that reached WhatsApp)", n, err)
		}
	})

	t.Run("candidates window per chat", func(t *testing.T) {
		s := open(t)
		for i := range 10 {
			mustEnqueue(ctx, t, s, base(group(1), fmt.Sprint("raid", i)))
		}
		quiet := mustEnqueue(ctx, t, s, base(group(2), "quiet"))
		urgent := base(group(1), "urgent")
		urgent.MessageTime = time.Now().Add(-47 * time.Hour)
		urgentID := mustEnqueue(ctx, t, s, urgent)
		cands, err := s.Candidates(ctx, bot, 2)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[int64]bool{}
		for _, c := range cands {
			ids[c.ID] = true
		}
		if !ids[quiet] || !ids[urgentID] {
			t.Errorf("candidates %v miss the other group's action or the urgent one", ids)
		}
		if len(cands) > 2+2+2+1 {
			t.Errorf("%d candidates for a window of 2, want at most 7", len(cands))
		}
	})

	t.Run("lock, recover, expire and fail pending", func(t *testing.T) {
		s := open(t)
		unlock, err := s.Lock(ctx, bot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Lock(ctx, bot); !errors.Is(err, actions.ErrExecutorRunning) {
			t.Errorf("second Lock = %v, want ErrExecutorRunning", err)
		}
		unlock()
		again, err := s.Lock(ctx, bot)
		if err != nil {
			t.Fatalf("Lock after unlock = %v", err)
		}
		again()

		running := mustEnqueue(ctx, t, s, base(group(1), "run"))
		mustClaim(ctx, t, s, running)
		if n, err := s.Recover(ctx, bot); err != nil || n != 1 {
			t.Errorf("Recover = %d, %v; want 1", n, err)
		}
		soon := base(group(1), "soon")
		soon.Deadline = time.Now().Add(time.Minute)
		soonID := mustEnqueue(ctx, t, s, soon)
		expired, err := s.ExpireDue(ctx, bot, time.Now().Add(2*time.Minute))
		if err != nil || len(expired) != 1 || expired[0].ID != soonID {
			t.Errorf("ExpireDue = %v, %v; want the one due action", expired, err)
		}
		k := kick(group(1), member(1))
		k.Bot = bot
		kickID := mustEnqueue(ctx, t, s, k)
		if pending, err := s.Pending(ctx, bot, group(1), actions.KindKick); err != nil || len(pending) != 1 || pending[0].ID != kickID {
			t.Errorf("Pending kicks = %v, %v; want the one kick", pending, err)
		}
		if n, err := s.FailPending(ctx, bot, group(1), []actions.Kind{actions.KindKick, actions.KindRevoke}, "not admin"); err != nil || n != 2 {
			t.Errorf("FailPending = %d, %v; want 2", n, err)
		}
	})

	t.Run("control state", func(t *testing.T) {
		s := open(t)
		if c, err := s.Control(ctx, "unknown"); err != nil || c.Paused || c.BreakerOpen {
			t.Errorf("Control of an unknown scope = %+v, %v; want zero", c, err)
		}
		at := time.Now().UTC().Truncate(time.Millisecond)
		if _, err := s.UpdateControl(ctx, string(bot), func(c *actions.Control) {
			c.BreakerOpen, c.BreakerReason, c.BreakerAt, c.Rate = true, "temp ban", at, 5
		}); err != nil {
			t.Fatal(err)
		}
		c, err := s.Control(ctx, string(bot))
		if err != nil || !c.BreakerOpen || c.BreakerReason != "temp ban" || !c.BreakerAt.Equal(at) || c.Rate != 5 {
			t.Errorf("Control = %+v, %v; want the saved state", c, err)
		}
	})
}

func mustEnqueue(ctx context.Context, t *testing.T, s actions.Store, a actions.Action) int64 {
	t.Helper()
	res, err := s.Enqueue(ctx, a)
	if err != nil || !res.Inserted {
		t.Fatalf("Enqueue = %+v, %v", res, err)
	}
	return res.ID
}

func mustGet(ctx context.Context, t *testing.T, s actions.Store, id int64) actions.Action {
	t.Helper()
	a, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustClaim(ctx context.Context, t *testing.T, s actions.Store, id int64) {
	t.Helper()
	if _, ok, err := s.Claim(ctx, id); !ok || err != nil {
		t.Fatalf("Claim %d = %v, %v", id, ok, err)
	}
}

func TestMemStoreContract(t *testing.T) {
	storeSuite(t, func(*testing.T) actions.Store { return actions.NewMemStore(nil) })
}
