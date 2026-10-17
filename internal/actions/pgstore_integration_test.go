//go:build integration

package actions_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestPGStoreContract(t *testing.T) {
	storeSuite(t, func(t *testing.T) actions.Store { return actions.NewPGStore(storetest.Pool(t), nil) })
}

// TestEnqueueTxCommitsWithTheDetection: an action enqueued in the caller's transaction exists
// only if that transaction commits (DESIGN.md §10.1).
func TestEnqueueTxCommitsWithTheDetection(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	s := actions.NewPGStore(pool, nil)
	a := revoke(group(1), "m1", time.Now(), actions.P1)
	a.Bot = bot
	errDetection := errors.New("detection failed")
	var id int64
	err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		res, err := s.EnqueueTx(ctx, tx, a)
		id = res.ID
		if err != nil {
			return err
		}
		return errDetection
	})
	if !errors.Is(err, errDetection) {
		t.Fatalf("InTx = %v", err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, actions.ErrNotFound) {
		t.Errorf("action of a rolled-back detection: Get = %v, want ErrNotFound", err)
	}
	if err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := s.EnqueueTx(ctx, tx, a)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if cands, err := s.Candidates(ctx, bot, 10); err != nil || len(cands) != 1 {
		t.Errorf("after the commit: %d candidates, %v; want 1", len(cands), err)
	}
}

// TestPGLockAcrossStores: two stores on the same database (two processes) can't both run an
// executor for one bot.
func TestPGLockAcrossStores(t *testing.T) {
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
	a, b := open(), open()
	unlock, err := a.Lock(ctx, bot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Lock(ctx, bot); !errors.Is(err, actions.ErrExecutorRunning) {
		t.Errorf("Lock from a second process = %v, want ErrExecutorRunning", err)
	}
	unlock()
	if unlockB, err := b.Lock(ctx, bot); err != nil {
		t.Errorf("Lock after the first released it = %v", err)
	} else {
		unlockB()
	}
}

// TestPGClaimSkipsLockedRows: a row locked by another transaction (a cancel in flight) is
// skipped, not waited for.
func TestPGClaimSkipsLockedRows(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	s := actions.NewPGStore(pool, nil)
	a := revoke(group(1), "m1", time.Now(), actions.P1)
	a.Bot = bot
	id := mustEnqueue(ctx, t, s, a)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT id FROM app.actions WHERE id = $1 FOR UPDATE", id); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok, _ := s.Claim(ctx, id)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Error("claimed a row locked by another transaction")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Claim waited for a locked row, want SKIP LOCKED")
	}
}

// TestOperatorAuditsInTheSameTransaction: a queue control change and its audit_log row commit
// together.
func TestOperatorAuditsInTheSameTransaction(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	op := actions.Operator{Store: actions.NewPGStore(pool, nil)}
	if err := op.Pause(ctx, "operator", "investigating"); err != nil {
		t.Fatal(err)
	}
	if err := op.SetCeiling(ctx, bot, "operator", 25); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app.audit_log WHERE action IN ('actions.pause', 'actions.ceiling')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("%d audit rows, want 2 (every queue control change is audited)", n)
	}
}

// TestExecutorOnPostgres runs the executor against the Postgres store end to end: actions run
// once in queue order, and a restart doesn't repeat finished work.
func TestExecutorOnPostgres(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	s := actions.NewPGStore(pool, nil)
	client := newTimed()
	cfg := actions.DefaultConfig()
	cfg.Spacing, cfg.Jitter, cfg.PollInterval = time.Millisecond, time.Millisecond, 20*time.Millisecond
	run := func() {
		ex, err := actions.NewExecutor(cfg, actions.Deps{Bot: bot, Client: client, Store: s, Groups: newGroups()})
		if err != nil {
			t.Fatal(err)
		}
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		wg.Go(func() {
			if err := ex.Run(rctx); err != nil {
				t.Errorf("Run = %v", err)
			}
		})
		wg.Wait()
	}
	for i, g := range []wa.JID{group(1), group(2), group(3)} {
		a := revoke(g, "m", time.Now().Add(-time.Duration(i)*time.Minute), actions.P1)
		a.Bot = bot
		mustEnqueue(ctx, t, s, a)
	}
	lock := actions.Action{Bot: bot, Chat: group(4), Kind: actions.KindAnnounce, Priority: actions.P0, DedupeKey: "lock", Payload: actions.Payload{On: true}}
	client.SetGroup(wa.GroupInfo{JID: group(4)})
	mustEnqueue(ctx, t, s, lock)
	run()
	calls := client.writes()
	if len(calls) != 4 || calls[0].chat != group(4) {
		t.Fatalf("calls %+v, want 4 with the P0 lockdown first", calls)
	}
	run()
	if n := len(client.writes()); n != 4 {
		t.Errorf("%d calls after a restart, want still 4 (finished work isn't repeated)", n)
	}
}
