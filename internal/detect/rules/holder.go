package rules

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Loader reads every enabled rule, every fingerprint and every active blacklist entry. The
// Postgres implementation lives in internal/store.
type Loader interface {
	LoadRules(ctx context.Context) ([]Rule, error)
	LoadFingerprints(ctx context.Context) ([]Fingerprint, error)
	LoadBlacklist(ctx context.Context) ([]BlacklistEntry, error)
}

// Holder keeps the current Snapshot. Readers call Current on every message; Reload builds a
// new snapshot from the Loader and swaps it in atomically, so a reader sees either the old
// or the new rules, never a mix.
type Holder struct {
	loader Loader
	clock  clock.Clock
	log    zerolog.Logger
	cur    atomic.Pointer[Snapshot]
}

// NewHolder returns a holder serving the Empty snapshot until the first Reload.
func NewHolder(loader Loader, clk clock.Clock, log zerolog.Logger) *Holder {
	h := &Holder{loader: loader, clock: clk, log: log}
	h.cur.Store(Empty())
	return h
}

// Current returns the snapshot in use. It is never nil.
func (h *Holder) Current() *Snapshot { return h.cur.Load() }

// Reload loads every entry and swaps in the new snapshot. Invalid entries are logged and
// skipped. On a load error the current snapshot stays in place and the error is returned.
func (h *Holder) Reload(ctx context.Context) error {
	rs, err := h.loader.LoadRules(ctx)
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	fps, err := h.loader.LoadFingerprints(ctx)
	if err != nil {
		return fmt.Errorf("load fingerprints: %w", err)
	}
	bl, err := h.loader.LoadBlacklist(ctx)
	if err != nil {
		return fmt.Errorf("load blacklist: %w", err)
	}
	s, errs := Compile(rs, fps, bl)
	for _, e := range errs {
		h.log.Warn().Err(e).Msg("skipping invalid rules entry")
	}
	h.cur.Store(s)
	nr, nf, nb := s.Counts()
	h.log.Debug().Int("rules", nr).Int("fingerprints", nf).Int("blacklist", nb).Msg("rules reloaded")
	return nil
}

// ResyncInterval is how often Run reloads even without a change notification, as a backstop
// for notifications lost while the listener was reconnecting.
const ResyncInterval = 5 * time.Minute

// retryDelay is the wait before retrying a failed reload.
const retryDelay = 2 * time.Second

// Run loads the rules, then reloads on every value from changes (the LISTEN/NOTIFY
// listener sends one per committed change, and one after it reconnects) and every
// ResyncInterval, until ctx ends. A burst of notifications costs one reload: pending
// notifications are drained before reloading. A failed reload is retried after a short
// delay; the old snapshot keeps serving meanwhile.
func (h *Holder) Run(ctx context.Context, changes <-chan struct{}) error {
	pending := true
	for {
		if pending {
			drain(changes)
			if err := h.Reload(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				h.log.Error().Err(err).Msg("rules reload failed; keeping the previous rules")
				if err := h.clock.Sleep(ctx, retryDelay); err != nil {
					return err
				}
				continue
			}
			pending = false
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-changes:
			if !ok {
				return errors.New("rules change notifications closed")
			}
			pending = true
		case <-h.clock.After(ResyncInterval):
			pending = true
		}
	}
}

func drain(ch <-chan struct{}) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// StaticLoader is a Loader over fixed slices, for tests and for running without a database.
type StaticLoader struct {
	Rules        []Rule
	Fingerprints []Fingerprint
	Blacklist    []BlacklistEntry
}

// LoadRules implements Loader.
func (s StaticLoader) LoadRules(context.Context) ([]Rule, error) { return s.Rules, nil }

// LoadFingerprints implements Loader.
func (s StaticLoader) LoadFingerprints(context.Context) ([]Fingerprint, error) {
	return s.Fingerprints, nil
}

// LoadBlacklist implements Loader.
func (s StaticLoader) LoadBlacklist(context.Context) ([]BlacklistEntry, error) {
	return s.Blacklist, nil
}
