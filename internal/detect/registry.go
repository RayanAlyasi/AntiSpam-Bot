package detect

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Registry holds the compiled-in analyzers and their instance-level enable flags
// (DESIGN.md §22.2). Per-group choices are the analyzer's own Applies. It's safe for
// concurrent use.
type Registry[E, P any] struct {
	mu      sync.RWMutex
	entries []entry[E, P] // registration order
	byName  map[string]int
}

type entry[E, P any] struct {
	a       Analyzer[E, P]
	tier    Tier
	enabled bool
}

// NewRegistry returns an empty registry.
func NewRegistry[E, P any]() *Registry[E, P] {
	return &Registry[E, P]{byName: map[string]int{}}
}

// Register adds an analyzer, enabled. Its name must be non-empty and unique, and its tier one of
// T0–T4. The tier is read once here.
func (r *Registry[E, P]) Register(a Analyzer[E, P]) error {
	if a == nil {
		return errors.New("register analyzer: nil")
	}
	name, tier := a.Name(), a.Tier()
	if name == "" {
		return errors.New("register analyzer: empty name")
	}
	if !tier.Valid() {
		return fmt.Errorf("register analyzer %q: invalid tier %v", name, tier)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("register analyzer %q: name already registered", name)
	}
	r.byName[name] = len(r.entries)
	r.entries = append(r.entries, entry[E, P]{a: a, tier: tier, enabled: true})
	return nil
}

// SetEnabled turns one analyzer on or off. An unknown name is an error, so a typo in the
// configuration doesn't silently leave an analyzer running.
func (r *Registry[E, P]) SetEnabled(name string, on bool) error {
	return r.Configure(map[string]bool{name: on})
}

// Configure applies enable flags by name. Names not in flags keep their state. If any name is
// unknown, nothing changes.
func (r *Registry[E, P]) Configure(flags map[string]bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var unknown []string
	for name := range flags {
		if _, ok := r.byName[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("configure analyzers: unknown %q", unknown)
	}
	for name, on := range flags {
		r.entries[r.byName[name]].enabled = on
	}
	return nil
}

// Enabled reports whether the named analyzer is registered and enabled.
func (r *Registry[E, P]) Enabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.byName[name]
	return ok && r.entries[i].enabled
}

// Names returns every registered name, in registration order.
func (r *Registry[E, P]) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.entries))
	for i, e := range r.entries {
		names[i] = e.a.Name()
	}
	return names
}

// tier returns the enabled analyzers of tier t, in registration order.
func (r *Registry[E, P]) tier(t Tier) []Analyzer[E, P] {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Analyzer[E, P]
	for _, e := range r.entries {
		if e.enabled && e.tier == t {
			out = append(out, e.a)
		}
	}
	return out
}
