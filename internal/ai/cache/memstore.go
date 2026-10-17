package cache

import (
	"context"
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests and for running without a database.
type MemStore struct {
	mu   sync.Mutex
	rows map[string]Entry
}

var _ Store = (*MemStore)(nil)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore { return &MemStore{rows: map[string]Entry{}} }

// Get implements Store.
func (s *MemStore) Get(_ context.Context, key string, notBefore time.Time) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.rows[key]
	if !ok || e.CreatedAt.Before(notBefore) {
		return Entry{}, false, nil
	}
	return e, true, nil
}

// Put implements Store.
func (s *MemStore) Put(_ context.Context, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[e.Key] = e
	return nil
}

// Len returns the number of stored entries.
func (s *MemStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}
