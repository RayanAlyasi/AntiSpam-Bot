// Package cache is the in-process cache layer (DESIGN.md §11): a thin, typed wrapper over otter
// with a size bound, a TTL measured on the injected clock, explicit invalidation and
// single-flight loading. Anything expensive to recompute is also kept in Postgres by its owner;
// this layer only saves round trips.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maypok86/otter/v2"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Options configures a Cache.
type Options struct {
	// MaxSize bounds the number of entries. It must be positive.
	MaxSize int
	// TTL removes an entry this long after it was last written. Zero keeps entries until they
	// are evicted for size or invalidated.
	TTL time.Duration
	// Clock measures the TTL and entry ages. Nil means clock.Real.
	Clock clock.Clock
}

// Cache is a typed, bounded in-memory cache. It's safe for concurrent use.
type Cache[K comparable, V any] struct {
	c   *otter.Cache[K, entry[V]]
	clk clock.Clock
	ttl time.Duration
}

type entry[V any] struct {
	val      V
	storedAt time.Time
}

// New builds a cache.
func New[K comparable, V any](opts Options) (*Cache[K, V], error) {
	if opts.MaxSize <= 0 {
		return nil, errors.New("cache: MaxSize must be positive")
	}
	if opts.TTL < 0 {
		return nil, errors.New("cache: TTL must not be negative")
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	o := &otter.Options[K, entry[V]]{
		MaximumSize: opts.MaxSize,
		Clock:       otterClock{clk},
	}
	if opts.TTL > 0 {
		o.ExpiryCalculator = otter.ExpiryWriting[K, entry[V]](opts.TTL)
	}
	c, err := otter.New(o)
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	return &Cache[K, V]{c: c, clk: clk, ttl: opts.TTL}, nil
}

// Get returns the cached value for key, if present and not expired.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	e, ok := c.lookup(key)
	return e.val, ok
}

// GetWithAge returns the cached value and how long ago it was stored, so callers can force a
// refresh of entries older than they accept (for example the 10-minute admin re-check, §13.3).
func (c *Cache[K, V]) GetWithAge(key K) (V, time.Duration, bool) {
	e, ok := c.lookup(key)
	if !ok {
		return e.val, 0, false
	}
	return e.val, c.clk.Now().Sub(e.storedAt), true
}

// Set stores a value, replacing any previous one and restarting its TTL.
func (c *Cache[K, V]) Set(key K, val V) {
	c.c.Set(key, entry[V]{val: val, storedAt: c.clk.Now()})
}

// Update applies fn to the cached value atomically. fn receives the current value and whether it
// was present; when keep is false the entry is removed. Entries that are expired count as absent.
func (c *Cache[K, V]) Update(key K, fn func(old V, found bool) (val V, keep bool)) {
	now := c.clk.Now()
	c.c.Compute(key, func(old entry[V], found bool) (entry[V], otter.ComputeOp) {
		if found && c.expired(old, now) {
			var zero entry[V]
			old, found = zero, false
		}
		val, keep := fn(old.val, found)
		if !keep {
			return old, otter.InvalidateOp
		}
		storedAt := now
		if found {
			storedAt = old.storedAt // an in-place update doesn't make the data fresher
		}
		return entry[V]{val: val, storedAt: storedAt}, otter.WriteOp
	})
}

// Invalidate removes one entry.
func (c *Cache[K, V]) Invalidate(key K) { c.c.Invalidate(key) }

// InvalidateAll removes every entry.
func (c *Cache[K, V]) InvalidateAll() { c.c.InvalidateAll() }

// Len is the approximate number of entries.
func (c *Cache[K, V]) Len() int { return c.c.EstimatedSize() }

// Load returns the cached value, or calls load once (concurrent callers for the same key wait for
// the same call) and caches its result. Errors are returned and not cached.
func (c *Cache[K, V]) Load(ctx context.Context, key K, load func(ctx context.Context, key K) (V, error)) (V, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	e, err := c.c.Get(ctx, key, otter.LoaderFunc[K, entry[V]](func(ctx context.Context, key K) (entry[V], error) {
		v, err := load(ctx, key)
		if err != nil {
			return entry[V]{}, err
		}
		return entry[V]{val: v, storedAt: c.clk.Now()}, nil
	}))
	if err != nil {
		var zero V
		return zero, err
	}
	return e.val, nil
}

// Close stops the cache's background goroutines.
func (c *Cache[K, V]) Close() { c.c.StopAllGoroutines() }

func (c *Cache[K, V]) lookup(key K) (entry[V], bool) {
	e, ok := c.c.GetIfPresent(key)
	if !ok || c.expired(e, c.clk.Now()) {
		return entry[V]{}, false
	}
	return e, true
}

// expired double-checks the TTL on read, so an entry is never served past its TTL even between
// otter's maintenance passes.
func (c *Cache[K, V]) expired(e entry[V], now time.Time) bool {
	return c.ttl > 0 && now.Sub(e.storedAt) >= c.ttl
}

// otterClock runs otter's expiry on the injected clock. Its tick only paces otter's background
// clean-up pass, which then judges expiry by NowNano, so a real ticker is enough.
type otterClock struct{ clk clock.Clock }

func (o otterClock) NowNano() int64 { return o.clk.Now().UnixNano() }

func (o otterClock) Tick(d time.Duration) <-chan time.Time { return time.Tick(d) }
