// Package aistore keeps the AI tier's state in Postgres (DESIGN.md §14): ai_usage behind
// budget.Store and ai_cache behind cache.Store. The budget and cache packages stay free of pgx;
// this package adapts the sqlc queries to their interfaces.
package aistore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
)

func date(t time.Time) pgtype.Date {
	return pgtype.Date{Time: budget.Day(t), Valid: true}
}

// Budget implements budget.Store on app.ai_usage.
type Budget struct {
	pool *pgxpool.Pool
}

var _ budget.Store = (*Budget)(nil)

// NewBudget returns the Postgres budget store.
func NewBudget(pool *pgxpool.Pool) *Budget { return &Budget{pool: pool} }

// Reserve implements budget.Store. An instance-wide advisory lock, taken before reading the
// counts, serializes reservations, so no limit is ever passed and each count is unique (the 80%
// alert relies on that).
func (b *Budget) Reserve(ctx context.Context, day time.Time, group string, lim budget.Limits) (budget.Counts, bool, error) {
	var c budget.Counts
	var ok bool
	err := store.InTx(ctx, b.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.AIBudgetLock(ctx); err != nil {
			return fmt.Errorf("lock ai budget: %w", err)
		}
		row, err := q.AIUsageCounts(ctx, db.AIUsageCountsParams{Day: date(day), GroupJid: group})
		if err != nil {
			return fmt.Errorf("count ai usage: %w", err)
		}
		c = budget.Counts{GroupDay: int(row.GroupDay), InstanceDay: int(row.InstanceDay), InstanceMonth: int(row.InstanceMonth)}
		over := func(used, limit int) bool { return limit > 0 && used+1 > limit }
		if over(c.GroupDay, lim.GroupPerDay) || over(c.InstanceDay, lim.InstancePerDay) || over(c.InstanceMonth, lim.InstancePerMonth) {
			return nil // denied: nothing counted
		}
		if err := q.AIUsageAddCall(ctx, db.AIUsageAddCallParams{Day: date(day), GroupJid: group}); err != nil {
			return fmt.Errorf("count ai call: %w", err)
		}
		c.GroupDay++
		c.InstanceDay++
		c.InstanceMonth++
		ok = true
		return nil
	})
	if err != nil {
		return budget.Counts{}, false, err
	}
	return c, ok, nil
}

// AddUsage implements budget.Store.
func (b *Budget) AddUsage(ctx context.Context, day time.Time, group string, u budget.Usage) error {
	err := db.New(b.pool).AIUsageAddTokens(ctx, db.AIUsageAddTokensParams{
		Day: date(day), GroupJid: group,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CostMicros: u.CostMicros,
	})
	if err != nil {
		return fmt.Errorf("add ai usage: %w", err)
	}
	return nil
}

// Row returns the ai_usage row for (day, group); zero when there is none.
func (b *Budget) Row(ctx context.Context, day time.Time, group string) (budget.Row, error) {
	r, err := db.New(b.pool).AIUsageRow(ctx, db.AIUsageRowParams{Day: date(day), GroupJid: group})
	if errors.Is(err, pgx.ErrNoRows) {
		return budget.Row{}, nil
	}
	if err != nil {
		return budget.Row{}, fmt.Errorf("read ai usage: %w", err)
	}
	return budget.Row{Calls: int(r.Calls), InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CostMicros: r.CostMicros}, nil
}

// Cache implements cache.Store on app.ai_cache.
type Cache struct {
	pool *pgxpool.Pool
}

var _ cache.Store = (*Cache)(nil)

// NewCache returns the Postgres result-cache store.
func NewCache(pool *pgxpool.Pool) *Cache { return &Cache{pool: pool} }

// Get implements cache.Store.
func (c *Cache) Get(ctx context.Context, key string, notBefore time.Time) (cache.Entry, bool, error) {
	r, err := db.New(c.pool).AICacheGet(ctx, db.AICacheGetParams{Key: key, NotBefore: notBefore})
	if errors.Is(err, pgx.ErrNoRows) {
		return cache.Entry{}, false, nil
	}
	if err != nil {
		return cache.Entry{}, false, fmt.Errorf("read ai cache: %w", err)
	}
	return cache.Entry{Key: r.Key, Model: r.Model, PromptVersion: r.PromptVersion, Result: r.Result, CreatedAt: r.CreatedAt}, true, nil
}

// Put implements cache.Store.
func (c *Cache) Put(ctx context.Context, e cache.Entry) error {
	err := db.New(c.pool).AICachePut(ctx, db.AICachePutParams{
		Key: e.Key, Model: e.Model, PromptVersion: e.PromptVersion, Result: e.Result, CreatedAt: e.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("write ai cache: %w", err)
	}
	return nil
}

// Purge deletes entries created before before (now minus cache.DefaultTTL in production) and
// returns how many it removed. Run it periodically; lookups already ignore expired entries.
func (c *Cache) Purge(ctx context.Context, before time.Time) (int64, error) {
	n, err := db.New(c.pool).AICachePurge(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("purge ai cache: %w", err)
	}
	return n, nil
}
