//go:build integration

package jobs_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/jobs"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

type echoArgs struct{ N int }

func (echoArgs) Kind() string { return "test_echo" }

type echoWorker struct {
	river.WorkerDefaults[echoArgs]
	got chan int
}

func (w *echoWorker) Work(_ context.Context, job *river.Job[echoArgs]) error {
	w.got <- job.Args.N
	return nil
}

func TestInsertTxRunsOnlyCommittedJobs(t *testing.T) {
	pool := storetest.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	w := &echoWorker{got: make(chan int, 4)}
	c, err := jobs.New(pool, jobs.Options{
		Work:   true,
		Logger: zerolog.Nop(),
		Register: func(ws *river.Workers, _ *[]*river.PeriodicJob) error {
			return river.AddWorkerSafely(ws, w)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	errAbort := errors.New("abort")
	if err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := c.InsertTx(ctx, tx, echoArgs{N: 1}, nil)
		if err != nil {
			return err
		}
		return errAbort
	}); !errors.Is(err, errAbort) {
		t.Fatal(err)
	}
	if err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := c.InsertTx(ctx, tx, echoArgs{N: 2}, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case n := <-w.got:
		if n != 2 {
			t.Fatalf("worked job %d first, want 2 (a rolled-back transaction must not leave its job)", n)
		}
	case <-ctx.Done():
		t.Fatal("committed job never ran")
	}
	select {
	case n := <-w.got:
		t.Fatalf("job %d ran, but its transaction rolled back", n)
	case <-time.After(2 * time.Second):
	}
}

func TestPeriodicHeartbeatRunsOnSchedule(t *testing.T) {
	pool := storetest.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var runs atomic.Int32
	done := make(chan struct{})
	c, err := jobs.New(pool, jobs.Options{
		Work:              true,
		Logger:            zerolog.Nop(),
		HeartbeatInterval: 500 * time.Millisecond,
		OnHeartbeat: func(time.Time) {
			if runs.Add(1) == 3 {
				close(done)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("heartbeat ran %d times in a minute, want 3 at a 500ms interval", runs.Load())
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := c.Stop(stopCtx); err != nil {
		t.Errorf("Stop: %v (shutdown must finish in bounded time)", err)
	}
}

func TestInsertOnlyClientDoesNotWork(t *testing.T) {
	pool := storetest.Pool(t)
	c, err := jobs.New(pool, jobs.Options{Logger: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); !errors.Is(err, jobs.ErrInsertOnly) {
		t.Errorf("Start on insert-only client: %v, want ErrInsertOnly (only the workers role runs jobs)", err)
	}
	ctx := context.Background()
	if err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := c.InsertTx(ctx, tx, jobs.HeartbeatArgs{}, nil)
		return err
	}); err != nil {
		t.Errorf("insert-only client can't enqueue: %v", err)
	}
}
