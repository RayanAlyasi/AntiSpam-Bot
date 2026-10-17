package actions_test

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// TestOnFinishedReportsFinalActions: the hook hears about every action the executor finishes,
// with its final status and result, so a review post can be linked to its WhatsApp message ID.
func TestOnFinishedReportsFinalActions(t *testing.T) {
	within(t, func(t *testing.T) {
		var mu sync.Mutex
		got := map[int64]actions.Action{}
		store := actions.NewMemStore(clock.Real{})
		client, groups := newTimed(), newGroups()
		groups.silent[group(1)] = true
		ex, err := actions.NewExecutor(actions.DefaultConfig(), actions.Deps{
			Bot: bot, Client: client, Store: store, Groups: groups, Rand: rand.New(rand.NewPCG(1, 2)),
			OnFinished: func(_ context.Context, a actions.Action) error {
				mu.Lock()
				defer mu.Unlock()
				got[a.ID] = a
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		enqueue := func(a actions.Action) int64 {
			a.Bot = bot
			return mustEnqueue(ctx, t, store, a)
		}
		review := enqueue(send("700@s.whatsapp.net", "review", "Spam?", actions.PurposeReview))
		refused := enqueue(send(group(1), "post", "hi", actions.PurposeWarning))
		// Behind a group's worth of P1 deletes (10/min per group), a P4 delete due in 30 s expires.
		for i := range 15 {
			enqueue(revoke(group(2), "busy"+string(rune('a'+i)), time.Now(), actions.P1))
		}
		late := revoke(group(2), "late", time.Now(), actions.P4)
		late.Deadline = time.Now().Add(30 * time.Second)
		expired := enqueue(late)
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { _ = ex.Run(rctx); close(done) }()
		time.Sleep(time.Minute)
		cancel()
		<-done
		mu.Lock()
		defer mu.Unlock()
		if a := got[review]; a.Status != actions.StatusDone || a.Result.MessageID == "" {
			t.Errorf("review send reported as %s with message ID %q, want done with the sent message's ID", a.Status, a.Result.MessageID)
		}
		if a := got[refused]; a.Status != actions.StatusRefused {
			t.Errorf("silent-group post reported as %q, want refused", a.Status)
		}
		if a := got[expired]; a.Status != actions.StatusExpired {
			t.Errorf("delete past its deadline reported as %q, want expired", a.Status)
		}
	})
}
