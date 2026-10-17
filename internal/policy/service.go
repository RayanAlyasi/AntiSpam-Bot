package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// ErrSettingsConflict is returned by SettingsRepo.SaveSettings when the stored document is no
// longer at the version the change was based on.
var ErrSettingsConflict = errors.New("settings changed concurrently")

// StoredSettings is a group's settings document as stored: the raw JSON and its version
// (0 when the group has none yet).
type StoredSettings struct {
	Doc       json.RawMessage
	Version   int
	UpdatedBy string
	UpdatedAt time.Time
}

// SettingsChange is one save of a group's settings document (DESIGN.md §12.1, §14).
type SettingsChange struct {
	Group       wa.JID
	PrevVersion int // the version the change was made on; 0 for the group's first document
	Doc         json.RawMessage
	Actor       string   // who changed it: a person's identifier, "system" or "operator"
	Changes     []Change // the dotted-key diff, written to the audit log
}

// AuditDiff is the audit_log diff of a settings change: {"version": n, "changes": [...]}.
func (c SettingsChange) AuditDiff(version int) json.RawMessage {
	type change struct {
		Key  string          `json:"key"`
		From json.RawMessage `json:"from"`
		To   json.RawMessage `json:"to"`
	}
	out := struct {
		Version int      `json:"version"`
		Changes []change `json:"changes"`
	}{Version: version, Changes: make([]change, 0, len(c.Changes))}
	for _, ch := range c.Changes {
		out.Changes = append(out.Changes, change{Key: ch.Key, From: json.RawMessage(ch.From), To: json.RawMessage(ch.To)})
	}
	b, _ := json.Marshal(out) // only valid JSON values go in: Change holds encoded leaves
	return b
}

// SettingsRepo stores settings documents. The store package implements it on group_settings
// and group_settings_history.
type SettingsRepo interface {
	// LoadSettings returns the group's current document, or ok=false when it has none.
	LoadSettings(ctx context.Context, group wa.JID) (doc StoredSettings, ok bool, err error)
	// SaveSettings stores c.Doc as the next version if the stored version is still
	// c.PrevVersion, and returns the new version. In the same transaction it appends the
	// version to the history, writes the audit entry (action "settings.update", diff
	// c.AuditDiff) and notifies listeners of the change (NOTIFY). It returns
	// ErrSettingsConflict when another change got there first.
	SaveSettings(ctx context.Context, c SettingsChange) (version int, err error)
}

// SettingsService reads and changes group settings through a cache that listeners invalidate
// when another process changes a document (NOTIFY). It's safe for concurrent use.
type SettingsService struct {
	repo SettingsRepo

	mu    sync.Mutex
	cache map[wa.JID]cached
	gen   map[wa.JID]uint64 // bumped by every invalidation, so a load that raced it isn't cached
	all   uint64            // bumped by InvalidateAll
}

type cached struct {
	s       Settings
	version int
}

// NewSettingsService returns a service over repo.
func NewSettingsService(repo SettingsRepo) *SettingsService {
	return &SettingsService{repo: repo, cache: map[wa.JID]cached{}, gen: map[wa.JID]uint64{}}
}

// Get returns the group's settings and their version. A group without a stored document gets
// the defaults at version 0.
func (s *SettingsService) Get(ctx context.Context, group wa.JID) (Settings, int, error) {
	s.mu.Lock()
	c, ok := s.cache[group]
	s.mu.Unlock()
	if ok {
		return c.s.Clone(), c.version, nil
	}
	return s.load(ctx, group)
}

func (s *SettingsService) generation(group wa.JID) (uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen[group], s.all
}

// remember caches c unless the group was invalidated since gen was read, or a newer version is
// already cached: a load that read the store before a save or an invalidation never puts an
// older document back.
func (s *SettingsService) remember(group wa.JID, g, all uint64, c cached) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen[group] != g || s.all != all {
		return
	}
	if old, ok := s.cache[group]; ok && old.version > c.version {
		return
	}
	s.cache[group] = cached{s: c.s.Clone(), version: c.version}
}

func (s *SettingsService) load(ctx context.Context, group wa.JID) (Settings, int, error) {
	g, all := s.generation(group)
	doc, ok, err := s.repo.LoadSettings(ctx, group)
	if err != nil {
		return Settings{}, 0, fmt.Errorf("load settings of %s: %w", group, err)
	}
	c := cached{s: Defaults()}
	if ok {
		parsed, err := ParseStored(doc.Doc)
		if err != nil {
			return Settings{}, 0, fmt.Errorf("stored settings of %s (version %d): %w", group, doc.Version, err)
		}
		c = cached{s: parsed, version: doc.Version}
	}
	s.remember(group, g, all, c)
	return c.s.Clone(), c.version, nil
}

// Invalidate drops a group's cached settings, so the next Get reads the store. The NOTIFY
// listener calls it.
func (s *SettingsService) Invalidate(group wa.JID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, group)
	s.gen[group]++
}

// InvalidateAll drops every cached document; the listener calls it after reconnecting, since
// notifications may have been missed meanwhile.
func (s *SettingsService) InvalidateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.cache)
	s.all++
}

// maxConflictRetries bounds how often a change is re-applied on a newer version.
const maxConflictRetries = 3

// Set changes one dotted key (the `.set` command and the website use the same keys) and
// returns the new settings and what changed. A value that doesn't validate changes nothing.
// When another change lands first, the key is re-applied on the newer document.
func (s *SettingsService) Set(ctx context.Context, group wa.JID, actor, key string, value json.RawMessage) (Settings, []Change, error) {
	return s.update(ctx, group, actor, -1, func(cur Settings) (Settings, error) { return cur.Set(key, value) })
}

// Replace stores a whole document (the website's editor) that was edited from version base.
// If the stored document has moved on since, nothing is stored and ErrSettingsConflict is
// returned, so an edit never silently reverts another admin's change.
func (s *SettingsService) Replace(ctx context.Context, group wa.JID, actor string, next Settings, base int) (Settings, []Change, error) {
	if err := next.Validate(); err != nil {
		return Settings{}, nil, err
	}
	return s.update(ctx, group, actor, base, func(Settings) (Settings, error) { return next.Clone(), nil })
}

// ApplyPreset switches the group to preset p (it resets the matrix and the preset's extras).
func (s *SettingsService) ApplyPreset(ctx context.Context, group wa.JID, actor string, p Preset) (Settings, []Change, error) {
	return s.update(ctx, group, actor, -1, func(cur Settings) (Settings, error) {
		if err := cur.ApplyPreset(p); err != nil {
			return Settings{}, err
		}
		return cur, cur.Validate()
	})
}

// update applies change to the latest document and saves it. base, when not negative, is the
// version the change must apply to; otherwise the change is re-applied after a conflict.
func (s *SettingsService) update(ctx context.Context, group wa.JID, actor string, base int, change func(Settings) (Settings, error)) (Settings, []Change, error) {
	if actor == "" {
		return Settings{}, nil, fmt.Errorf("%w: a settings change needs an actor", ErrInvalidSettings)
	}
	for range maxConflictRetries {
		cur, version, err := s.load(ctx, group) // always fresh: a stale cache would only conflict
		if err != nil {
			return Settings{}, nil, err
		}
		if base >= 0 && version != base {
			return Settings{}, nil, fmt.Errorf("save settings of %s: %w (edited from version %d, stored %d)", group, ErrSettingsConflict, base, version)
		}
		next, err := change(cur.Clone())
		if err != nil {
			return Settings{}, nil, err
		}
		changes, err := Diff(cur, next)
		if err != nil {
			return Settings{}, nil, err
		}
		if len(changes) == 0 {
			return next, nil, nil
		}
		doc, err := json.Marshal(next)
		if err != nil {
			return Settings{}, nil, fmt.Errorf("encode settings of %s: %w", group, err)
		}
		g, all := s.generation(group)
		v, err := s.repo.SaveSettings(ctx, SettingsChange{Group: group, PrevVersion: version, Doc: doc, Actor: actor, Changes: changes})
		if errors.Is(err, ErrSettingsConflict) {
			s.Invalidate(group)
			if base >= 0 {
				return Settings{}, nil, fmt.Errorf("save settings of %s: %w", group, err)
			}
			continue
		}
		if err != nil {
			return Settings{}, nil, fmt.Errorf("save settings of %s: %w", group, err)
		}
		s.remember(group, g, all, cached{s: next, version: v})
		return next, changes, nil
	}
	return Settings{}, nil, fmt.Errorf("save settings of %s: %w (gave up after %d tries)", group, ErrSettingsConflict, maxConflictRetries)
}

// Load returns the group's settings, or the defaults for a group with none yet. With Save it
// is the settings port the review and command layer uses (review.SettingsDocs).
func (s *SettingsService) Load(ctx context.Context, group wa.JID) (Settings, error) {
	st, _, err := s.Get(ctx, group)
	return st, err
}

// Save stores a document the caller built from a Load. Only the listed dotted-key changes are
// written: each changed key takes its value from next (a key next doesn't have is removed), all
// in one step on the latest stored document, which is then validated as a whole. So a
// concurrent change to another key isn't lost, and a multi-key change (a preset switch, a set
// of thresholds) never fails on an intermediate state. With no changes nothing is stored.
func (s *SettingsService) Save(ctx context.Context, group wa.JID, next Settings, actor wa.JID, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	want, err := toTree(next)
	if err != nil {
		return err
	}
	_, _, err = s.update(ctx, group, string(actor), -1, func(cur Settings) (Settings, error) {
		// A preset switch resets the matrix and the extras on the latest document first, so a
		// concurrent switch can't leave cells of two presets under one preset name.
		for _, c := range changes {
			if c.Key == "preset" {
				if err := cur.ApplyPreset(next.Preset); err != nil {
					return Settings{}, err
				}
				break
			}
		}
		tree, err := toTree(cur)
		if err != nil {
			return Settings{}, err
		}
		for _, c := range changes {
			v, ok := lookup(want, splitKey(c.Key))
			if err := put(tree, splitKey(c.Key), v, ok); err != nil {
				return Settings{}, fmt.Errorf("%s: %w", c.Key, err)
			}
		}
		raw, err := json.Marshal(tree)
		if err != nil {
			return Settings{}, fmt.Errorf("encode settings: %w", err)
		}
		return ParseStored(raw)
	})
	return err
}

// lookup returns the value at path in a decoded document.
func lookup(tree map[string]any, path []string) (any, bool) {
	var cur any = tree
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// put sets (or, with ok false, removes) the value at path in a decoded document, creating
// missing objects on the way.
func put(tree map[string]any, path []string, v any, ok bool) error {
	if len(path) == 0 {
		return fmt.Errorf("%w: empty key", ErrUnknownKey)
	}
	obj := tree
	for _, p := range path[:len(path)-1] {
		next, isObj := obj[p].(map[string]any)
		if !isObj {
			if !ok {
				return nil // nothing to remove
			}
			next = map[string]any{}
			obj[p] = next
		}
		obj = next
	}
	last := path[len(path)-1]
	if ok {
		obj[last] = v
	} else {
		delete(obj, last)
	}
	return nil
}
