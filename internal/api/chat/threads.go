package chat

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// ExceptionInterval is how often a person without an open thread may get a command reply: the
// one exception to inbound-first (§13.7).
const ExceptionInterval = 24 * time.Hour

// ThreadStore records inbound DMs and the command-reply exception per person.
type ThreadStore interface {
	MarkInbound(ctx context.Context, person wa.JID, at time.Time) error
	// LastInbound returns when the person last messaged the bot; ok is false if they never did.
	LastInbound(ctx context.Context, person wa.JID) (at time.Time, ok bool, err error)
	MarkException(ctx context.Context, person wa.JID, at time.Time) error
	LastException(ctx context.Context, person wa.JID) (at time.Time, ok bool, err error)
}

// Purpose is why the bot wants to DM someone.
type Purpose int

// DM purposes.
const (
	PurposeNotice       Purpose = iota + 1 // onboarding, alerts, review items: needs an open thread
	PurposeCommandReply                    // a reply to a command the person just sent
)

// Gate decides whether the bot may DM a person (§3, §13.6): only into a thread the person
// opened, except a reply to a command they just sent, at most once per ExceptionInterval until
// they write to the bot.
type Gate struct {
	store ThreadStore
	clock clock.Clock
	mu    sync.Mutex // serializes the exception check and its record
}

// NewGate returns a Gate.
func NewGate(store ThreadStore, c clock.Clock) *Gate {
	if c == nil {
		c = clock.Real{}
	}
	return &Gate{store: store, clock: c}
}

// Inbound records an inbound DM, which opens (or keeps open) the thread.
func (g *Gate) Inbound(ctx context.Context, person wa.JID) error {
	if err := g.store.MarkInbound(ctx, person, g.clock.Now()); err != nil {
		return fmt.Errorf("record inbound DM: %w", err)
	}
	return nil
}

// Open reports whether the person has an open thread with the bot.
func (g *Gate) Open(ctx context.Context, person wa.JID) (bool, error) {
	_, ok, err := g.store.LastInbound(ctx, person)
	if err != nil {
		return false, fmt.Errorf("thread lookup: %w", err)
	}
	return ok, nil
}

// Allow reports whether the bot may DM the person now for purpose p, and records the use of
// the command-reply exception when it applies.
func (g *Gate) Allow(ctx context.Context, person wa.JID, p Purpose) (bool, error) {
	open, err := g.Open(ctx, person)
	if err != nil || open {
		return open, err
	}
	if p != PurposeCommandReply {
		return false, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	last, used, err := g.store.LastException(ctx, person)
	if err != nil {
		return false, fmt.Errorf("exception lookup: %w", err)
	}
	if used && now.Sub(last) < ExceptionInterval {
		return false, nil
	}
	if err := g.store.MarkException(ctx, person, now); err != nil {
		return false, fmt.Errorf("record exception: %w", err)
	}
	return true, nil
}

// MemoryThreads is an in-memory ThreadStore, safe for concurrent use.
type MemoryThreads struct {
	mu        sync.Mutex
	inbound   map[wa.JID]time.Time
	exception map[wa.JID]time.Time
}

var _ ThreadStore = (*MemoryThreads)(nil)

// NewMemoryThreads returns an empty MemoryThreads.
func NewMemoryThreads() *MemoryThreads {
	return &MemoryThreads{inbound: map[wa.JID]time.Time{}, exception: map[wa.JID]time.Time{}}
}

// MarkInbound records an inbound DM.
func (m *MemoryThreads) MarkInbound(_ context.Context, person wa.JID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inbound[person] = at
	return nil
}

// LastInbound returns the last inbound DM time.
func (m *MemoryThreads) LastInbound(_ context.Context, person wa.JID) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.inbound[person]
	return t, ok, nil
}

// MarkException records a command reply sent under the exception.
func (m *MemoryThreads) MarkException(_ context.Context, person wa.JID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exception[person] = at
	return nil
}

// LastException returns when the exception was last used.
func (m *MemoryThreads) LastException(_ context.Context, person wa.JID) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.exception[person]
	return t, ok, nil
}
