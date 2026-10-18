package actions

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// MemStore is an in-memory Store, for tests and for running the queue without Postgres. It is
// safe for concurrent use. Inside a testing/synctest bubble it runs on the bubble's time.
type MemStore struct {
	clock clock.Clock

	mu       sync.Mutex
	nextID   int64
	actions  map[int64]*Action
	byKey    map[string]int64
	locked   map[wa.JID]bool
	controls map[string]Control
	called   map[int64]bool // actions that reached WhatsApp at least once
}

var _ Store = (*MemStore)(nil)

// NewMemStore returns an empty store that stamps actions with c (clock.Real when nil).
func NewMemStore(c clock.Clock) *MemStore {
	if c == nil {
		c = clock.Real{}
	}
	return &MemStore{
		clock:    c,
		actions:  map[int64]*Action{},
		byKey:    map[string]int64{},
		locked:   map[wa.JID]bool{},
		controls: map[string]Control{},
		called:   map[int64]bool{},
	}
}

// All returns a copy of every stored action, ordered by ID. Tests use it to inspect outcomes.
func (s *MemStore) All() []Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Action, 0, len(s.actions))
	for _, a := range s.actions {
		out = append(out, clone(*a))
	}
	slices.SortFunc(out, func(a, b Action) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Enqueue implements Store.
func (s *MemStore) Enqueue(_ context.Context, a Action) (EnqueueResult, error) {
	now := s.clock.Now()
	a, dropped, err := prepare(a, now)
	if err != nil {
		return EnqueueResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if dropped != "" {
		if id, ok := s.byKey[a.DedupeKey]; ok {
			return EnqueueResult{ID: id}, nil
		}
		return EnqueueResult{Dropped: dropped}, nil
	}
	if id, ok := s.byKey[a.DedupeKey]; ok {
		return EnqueueResult{ID: id}, nil
	}
	s.nextID++
	a.ID = s.nextID
	s.actions[a.ID] = &a
	s.byKey[a.DedupeKey] = a.ID
	return EnqueueResult{ID: a.ID, Inserted: true}, nil
}

// Get implements Store.
func (s *MemStore) Get(_ context.Context, id int64) (Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.actions[id]
	if !ok {
		return Action{}, fmt.Errorf("action %d: %w", id, ErrNotFound)
	}
	return clone(*a), nil
}

// Cancel implements Store.
func (s *MemStore) Cancel(_ context.Context, dedupeKey, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byKey[dedupeKey]
	if !ok {
		return false, nil
	}
	a := s.actions[id]
	if a.Status != StatusPending {
		return false, nil
	}
	s.finish(a, StatusCancelled, reason)
	return true, nil
}

// Lock implements Store.
func (s *MemStore) Lock(_ context.Context, bot wa.JID) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked[bot] {
		return nil, fmt.Errorf("bot %s: %w", maskJID(bot), ErrExecutorRunning)
	}
	s.locked[bot] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.locked, bot)
		})
	}, nil
}

// HoldsLock implements Store.
func (s *MemStore) HoldsLock(_ context.Context, bot wa.JID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.locked[bot] {
		return fmt.Errorf("bot %s: %w", maskJID(bot), ErrLockLost)
	}
	return nil
}

// Recover implements Store.
func (s *MemStore) Recover(_ context.Context, bot wa.JID) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.actions {
		if a.Bot == bot && a.Status == StatusRunning {
			a.Status = StatusPending
			a.UpdatedAt = s.clock.Now()
			n++
		}
	}
	return n, nil
}

// Candidates implements Store.
func (s *MemStore) Candidates(_ context.Context, bot wa.JID, n int) ([]Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type slot struct {
		p    Priority
		chat wa.JID
	}
	byChat := map[slot][]*Action{}
	byPriority := map[Priority][]*Action{}
	for _, a := range s.actions {
		if a.Bot == bot && a.Status == StatusPending {
			k := slot{a.Priority, a.Chat}
			byChat[k] = append(byChat[k], a)
			byPriority[a.Priority] = append(byPriority[a.Priority], a)
		}
	}
	picked := map[int64]*Action{}
	take := func(as []*Action, order func(a, b *Action) int) {
		slices.SortFunc(as, order)
		for _, a := range as[:min(n, len(as))] {
			picked[a.ID] = a
		}
	}
	for _, as := range byChat {
		take(as, queueOrder)
		take(as, func(a, b *Action) int { return cmp.Or(b.MessageTime.Compare(a.MessageTime), cmp.Compare(a.ID, b.ID)) })
	}
	for _, as := range byPriority {
		take(as, func(a, b *Action) int { return cmp.Or(a.Deadline.Compare(b.Deadline), cmp.Compare(a.ID, b.ID)) })
	}
	out := make([]Action, 0, len(picked))
	for _, a := range picked {
		out = append(out, clone(*a))
	}
	slices.SortFunc(out, func(a, b Action) int { return queueOrder(&a, &b) })
	return out, nil
}

// queueOrder is the table's order: priority, not_before, id.
func queueOrder(a, b *Action) int {
	return cmp.Or(cmp.Compare(a.Priority, b.Priority), a.NotBefore.Compare(b.NotBefore), cmp.Compare(a.ID, b.ID))
}

// Pending implements Store.
func (s *MemStore) Pending(_ context.Context, bot, chat wa.JID, kind Kind) ([]Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Action
	for _, a := range s.actions {
		if a.Bot == bot && a.Chat == chat && a.Kind == kind && a.Status == StatusPending {
			out = append(out, clone(*a))
		}
	}
	slices.SortFunc(out, func(a, b Action) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// Claim implements Store.
func (s *MemStore) Claim(_ context.Context, id int64) (Action, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.actions[id]
	if !ok || a.Status != StatusPending {
		return Action{}, false, nil
	}
	a.Status = StatusRunning
	a.Attempts++
	a.UpdatedAt = s.clock.Now()
	return clone(*a), true, nil
}

// Finish implements Store.
func (s *MemStore) Finish(_ context.Context, id int64, status Status, errText string, res Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.actions[id]
	if !ok {
		return fmt.Errorf("finish action %d: %w", id, ErrNotFound)
	}
	if a.Status != StatusRunning {
		return fmt.Errorf("finish action %d: status is %s, not running", id, a.Status)
	}
	if !status.Final() {
		return fmt.Errorf("finish action %d: %s is not a final status", id, status)
	}
	a.Result = res
	if status == StatusDone || status == StatusFailed {
		s.called[id] = true
	}
	s.finish(a, status, errText)
	return nil
}

// Retry implements Store.
func (s *MemStore) Retry(_ context.Context, id int64, r RetryInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.actions[id]
	if !ok {
		return fmt.Errorf("retry action %d: %w", id, ErrNotFound)
	}
	if a.Status != StatusRunning {
		return fmt.Errorf("retry action %d: status is %s, not running", id, a.Status)
	}
	if r.Called {
		s.called[id] = true
	}
	if !r.Attempted && a.Attempts > 0 {
		a.Attempts--
	}
	a.Status = StatusPending
	a.NotBefore = r.NotBefore
	a.LastError = r.Error
	a.Result = r.Result
	a.Result.Participants = slices.Clone(r.Result.Participants)
	a.UpdatedAt = s.clock.Now()
	return nil
}

// ExpireDue implements Store.
func (s *MemStore) ExpireDue(_ context.Context, bot wa.JID, now time.Time) ([]Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Action
	for _, a := range s.actions {
		if a.Bot == bot && a.Status == StatusPending && !a.Deadline.After(now) {
			s.finish(a, StatusExpired, "deadline passed")
			out = append(out, clone(*a))
		}
	}
	slices.SortFunc(out, func(a, b Action) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// FailPending implements Store.
func (s *MemStore) FailPending(ctx context.Context, bot, chat wa.JID, kinds []Kind, reason string) (int, error) {
	failed, err := s.FailChat(ctx, bot, chat, kinds, reason)
	return len(failed), err
}

// FailChat implements Store.
func (s *MemStore) FailChat(_ context.Context, bot, chat wa.JID, kinds []Kind, reason string) ([]Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Action
	for _, a := range s.actions {
		if a.Bot == bot && a.Chat == chat && a.Status == StatusPending && slices.Contains(kinds, a.Kind) {
			s.finish(a, StatusFailed, reason)
			out = append(out, clone(*a))
		}
	}
	slices.SortFunc(out, func(a, b Action) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// WritesSince implements Store.
func (s *MemStore) WritesSince(_ context.Context, bot wa.JID, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, a := range s.actions {
		if a.Bot == bot && s.called[id] && !a.UpdatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// Control implements Store.
func (s *MemStore) Control(_ context.Context, scope string) (Control, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controls[scope], nil
}

// UpdateControl implements Store.
func (s *MemStore) UpdateControl(_ context.Context, scope string, change func(*Control)) (Control, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.controls[scope]
	change(&c)
	s.controls[scope] = c
	return c, nil
}

// finish sets a final status. It must be called with s.mu held.
func (s *MemStore) finish(a *Action, status Status, errText string) {
	a.Status = status
	a.LastError = errText
	a.UpdatedAt = s.clock.Now()
}

func clone(a Action) Action {
	a.Payload.JIDs = slices.Clone(a.Payload.JIDs)
	a.Payload.Variants = slices.Clone(a.Payload.Variants)
	a.Result.Participants = slices.Clone(a.Result.Participants)
	return a
}
