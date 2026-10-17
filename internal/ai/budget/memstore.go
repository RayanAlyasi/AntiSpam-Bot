package budget

import (
	"context"
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests and for running without a database. It is safe for
// concurrent use.
type MemStore struct {
	mu   sync.Mutex
	rows map[memKey]*Row
}

type memKey struct {
	day   time.Time
	group string
}

// Row is one ai_usage row.
type Row struct {
	Calls                     int
	InputTokens, OutputTokens int64
	CostMicros                int64
}

var _ Store = (*MemStore)(nil)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore { return &MemStore{rows: map[memKey]*Row{}} }

// Reserve implements Store.
func (s *MemStore) Reserve(_ context.Context, day time.Time, group string, lim Limits) (Counts, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	day = Day(day)
	var c Counts
	for k, r := range s.rows {
		if k.day.Year() == day.Year() && k.day.Month() == day.Month() {
			c.InstanceMonth += r.Calls
			if k.day.Equal(day) {
				c.InstanceDay += r.Calls
				if k.group == group {
					c.GroupDay += r.Calls
				}
			}
		}
	}
	over := func(used, limit int) bool { return limit > 0 && used+1 > limit }
	if over(c.GroupDay, lim.GroupPerDay) || over(c.InstanceDay, lim.InstancePerDay) || over(c.InstanceMonth, lim.InstancePerMonth) {
		return c, false, nil
	}
	s.row(day, group).Calls++
	c.GroupDay++
	c.InstanceDay++
	c.InstanceMonth++
	return c, true, nil
}

// AddUsage implements Store.
func (s *MemStore) AddUsage(_ context.Context, day time.Time, group string, u Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(Day(day), group)
	r.InputTokens += u.InputTokens
	r.OutputTokens += u.OutputTokens
	r.CostMicros += u.CostMicros
	return nil
}

// Get returns a copy of the (day, group) row.
func (s *MemStore) Get(day time.Time, group string) Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rows[memKey{Day(day), group}]; ok {
		return *r
	}
	return Row{}
}

func (s *MemStore) row(day time.Time, group string) *Row {
	k := memKey{day, group}
	r, ok := s.rows[k]
	if !ok {
		r = &Row{}
		s.rows[k] = r
	}
	return r
}
