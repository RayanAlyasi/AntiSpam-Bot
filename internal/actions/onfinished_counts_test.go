package actions_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestOnFinishedReportsEachActionOnce: coalesced kicks and the admin actions failed when the
// bot loses admin are each reported exactly once, with the status the store holds.
func TestOnFinishedReportsEachActionOnce(t *testing.T) {
	within(t, func(t *testing.T) {
		var mu sync.Mutex
		reports := map[int64][]actions.Status{}
		store := actions.NewMemStore(clock.Real{})
		client := newTimed()
		ex, err := actions.NewExecutor(actions.DefaultConfig(), actions.Deps{
			Bot: bot, Client: client, Store: store, Groups: newGroups(), Rand: rand.New(rand.NewPCG(1, 2)),
			OnFinished: func(_ context.Context, a actions.Action) error {
				mu.Lock()
				defer mu.Unlock()
				reports[a.ID] = append(reports[a.ID], a.Status)
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		kicked, lost := group(1), group(2)
		info := wa.GroupInfo{JID: kicked}
		for i := range 12 {
			info.Participants = append(info.Participants, wa.Participant{JID: member(i)})
		}
		client.SetGroup(info)
		for i := range 12 {
			k := kick(kicked, member(i))
			k.Bot = bot
			mustEnqueue(ctx, t, store, k)
		}
		// In the other group the first delete finds the bot no longer admin; the rest fail with it.
		client.FailNext(fake.MethodRevoke, wa.ErrNotAdmin)
		for i := range 4 {
			a := revoke(lost, fmt.Sprint("m", i), time.Now().Add(-time.Duration(i)*time.Minute), actions.P1)
			a.Bot = bot
			mustEnqueue(ctx, t, store, a)
		}
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { _ = ex.Run(rctx); close(done) }()
		time.Sleep(5 * time.Minute)
		cancel()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, a := range store.All() {
			got := reports[a.ID]
			if len(got) != 1 || got[0] != a.Status {
				t.Errorf("action %d (%s, %s in the store) reported %v, want exactly once with its final status", a.ID, a.Kind, a.Status, got)
			}
		}
	})
}
