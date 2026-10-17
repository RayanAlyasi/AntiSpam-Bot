// Package reviewjobs runs the review package's periodic work on River: applying review timeouts
// (DESIGN.md §9.5) and purging evidence snapshots past their 30-day retention (§14).
package reviewjobs

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

// Intervals. Timeouts are checked every minute so a 15-minute ask-before-action timeout fires
// within a minute of its deadline.
const (
	ExpireInterval = time.Minute
	PurgeInterval  = time.Hour
)

// ExpireArgs is the periodic job that applies review timeouts.
type ExpireArgs struct{}

// Kind implements river.JobArgs.
func (ExpireArgs) Kind() string { return "review_expire" }

// PurgeArgs is the periodic job that deletes expired evidence snapshots.
type PurgeArgs struct{}

// Kind implements river.JobArgs.
func (PurgeArgs) Kind() string { return "evidence_purge" }

type expireWorker struct {
	river.WorkerDefaults[ExpireArgs]
	svc *review.Service
}

func (w *expireWorker) Work(ctx context.Context, _ *river.Job[ExpireArgs]) error {
	if _, err := w.svc.Expire(ctx); err != nil {
		return fmt.Errorf("expire reviews: %w", err)
	}
	return nil
}

type purgeWorker struct {
	river.WorkerDefaults[PurgeArgs]
	evidence review.EvidenceStore
	clock    clock.Clock
}

func (w *purgeWorker) Work(ctx context.Context, _ *river.Job[PurgeArgs]) error {
	if _, err := w.evidence.Purge(ctx, w.clock.Now()); err != nil {
		return fmt.Errorf("purge evidence: %w", err)
	}
	return nil
}

// Register returns a jobs.Options.Register function that adds both periodic jobs. A nil
// evidence store skips the purge job.
func Register(svc *review.Service, evidence review.EvidenceStore, c clock.Clock) func(*river.Workers, *[]*river.PeriodicJob) error {
	if c == nil {
		c = clock.Real{}
	}
	return func(workers *river.Workers, periodic *[]*river.PeriodicJob) error {
		if err := river.AddWorkerSafely(workers, &expireWorker{svc: svc}); err != nil {
			return fmt.Errorf("register review expire: %w", err)
		}
		*periodic = append(*periodic, river.NewPeriodicJob(river.PeriodicInterval(ExpireInterval),
			// No retries: the next run a minute later is the retry, so failures don't pile up.
			func() (river.JobArgs, *river.InsertOpts) { return ExpireArgs{}, &river.InsertOpts{MaxAttempts: 1} },
			&river.PeriodicJobOpts{ID: ExpireArgs{}.Kind(), RunOnStart: true}))
		if evidence == nil {
			return nil
		}
		if err := river.AddWorkerSafely(workers, &purgeWorker{evidence: evidence, clock: c}); err != nil {
			return fmt.Errorf("register evidence purge: %w", err)
		}
		*periodic = append(*periodic, river.NewPeriodicJob(river.PeriodicInterval(PurgeInterval),
			func() (river.JobArgs, *river.InsertOpts) { return PurgeArgs{}, &river.InsertOpts{MaxAttempts: 1} },
			&river.PeriodicJobOpts{ID: PurgeArgs{}.Kind(), RunOnStart: true}))
		return nil
	}
}
