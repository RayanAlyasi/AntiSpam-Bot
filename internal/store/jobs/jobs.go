package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/migrations"
)

// Defaults for Options.
const (
	DefaultMaxWorkers        = 4
	DefaultHeartbeatInterval = time.Minute
)

// Options configures a Client.
type Options struct {
	// Work makes the client fetch and run jobs (the workers role). Without it the client only
	// inserts, which is what the other roles need.
	Work bool
	// MaxWorkers caps concurrent jobs on the default queue; 0 means DefaultMaxWorkers.
	MaxWorkers int
	// Register adds the job workers and periodic jobs; it runs before the client is built.
	// The heartbeat job is always registered.
	Register func(*river.Workers, *[]*river.PeriodicJob) error
	// HeartbeatInterval is how often the heartbeat job runs; 0 means DefaultHeartbeatInterval.
	HeartbeatInterval time.Duration
	// OnHeartbeat, when set, is called with Clock's time each time the heartbeat job runs.
	OnHeartbeat func(time.Time)
	// Clock stamps heartbeats; nil means the real clock.
	Clock clock.Clock
	// Logger receives River's own logs (warnings and errors only) and the job logs.
	Logger zerolog.Logger
	// ErrorHandler, when set, is told about every job error and panic (river.ErrorHandler).
	ErrorHandler river.ErrorHandler
}

// Client is the River client for one process.
type Client struct {
	river *river.Client[pgx.Tx]
	work  bool
}

// New builds a client on pool. It doesn't start working until Start.
func New(pool *pgxpool.Pool, opts Options) (*Client, error) {
	workers := river.NewWorkers()
	var periodic []*river.PeriodicJob
	if opts.Register != nil {
		if err := opts.Register(workers, &periodic); err != nil {
			return nil, fmt.Errorf("register jobs: %w", err)
		}
	}
	interval := opts.HeartbeatInterval
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	if err := river.AddWorkerSafely(workers, &heartbeatWorker{log: opts.Logger, clock: clk, onRun: opts.OnHeartbeat}); err != nil {
		return nil, fmt.Errorf("register heartbeat: %w", err)
	}
	periodic = append(periodic, river.NewPeriodicJob(river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) { return HeartbeatArgs{}, nil },
		&river.PeriodicJobOpts{ID: HeartbeatArgs{}.Kind(), RunOnStart: true}))

	cfg := &river.Config{
		Schema:  migrations.RiverSchema,
		Workers: workers,
		Logger:  slog.New(zerolog.NewSlogHandler(opts.Logger.Level(max(opts.Logger.GetLevel(), zerolog.WarnLevel)))),
	}
	if opts.ErrorHandler != nil {
		cfg.ErrorHandler = opts.ErrorHandler
	}
	if opts.Work {
		n := opts.MaxWorkers
		if n <= 0 {
			n = DefaultMaxWorkers
		}
		cfg.Queues = map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: n}}
		cfg.PeriodicJobs = periodic
	}
	rc, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Client{river: rc, work: opts.Work}, nil
}

// ErrInsertOnly is returned by Start on a client built without Options.Work.
var ErrInsertOnly = errors.New("jobs: client is insert-only (Options.Work is false)")

// Start begins fetching and running jobs in the background. End it with Stop; pass a context
// that outlives the shutdown (context.WithoutCancel), since cancelling it is a hard stop that
// skips the graceful drain.
func (c *Client) Start(ctx context.Context) error {
	if !c.work {
		return ErrInsertOnly
	}
	if err := c.river.Start(ctx); err != nil {
		return fmt.Errorf("start river: %w", err)
	}
	return nil
}

// Stop stops fetching new jobs and waits for running ones, until ctx is done.
func (c *Client) Stop(ctx context.Context) error {
	if !c.work {
		return nil
	}
	if err := c.river.Stop(ctx); err != nil {
		return fmt.Errorf("stop river: %w", err)
	}
	return nil
}

// InsertTx enqueues a job in tx, so it commits or rolls back with the change that needs it.
func (c *Client) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	res, err := c.river.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return nil, fmt.Errorf("enqueue %s: %w", args.Kind(), err)
	}
	return res, nil
}

// HeartbeatArgs is the periodic heartbeat job. It proves the periodic scheduler and the workers
// run, and its last run time is a cheap liveness signal for the workers role.
type HeartbeatArgs struct{}

// Kind implements river.JobArgs.
func (HeartbeatArgs) Kind() string { return "heartbeat" }

type heartbeatWorker struct {
	river.WorkerDefaults[HeartbeatArgs]
	log   zerolog.Logger
	clock clock.Clock
	onRun func(time.Time)
}

func (w *heartbeatWorker) Work(_ context.Context, job *river.Job[HeartbeatArgs]) error {
	now := w.clock.Now().UTC()
	w.log.Debug().Int64("job_id", job.ID).Msg("heartbeat job ran")
	if w.onRun != nil {
		w.onRun(now)
	}
	return nil
}
