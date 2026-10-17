package aistore

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// PurgeInterval is how often expired ai_cache rows are deleted.
const PurgeInterval = 6 * time.Hour

// PurgeCacheArgs is the periodic job that deletes ai_cache rows older than 30 days (§11).
type PurgeCacheArgs struct{}

// Kind implements river.JobArgs.
func (PurgeCacheArgs) Kind() string { return "ai_cache_purge" }

type purgeWorker struct {
	river.WorkerDefaults[PurgeCacheArgs]
	cache *Cache
	clock clock.Clock
}

func (w *purgeWorker) Work(ctx context.Context, _ *river.Job[PurgeCacheArgs]) error {
	if _, err := w.cache.Purge(ctx, w.clock.Now().Add(-cache.DefaultTTL)); err != nil {
		return err
	}
	return nil
}

// RegisterPurge adds the purge worker and its periodic job; pass it from jobs.Options.Register.
func RegisterPurge(workers *river.Workers, periodic *[]*river.PeriodicJob, c *Cache, clk clock.Clock) error {
	if clk == nil {
		clk = clock.Real{}
	}
	if err := river.AddWorkerSafely(workers, &purgeWorker{cache: c, clock: clk}); err != nil {
		return fmt.Errorf("register ai cache purge: %w", err)
	}
	*periodic = append(*periodic, river.NewPeriodicJob(river.PeriodicInterval(PurgeInterval),
		func() (river.JobArgs, *river.InsertOpts) { return PurgeCacheArgs{}, nil },
		&river.PeriodicJobOpts{ID: PurgeCacheArgs{}.Kind(), RunOnStart: true}))
	return nil
}
