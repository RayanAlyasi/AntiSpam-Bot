package actions_test

import (
	"context"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestOneOutageIsOneWarning: the keepalive timeouts and disconnect of one outage right after a
// write slow the bot down once and don't open the breaker; the next outage counts again.
func TestOneOutageIsOneWarning(t *testing.T) {
	within(t, func(t *testing.T) {
		conn := &fakeConnector{clients: map[wa.JID]wa.WAClient{bot: fake.New()}}
		s := actions.NewMemStore(clock.Real{})
		r := newRunner(t, conn, s)
		startRunner(t, r)
		ctx := context.Background()
		a := revoke(group(1), "m1", time.Now(), actions.P1)
		a.Bot = bot
		mustEnqueue(ctx, t, s, a)
		time.Sleep(8 * time.Second)
		ex, ok := r.Executor(bot)
		if !ok {
			t.Fatal("no executor for the connected bot")
		}
		for _, st := range []wa.ConnectionState{wa.KeepAliveTimeout, wa.KeepAliveTimeout, wa.Disconnected} {
			_ = r.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: st})
		}
		if st := ex.State(); st.BreakerOpen || st.Rate != 5 {
			t.Errorf("after one outage: %+v, want rate 5 (one quarter cut) and the breaker closed", st)
		}
		_ = r.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Connected})
		b := revoke(group(1), "m2", time.Now(), actions.P1)
		b.Bot = bot
		mustEnqueue(ctx, t, s, b)
		time.Sleep(30 * time.Second)
		_ = r.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Disconnected})
		if st := ex.State(); st.Rate != 2 {
			t.Errorf("after a second outage: rate %g, want 2 (it counts again)", st.Rate)
		}
	})
}

// TestRunnerTakesOverWhenTheOwnerStops: a second process's runner takes the bot over once the
// first one stops.
func TestRunnerTakesOverWhenTheOwnerStops(t *testing.T) {
	within(t, func(t *testing.T) {
		s := actions.NewMemStore(clock.Real{})
		ca, cb := fake.New(), fake.New()
		ra := newRunner(t, &fakeConnector{clients: map[wa.JID]wa.WAClient{bot: ca}}, s)
		ctx, stopA := context.WithCancel(context.Background())
		doneA := make(chan struct{})
		go func() { _ = ra.Run(ctx); close(doneA) }()
		time.Sleep(time.Second)
		rb := newRunner(t, &fakeConnector{clients: map[wa.JID]wa.WAClient{bot: cb}}, s)
		startRunner(t, rb)
		time.Sleep(10 * time.Second)
		if _, ok := rb.Executor(bot); ok {
			t.Error("the second runner reports an executor while the first owns the bot")
		}
		stopA()
		<-doneA
		a := revoke(group(1), "m1", time.Now(), actions.P1)
		a.Bot = bot
		mustEnqueue(context.Background(), t, s, a)
		time.Sleep(20 * time.Second)
		if n := len(cb.CallsTo(fake.MethodRevoke)); n != 1 {
			t.Errorf("%d revokes through the second process after the first stopped, want 1 (it takes over)", n)
		}
	})
}
