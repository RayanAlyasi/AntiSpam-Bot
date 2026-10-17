//go:build integration

package actions_test

import (
	"context"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

// TestDocumentedBreakerCloseSQL: the SQL in docs/action-queue.md closes an open breaker, and the
// executor takes the close.
func TestDocumentedBreakerCloseSQL(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	s := actions.NewPGStore(pool, nil)
	cfg := actions.DefaultConfig()
	cfg.PollInterval = 20 * time.Millisecond
	ex, err := actions.NewExecutor(cfg, actions.Deps{Bot: bot, Client: newTimed(), Store: s, Groups: newGroups()})
	if err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = ex.Run(rctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(200 * time.Millisecond)
	if err := ex.OpenBreaker(ctx, "temporary ban"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := pool.Exec(ctx, `UPDATE app.action_controls
SET state = state || jsonb_build_object('BreakerOpen', false, 'BreakerReason', '', 'BreakerClosedAt', now())
WHERE scope = $1`, string(bot)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for ex.State().BreakerOpen && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if ex.State().BreakerOpen {
		t.Error("breaker still open after the documented SQL, want closed within a poll interval")
	}
}
