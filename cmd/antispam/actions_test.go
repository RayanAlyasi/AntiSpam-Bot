package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/alert"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type noBots struct{}

func (noBots) Connected() []wa.JID               { return nil }
func (noBots) Client(wa.JID) (wa.WAClient, bool) { return nil, false }
func (noBots) Posting(context.Context, wa.JID) (actions.Posting, error) {
	return actions.Posting{Protected: true, Silent: true}, nil
}
func (noBots) NotAdmin(context.Context, wa.JID, wa.JID) error { return nil }

// TestAccountProblemOpensTheBreaker: the connector's temporary-ban event goes through the health
// tracker into the action runner and opens that bot's breaker (DESIGN.md §10.5).
func TestAccountProblemOpensTheBreaker(t *testing.T) {
	ctx := context.Background()
	bot := wa.JID("966500000001@s.whatsapp.net")
	s := actions.NewMemStore(nil)
	runner, err := actions.NewRunner(actions.DefaultConfig(), actions.RunnerDeps{Connector: noBots{}, Store: s, Groups: noBots{}})
	if err != nil {
		t.Fatal(err)
	}
	n := alert.NewNotifier(nil, 0, clock.Real{}, zerolog.Nop())
	health, err := alert.NewHealth(n, runner, prometheus.NewRegistry(), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range []wa.AccountProblem{wa.ProblemTemporaryBan, wa.ProblemLoggedOut, wa.ProblemStreamReplaced} {
		_, _ = s.UpdateControl(ctx, string(bot), func(c *actions.Control) { c.BreakerOpen = false })
		if err := health.HandleEvent(ctx, &wa.AccountEvent{Bot: bot, Problem: problem, Code: 101, Expire: time.Hour}); err != nil {
			t.Fatal(err)
		}
		if c, _ := s.Control(ctx, string(bot)); !c.BreakerOpen {
			t.Errorf("%s: breaker closed, want open (only P0 lockdowns may run)", problem)
		}
	}
}

type sinkFunc func(alert.Alert)

func (f sinkFunc) Name() string                                { return "test" }
func (f sinkFunc) Send(_ context.Context, a alert.Alert) error { f(a); return nil }

func TestActionsAlerterMapsKinds(t *testing.T) {
	var mu sync.Mutex
	var got []alert.Alert
	n := alert.NewNotifier([]alert.Sink{sinkFunc(func(a alert.Alert) { mu.Lock(); got = append(got, a); mu.Unlock() })}, 0, clock.Real{}, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = n.Run(ctx); close(done) }()
	a := actionsAlerter{n}
	bot := wa.JID("966500000001@s.whatsapp.net")
	_ = a.Alert(ctx, actions.Alert{Bot: bot, Kind: actions.AlertBreakerOpen, Message: "temporary ban"})
	_ = a.Alert(ctx, actions.Alert{Bot: bot, Kind: actions.AlertDailyWrites, Message: "7500"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		l := len(got)
		mu.Unlock()
		if l == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Severity != alert.Critical || got[1].Severity != alert.Warning || got[0].Key == got[1].Key {
		t.Errorf("alerts %+v, want a critical breaker alert and a warning for daily writes, with distinct keys", got)
	}
}

func TestMarkNotAdmin(t *testing.T) {
	ctx := context.Background()
	groups := identity.NewMemoryGroupStore()
	g := wa.JID("120300001@g.us")
	if err := groups.UpsertGroup(ctx, identity.Group{JID: g, BotIsAdmin: true}); err != nil {
		t.Fatal(err)
	}
	if err := markNotAdmin(groups)(ctx, "", g); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := groups.Group(ctx, g); got.BotIsAdmin {
		t.Error("group still shows the bot as admin after a not-admin error")
	}
	if err := markNotAdmin(groups)(ctx, "", "120300009@g.us"); err != nil {
		t.Errorf("unknown group: %v, want nil", err)
	}
}
