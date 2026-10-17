//go:build integration

package actions_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestLockLossStopsCalls: when the executor's lock connection dies, that executor makes no
// more WhatsApp calls, so two executors never write for one bot at once (Q-001); another
// process may take over, and the first stands by.
func TestLockLossStopsCalls(t *testing.T) {
	ctx := context.Background()
	url := storetest.URL(t, true)
	open := func() *actions.PGStore {
		pool, err := store.Open(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return actions.NewPGStore(pool, nil)
	}
	cfg := actions.DefaultConfig()
	cfg.Spacing, cfg.Jitter, cfg.PollInterval = time.Millisecond, time.Millisecond, 20*time.Millisecond
	cfg.CeilingPerMin, cfg.Burst, cfg.GroupPerMin = 600, 50, 600
	sa, sb := open(), open()
	ca, cb := newTimed(), newTimed()
	start := func(s *actions.PGStore, c *timedClient) (context.CancelFunc, *sync.WaitGroup) {
		ex, err := actions.NewExecutor(cfg, actions.Deps{Bot: bot, Client: c, Store: s, Groups: newGroups()})
		if err != nil {
			t.Fatal(err)
		}
		rctx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Go(func() { _ = ex.Run(rctx) })
		return cancel, &wg
	}
	stopA, wgA := start(sa, ca)
	defer func() { stopA(); wgA.Wait() }()
	time.Sleep(200 * time.Millisecond)

	// Kill A's lock connection, as a database restart or a dropped idle connection would.
	var killed bool
	conn, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.QueryRow(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype = 'advisory' AND granted LIMIT 1`).Scan(&killed); err != nil || !killed {
		t.Fatalf("terminate the lock backend: %v %v", killed, err)
	}
	stopB, wgB := start(sb, cb)
	defer func() { stopB(); wgB.Wait() }()
	time.Sleep(300 * time.Millisecond)

	for i := range 40 {
		a := revoke(group(i%4), "m"+string(rune('a'+i)), time.Now(), actions.P1)
		a.Bot = bot
		mustEnqueue(ctx, t, sa, a)
	}
	time.Sleep(3 * time.Second)
	if n := len(ca.CallsTo(fake.MethodRevoke)); n != 0 {
		t.Errorf("executor A made %d calls after losing its lock, want 0 (another executor may own the bot)", n)
	}
	if n := len(cb.CallsTo(fake.MethodRevoke)); n != 40 {
		t.Errorf("executor B made %d calls, want all 40 (it took the lock over)", n)
	}
}
