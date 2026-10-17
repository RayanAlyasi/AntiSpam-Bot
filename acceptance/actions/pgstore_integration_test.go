//go:build integration

package actions_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// ---- Postgres helpers (real time: the store and executor run on clock.Real) ----

// pgPools returns n pools on one fresh, migrated database: each pool stands for a separate
// process of the bot.
func pgPools(t *testing.T, n int) []*pgxpool.Pool {
	t.Helper()
	url := storetest.URL(t, true)
	pools := make([]*pgxpool.Pool, n)
	for i := range pools {
		p, err := store.Open(context.Background(), url)
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		t.Cleanup(p.Close)
		pools[i] = p
	}
	return pools
}

// pgConfig is a fast executor configuration for real-time tests: tiny spacing and polling,
// a high ceiling, everything else at its defaults.
func pgConfig() actions.Config {
	cfg := actions.DefaultConfig()
	cfg.CeilingPerMin = 600
	cfg.Burst = 10
	cfg.Spacing = 20 * time.Millisecond
	cfg.Jitter = 20 * time.Millisecond
	cfg.GroupPerMin = 100
	cfg.KickWindow = 200 * time.Millisecond
	cfg.PollInterval = 100 * time.Millisecond
	return cfg
}

// pgExec is one bot's executor on a PGStore with the fake WhatsApp client.
type pgExec struct {
	t      *testing.T
	exec   *actions.Executor
	wa     *fake.Client
	rec    *recorder
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	once   sync.Once
}

// newPGExec builds an executor for bot on s; start runs it.
func newPGExec(t *testing.T, cfg actions.Config, s actions.Store, bot wa.JID, seed uint64) *pgExec {
	t.Helper()
	e := &pgExec{t: t, wa: fake.New(), done: make(chan struct{})}
	e.rec = newRecorder(e.wa)
	exec, err := actions.NewExecutor(cfg, actions.Deps{
		Bot: bot, Client: e.rec, Store: s, Clock: clock.Real{},
		Rand: rand.New(rand.NewPCG(seed, 7)), Groups: newFakeGroups(), Alerts: &fakeAlerts{}, Log: zerolog.Nop(),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v, want nil for a valid config and deps", err)
	}
	e.exec = exec
	return e
}

// start runs the executor until stop or the end of the test.
func (e *pgExec) start() *pgExec {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	go func() {
		e.err = e.exec.Run(ctx)
		close(e.done)
	}()
	e.t.Cleanup(e.stop)
	return e
}

// stop cancels Run and checks that it returned nil.
func (e *pgExec) stop() {
	e.once.Do(func() {
		e.cancel()
		select {
		case <-e.done:
		case <-time.After(10 * time.Second):
			e.t.Errorf("Run didn't return within 10 s of its context being cancelled, want a prompt stop (DESIGN §10.1)")
			return
		}
		if e.err != nil {
			e.t.Errorf("Run returned %v after its context was cancelled, want nil (executor contract, DESIGN §10.1)", e.err)
		}
	})
}

// checkRunning fails the test if Run returned before it was stopped.
func (e *pgExec) checkRunning() {
	e.t.Helper()
	select {
	case <-e.done:
		e.t.Fatalf("Run returned early with %v, want it to keep running until cancelled (DESIGN §10.1)", e.err)
	default:
	}
}

// waitFor polls cond every 20 ms until it holds, or fails with msg() after limit.
func waitFor(t *testing.T, limit time.Duration, cond func() bool, msg func() string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func pgStatus(t *testing.T, s actions.Store, id int64) actions.Status {
	t.Helper()
	a, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return a.Status
}

func pgEnqueue(t *testing.T, s actions.Store, a actions.Action) int64 {
	t.Helper()
	res, err := s.Enqueue(context.Background(), a)
	if err != nil || !res.Inserted {
		t.Fatalf("Enqueue(%s %s) = %+v, %v; want inserted", a.Kind, a.DedupeKey, res, err)
	}
	return res.ID
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// createDetections makes a stand-in for the detections table, so a test can write "the
// detection" and its action in one transaction.
func createDetections(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"CREATE TABLE public.test_detections (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, chat text NOT NULL)"); err != nil {
		t.Fatalf("create test_detections: %v", err)
	}
}

// ---- F-064: actions table and transactional enqueue ----

func TestPGEnqueueTxCommitsWithDetection(t *testing.T) {
	errReject := errors.New("detection rejected")
	for _, tc := range []struct {
		name   string
		commit bool
	}{{"commit", true}, {"rollback", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool := pgPools(t, 1)[0]
			createDetections(t, pool)
			s := actions.NewPGStore(pool, clock.Real{})
			g := group(1)
			a := kick(g, "det", member(1), member(2))
			var res actions.EnqueueResult
			before := time.Now()
			err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
				var det int64
				if err := tx.QueryRow(ctx, "INSERT INTO public.test_detections (chat) VALUES ($1) RETURNING id", string(g)).Scan(&det); err != nil {
					return err
				}
				a.ReasonDetectionID = det
				var err error
				if res, err = s.EnqueueTx(ctx, tx, a); err != nil {
					return err
				}
				if !tc.commit {
					return errReject
				}
				return nil
			})
			if tc.commit && err != nil {
				t.Fatalf("InTx: %v, want nil", err)
			}
			if !tc.commit && !errors.Is(err, errReject) {
				t.Fatalf("InTx = %v, want the callback's error", err)
			}
			if !res.Inserted || res.ID == 0 {
				t.Fatalf("EnqueueTx = %+v, want Inserted with an ID (a new dedupe key)", res)
			}
			dets := countRows(t, pool, "SELECT count(*) FROM public.test_detections")
			rows := countRows(t, pool, "SELECT count(*) FROM app.actions WHERE dedupe_key = $1", a.DedupeKey)

			if !tc.commit {
				if dets != 0 || rows != 0 {
					t.Fatalf("after rollback: %d detections, %d actions, want 0 and 0 (an action rolls back with the detection that caused it, DESIGN §10.1)", dets, rows)
				}
				if _, err := s.Get(ctx, res.ID); !errors.Is(err, actions.ErrNotFound) {
					t.Fatalf("Get(rolled-back ID) = %v, want ErrNotFound (nothing of a rolled-back enqueue survives)", err)
				}
				again, err := s.Enqueue(ctx, a)
				if err != nil || !again.Inserted {
					t.Fatalf("Enqueue after rollback = %+v, %v; want Inserted (a rolled-back enqueue must not use up its dedupe key)", again, err)
				}
				return
			}

			if dets != 1 || rows != 1 {
				t.Fatalf("after commit: %d detections, %d actions, want 1 and 1 (the action commits with its detection, DESIGN §10.1)", dets, rows)
			}
			got, err := s.Get(ctx, res.ID)
			if err != nil {
				t.Fatalf("Get(%d): %v", res.ID, err)
			}
			if got.Status != actions.StatusPending || got.Bot != a.Bot || got.Chat != g || got.Kind != actions.KindKick ||
				got.Priority != a.Priority || got.DedupeKey != a.DedupeKey || got.ReasonDetectionID != a.ReasonDetectionID ||
				!slices.Equal(got.Payload.JIDs, a.Payload.JIDs) {
				t.Fatalf("stored action = %+v, want a pending copy of %+v (the table is the source of truth, DESIGN §10.1)", got, a)
			}
			after := time.Now()
			if got.NotBefore.Before(before.Add(-time.Second)) || got.NotBefore.After(after.Add(time.Second)) {
				t.Errorf("NotBefore = %s, want about now (%s): an unset NotBefore means at once", got.NotBefore, before)
			}
			want := actions.DefaultDeadline(a, before)
			if d := got.Deadline.Sub(want); d < -time.Second || d > after.Sub(before)+time.Second {
				t.Errorf("Deadline = %s, want about %s (DefaultDeadline of the kind, DESIGN §10.4)", got.Deadline, want)
			}
		})
	}
}

func TestPGDedupeKeyUniqueForever(t *testing.T) {
	ctx := context.Background()
	pool := pgPools(t, 1)[0]
	s := actions.NewPGStore(pool, clock.Real{})

	// Sequential: a repeat is a no-op, also after the first one reached a final status.
	first := pgEnqueue(t, s, revoke(group(1), "dup", actions.P1, time.Minute))
	for _, step := range []string{"pending", "cancelled"} {
		if step == "cancelled" {
			if ok, err := s.Cancel(ctx, actions.RevokeKey(group(1), "dup"), "sender deleted it"); err != nil || !ok {
				t.Fatalf("Cancel = %v, %v; want true for a pending action", ok, err)
			}
		}
		res, err := s.Enqueue(ctx, revoke(group(1), "dup", actions.P0, time.Minute))
		if err != nil {
			t.Fatalf("repeat Enqueue (%s): %v, want nil", step, err)
		}
		if res.Inserted || res.ID != first {
			t.Fatalf("repeat Enqueue while the first is %s = %+v, want Inserted=false, ID=%d (a dedupe key is unique forever, DESIGN §10.1)", step, res, first)
		}
	}
	if got := pgStatus(t, s, first); got != actions.StatusCancelled {
		t.Fatalf("first action is %s after a repeat enqueue, want cancelled (a repeat must not revive or change it)", got)
	}

	// Two transactions racing on one key: the second waits for the first and then reports it.
	a := kick(group(2), "race", member(1))
	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	r1, err := s.EnqueueTx(ctx, tx1, a)
	if err != nil || !r1.Inserted {
		t.Fatalf("EnqueueTx in tx1 = %+v, %v; want Inserted", r1, err)
	}
	type out struct {
		res actions.EnqueueResult
		err error
	}
	second := make(chan out, 1)
	go func() {
		var o out
		o.err = store.InTx(ctx, pool, func(tx pgx.Tx) error {
			var err error
			o.res, err = s.EnqueueTx(ctx, tx, a)
			return err
		})
		second <- o
	}()
	time.Sleep(300 * time.Millisecond) // let tx2 reach the conflicting insert
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}
	var o out
	select {
	case o = <-second:
	case <-time.After(10 * time.Second):
		t.Fatalf("the second transaction's enqueue didn't finish within 10 s of the first committing, want it to resolve the conflict")
	}
	if o.err != nil {
		t.Fatalf("second transaction's enqueue of the same key: %v, want nil (a duplicate is a no-op, not an error, DESIGN §10.1)", o.err)
	}
	if o.res.Inserted || o.res.ID != r1.ID {
		t.Fatalf("second transaction's enqueue = %+v, want Inserted=false, ID=%d (the key is unique across transactions)", o.res, r1.ID)
	}
	if n := countRows(t, pool, "SELECT count(*) FROM app.actions WHERE dedupe_key = $1", a.DedupeKey); n != 1 {
		t.Fatalf("%d rows with the raced key, want 1 (a duplicate write could double a kick, DESIGN §10.1)", n)
	}
}
