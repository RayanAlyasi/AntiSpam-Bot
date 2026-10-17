// Package cache keeps AI verdicts so identical and near-identical spam is classified once
// (DESIGN.md §7.4 cost controls 2 and 3, §11): a 30-day result cache keyed on
// sha256(normalized text + context hash + model + prompt version), and in-memory reuse of the
// first verdict for every copy in the same SimHash cluster or campaign, so a campaign across 40
// groups costs one call. Only definitive verdicts are kept; unsure answers and failures are
// retried next time. The cache is generic over the verdict type so it does not depend on how the
// classifier represents one; persistence sits behind Store (ai_cache), never pgx.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Defaults for Options.
const (
	DefaultTTL         = 30 * 24 * time.Hour // ai_cache retention (§11)
	DefaultClusterTTL  = 24 * time.Hour      // how long a cluster or campaign reuses its first verdict
	DefaultMaxClusters = 50_000              // bound on in-memory cluster entries
	// DefaultFlightTimeout bounds a shared call (store lookups, budget, provider calls) once
	// the caller that started it has gone, so a hung store can't block a cluster forever.
	DefaultFlightTimeout = 5 * time.Minute
)

// Key is the result-cache key: sha256 over the normalized text, the context hash, the model and
// the prompt version. Each field is length-prefixed, so different splits never collide, and a
// new model or prompt version always misses.
func Key(normText, contextHash, model, promptVersion string) string {
	h := sha256.New()
	for _, f := range []string{normText, contextHash, model, promptVersion} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		h.Write(n[:])
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Entry is one ai_cache row.
type Entry struct {
	Key           string
	Model         string
	PromptVersion string
	Result        json.RawMessage
	CreatedAt     time.Time
}

// Store persists ai_cache(key PK, model, prompt_version, result jsonb, created_at).
type Store interface {
	// Get returns the entry for key if it was created at or after notBefore.
	Get(ctx context.Context, key string, notBefore time.Time) (Entry, bool, error)
	// Put inserts or replaces the entry.
	Put(ctx context.Context, e Entry) error
}

// Source says where a verdict came from, for explanations and metrics.
type Source int

const (
	SourceFresh   Source = iota // a provider call (or a failure) produced it now
	SourceCached                // the 30-day result cache
	SourceCluster               // another copy in the same cluster or campaign
)

func (s Source) String() string {
	switch s {
	case SourceCached:
		return "cache"
	case SourceCluster:
		return "cluster"
	default:
		return "fresh"
	}
}

// Options configure a Cache.
type Options[V any] struct {
	// Definitive reports whether a verdict may be cached and reused (spam or not_spam). Required.
	Definitive func(V) bool
	// Sanitize prepares a verdict for storage and reuse, e.g. capping and redacting the reason so
	// no message text is kept (§17). It also runs on the value returned with a failure, which
	// may be the zero value. Optional.
	Sanitize      func(V) V
	TTL           time.Duration // 0 means DefaultTTL
	ClusterTTL    time.Duration // 0 means DefaultClusterTTL
	MaxClusters   int           // 0 means DefaultMaxClusters
	FlightTimeout time.Duration // 0 means DefaultFlightTimeout
}

// Request identifies one classification.
type Request struct {
	Key           string // from Key
	Cluster       string // SimHash cluster or campaign ID; empty when the message has none
	Model         string
	PromptVersion string
}

// Cache is safe for concurrent use.
type Cache[V any] struct {
	store  Store
	clock  clock.Clock
	opts   Options[V]
	flight singleflight.Group

	mu       sync.Mutex
	clusters map[string]clusterEntry[V]
	flights  map[string]*flightState // the context of each shared call, by flight key
}

// flightState is the context of one shared call and how many callers are waiting on it. The
// call runs until it finishes, FlightTimeout passes, or the last waiting caller gives up.
type flightState struct {
	ctx     context.Context
	cancel  context.CancelFunc
	waiters int
}

type clusterEntry[V any] struct {
	v       V
	expires time.Time
}

type result[V any] struct {
	v      V // what the leader gets
	shared V // what copies waiting on the same flight get: sanitized, so no other group's data leaks
	src    Source
}

// New builds a Cache.
func New[V any](store Store, clk clock.Clock, opts Options[V]) (*Cache[V], error) {
	if store == nil || clk == nil || opts.Definitive == nil {
		return nil, errors.New("cache: store, clock and Definitive are required")
	}
	if opts.TTL < 0 || opts.ClusterTTL < 0 || opts.MaxClusters < 0 || opts.FlightTimeout < 0 {
		return nil, errors.New("cache: durations and sizes must not be negative")
	}
	if opts.TTL == 0 {
		opts.TTL = DefaultTTL
	}
	if opts.ClusterTTL == 0 {
		opts.ClusterTTL = DefaultClusterTTL
	}
	if opts.MaxClusters == 0 {
		opts.MaxClusters = DefaultMaxClusters
	}
	if opts.FlightTimeout == 0 {
		opts.FlightTimeout = DefaultFlightTimeout
	}
	return &Cache[V]{store: store, clock: clk, opts: opts, clusters: map[string]clusterEntry[V]{}, flights: map[string]*flightState{}}, nil
}

// Do returns the cluster's verdict, else the cached one, else calls classify. Concurrent calls
// for the same cluster (or the same key, without a cluster) share one classify call. classify
// owns the budget check: cache and cluster hits never reach it, so they cost no budget.
// A store read error is not fatal: the call goes ahead uncached and the error is returned
// alongside the verdict for logging. A failed classify returns its error and caches nothing.
// Only the caller whose classify ran gets its verdict unsanitized; every other caller gets the
// sanitized copy. The shared call does not stop when the caller that started it gives up, so
// copies still waiting get an answer; it stops when no caller is waiting any more, and at
// Options.FlightTimeout at the latest.
func (c *Cache[V]) Do(ctx context.Context, req Request, classify func(context.Context) (V, error)) (V, Source, error) {
	if req.Key == "" {
		var zero V
		return zero, SourceFresh, errors.New("cache: empty key")
	}
	if v, ok := c.cluster(req.Cluster); ok {
		return v, SourceCluster, nil
	}
	flightKey := "k:" + req.Key
	if req.Cluster != "" {
		flightKey = "c:" + req.Cluster
	}
	fs := c.join(ctx, flightKey)
	defer c.leave(flightKey, fs)
	var storeErr error
	led := false // only the caller whose function runs made the provider call
	ch := c.flight.DoChan(flightKey, func() (any, error) {
		led = true
		flightCtx := fs.ctx
		defer c.finish(flightKey, fs)
		// Re-check: a flight that just finished may have filled the cluster.
		if v, ok := c.cluster(req.Cluster); ok {
			return result[V]{v, v, SourceCluster}, nil
		}
		if v, ok, err := c.lookup(flightCtx, req.Key); err != nil {
			storeErr = err
		} else if ok {
			c.remember(req.Cluster, v)
			return result[V]{v, v, SourceCached}, nil
		}
		v, err := classify(flightCtx)
		stored := c.sanitize(v)
		if err != nil {
			return result[V]{v, stored, SourceFresh}, err
		}
		if c.opts.Definitive(v) {
			c.remember(req.Cluster, stored)
			if perr := c.put(flightCtx, req, stored); perr != nil {
				storeErr = errors.Join(storeErr, perr)
			}
		}
		return result[V]{v, stored, SourceFresh}, nil
	})
	select {
	case <-ctx.Done():
		var zero V
		return zero, SourceFresh, ctx.Err()
	case r := <-ch:
		res, _ := r.Val.(result[V])
		v, src := res.v, res.src
		if !led {
			v = res.shared
			if src == SourceFresh {
				src = SourceCluster // another caller's provider call answered this one
			}
		}
		if r.Err != nil {
			return v, SourceFresh, r.Err
		}
		return v, src, storeErr
	}
}

// join registers the caller as waiting on the flight for key, creating its context if needed.
func (c *Cache[V]) join(ctx context.Context, key string) *flightState {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs := c.flights[key]
	if fs == nil {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.opts.FlightTimeout)
		fs = &flightState{ctx: fctx, cancel: cancel}
		c.flights[key] = fs
	}
	fs.waiters++
	return fs
}

// leave unregisters a caller; the last one out cancels the call, since nobody needs its answer.
func (c *Cache[V]) leave(key string, fs *flightState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs.waiters--
	if fs.waiters == 0 {
		fs.cancel()
		if c.flights[key] == fs {
			delete(c.flights, key)
			// A copy arriving before the cancelled call returns must start a new call, not
			// join one that can only fail.
			c.flight.Forget(key)
		}
	}
}

// finish forgets a flight's context once its call has returned, so the next call gets a new one.
func (c *Cache[V]) finish(key string, fs *flightState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[key] == fs {
		delete(c.flights, key)
	}
}

func (c *Cache[V]) sanitize(v V) V {
	if c.opts.Sanitize != nil {
		return c.opts.Sanitize(v)
	}
	return v
}

func (c *Cache[V]) lookup(ctx context.Context, key string) (V, bool, error) {
	var v V
	e, ok, err := c.store.Get(ctx, key, c.clock.Now().Add(-c.opts.TTL))
	if err != nil {
		return v, false, fmt.Errorf("ai cache get: %w", err)
	}
	if !ok {
		return v, false, nil
	}
	if err := json.Unmarshal(e.Result, &v); err != nil {
		return v, false, fmt.Errorf("ai cache decode %s: %w", key, err)
	}
	if !c.opts.Definitive(v) { // never trust a stored non-definitive verdict
		return v, false, nil
	}
	return v, true, nil
}

func (c *Cache[V]) put(ctx context.Context, req Request, v V) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("ai cache encode: %w", err)
	}
	e := Entry{Key: req.Key, Model: req.Model, PromptVersion: req.PromptVersion, Result: raw, CreatedAt: c.clock.Now()}
	if err := c.store.Put(ctx, e); err != nil {
		return fmt.Errorf("ai cache put: %w", err)
	}
	return nil
}

func (c *Cache[V]) cluster(id string) (V, bool) {
	var zero V
	if id == "" {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.clusters[id]
	if !ok {
		return zero, false
	}
	if !c.clock.Now().Before(e.expires) {
		delete(c.clusters, id)
		return zero, false
	}
	return e.v, true
}

func (c *Cache[V]) remember(id string, v V) {
	if id == "" {
		return
	}
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.clusters[id]; !ok && len(c.clusters) >= c.opts.MaxClusters {
		c.evict(now)
	}
	c.clusters[id] = clusterEntry[V]{v: v, expires: now.Add(c.opts.ClusterTTL)}
}

// evict drops expired entries, and if none were expired the one closest to expiry.
func (c *Cache[V]) evict(now time.Time) {
	var oldest string
	var oldestAt time.Time
	removed := false
	for id, e := range c.clusters {
		if !now.Before(e.expires) {
			delete(c.clusters, id)
			removed = true
			continue
		}
		if oldest == "" || e.expires.Before(oldestAt) {
			oldest, oldestAt = id, e.expires
		}
	}
	if !removed && oldest != "" {
		delete(c.clusters, oldest)
	}
}
