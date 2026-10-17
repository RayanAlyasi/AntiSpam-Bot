package actions_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// fakeConnector is the connector seen by the Runner: which bots are connected, with which client.
type fakeConnector struct {
	mu      sync.Mutex
	clients map[wa.JID]wa.WAClient
}

func (c *fakeConnector) set(bot wa.JID, client wa.WAClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client == nil {
		delete(c.clients, bot)
		return
	}
	c.clients[bot] = client
}

func (c *fakeConnector) Connected() []wa.JID {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []wa.JID
	for b := range c.clients {
		out = append(out, b)
	}
	slices.Sort(out)
	return out
}

func (c *fakeConnector) Client(bot wa.JID) (wa.WAClient, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl, ok := c.clients[bot]
	return cl, ok
}

func newRunner(t *testing.T, conn *fakeConnector, s actions.Store) *actions.Runner {
	t.Helper()
	r, err := actions.NewRunner(actions.DefaultConfig(), actions.RunnerDeps{Connector: conn, Store: s, Groups: newGroups(), Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func startRunner(t *testing.T, r *actions.Runner) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// TestRunnerFollowsConnectedBots: a bot's executor runs while it is connected, stops when it
// goes away and restarts with a new client after a reconnect.
func TestRunnerFollowsConnectedBots(t *testing.T) {
	within(t, func(t *testing.T) {
		conn := &fakeConnector{clients: map[wa.JID]wa.WAClient{}}
		s := actions.NewMemStore(clock.Real{})
		r := newRunner(t, conn, s)
		startRunner(t, r)
		ctx := context.Background()
		first := fake.New()
		conn.set(bot, first)
		a := revoke(group(1), "m1", time.Now(), actions.P1)
		a.Bot = bot
		mustEnqueue(ctx, t, s, a)
		time.Sleep(10 * time.Second)
		if n := len(first.CallsTo(fake.MethodRevoke)); n != 1 {
			t.Fatalf("%d revokes through the connected bot, want 1", n)
		}
		if _, ok := r.Executor(bot); !ok {
			t.Error("no executor for a connected bot")
		}
		conn.set(bot, nil)
		time.Sleep(10 * time.Second)
		if _, ok := r.Executor(bot); ok {
			t.Error("executor still running after its bot went away")
		}
		second := fake.New()
		conn.set(bot, second)
		b := revoke(group(1), "m2", time.Now(), actions.P1)
		b.Bot = bot
		mustEnqueue(ctx, t, s, b)
		time.Sleep(10 * time.Second)
		if n := len(second.CallsTo(fake.MethodRevoke)); n != 1 || len(first.CallsTo(fake.MethodRevoke)) != 1 {
			t.Errorf("after a reconnect: %d revokes through the new client, %d through the old; want 1 and still 1",
				n, len(first.CallsTo(fake.MethodRevoke)))
		}
	})
}

// TestRunnerOneExecutorPerBotAcrossRunners: two processes with the same bot connected never both
// run its actions.
func TestRunnerOneExecutorPerBotAcrossRunners(t *testing.T) {
	within(t, func(t *testing.T) {
		s := actions.NewMemStore(clock.Real{})
		ca, cb := fake.New(), fake.New()
		ra := newRunner(t, &fakeConnector{clients: map[wa.JID]wa.WAClient{bot: ca}}, s)
		rb := newRunner(t, &fakeConnector{clients: map[wa.JID]wa.WAClient{bot: cb}}, s)
		startRunner(t, ra)
		startRunner(t, rb)
		ctx := context.Background()
		for i := range 6 {
			a := revoke(group(i), "m", time.Now(), actions.P1)
			a.Bot = bot
			mustEnqueue(ctx, t, s, a)
		}
		time.Sleep(time.Minute)
		na, nb := len(ca.CallsTo(fake.MethodRevoke)), len(cb.CallsTo(fake.MethodRevoke))
		if na+nb != 6 || (na != 0 && nb != 0) {
			t.Errorf("revokes: %d through one process, %d through the other; want all 6 through one", na, nb)
		}
	})
}

// TestRunnerTripAndDisconnect: an account problem opens the breaker, also for a bot whose
// executor isn't running here, and a disconnect right after a write slows the bot down.
func TestRunnerTripAndDisconnect(t *testing.T) {
	within(t, func(t *testing.T) {
		conn := &fakeConnector{clients: map[wa.JID]wa.WAClient{}}
		s := actions.NewMemStore(clock.Real{})
		r := newRunner(t, conn, s)
		startRunner(t, r)
		ctx := context.Background()
		other := wa.JID("200@s.whatsapp.net")
		if err := r.Trip(ctx, other, wa.ProblemLoggedOut, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if c, _ := s.Control(ctx, string(other)); !c.BreakerOpen {
			t.Error("Trip for a bot without a running executor didn't persist the open breaker")
		}
		client := fake.New()
		conn.set(bot, client)
		a := revoke(group(1), "m1", time.Now(), actions.P1)
		a.Bot = bot
		mustEnqueue(ctx, t, s, a)
		time.Sleep(8 * time.Second)
		ex, ok := r.Executor(bot)
		if !ok {
			t.Fatal("no executor for the connected bot")
		}
		if err := r.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Disconnected}); err != nil {
			t.Fatal(err)
		}
		if st := ex.State(); st.Rate >= float64(st.Ceiling) {
			t.Errorf("rate %g after a disconnect right after a write, want below the ceiling %d", st.Rate, st.Ceiling)
		}
		if err := r.Trip(ctx, bot, wa.ProblemTemporaryBan, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if !ex.State().BreakerOpen {
			t.Error("breaker closed after a temporary ban, want open")
		}
	})
}
