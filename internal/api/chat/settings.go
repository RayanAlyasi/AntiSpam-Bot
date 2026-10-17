package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// DocSettings is the Settings port backed by policy settings documents: the same dotted keys,
// validation and presets as the website (§12.1, F-054, F-055).
type DocSettings struct {
	Docs review.SettingsDocs
}

var _ Settings = DocSettings{}

// text renders a JSON value the way an admin would type it: strings unquoted, the rest as JSON.
func text(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func mapErr(key string, err error) error {
	switch {
	case errors.Is(err, policy.ErrUnknownKey):
		return fmt.Errorf("%q: %w", key, ErrUnknownKey)
	case errors.Is(err, policy.ErrInvalidSettings):
		return fmt.Errorf("%w: %w", ErrInvalidValue, err)
	}
	return err
}

// Get returns a key's value.
func (d DocSettings) Get(ctx context.Context, group wa.JID, key string) (string, error) {
	s, err := d.Docs.Load(ctx, group)
	if err != nil {
		return "", fmt.Errorf("load settings %s: %w", group, err)
	}
	raw, err := s.Get(key)
	if err != nil {
		return "", mapErr(key, err)
	}
	return text(raw), nil
}

// Set validates and stores one value, typed as an admin would ("2h", "true", "ask").
func (d DocSettings) Set(ctx context.Context, group wa.JID, key, value string, actor wa.JID) (Change, error) {
	s, err := d.Docs.Load(ctx, group)
	if err != nil {
		return Change{}, fmt.Errorf("load settings %s: %w", group, err)
	}
	old, err := s.Get(key)
	if err != nil {
		return Change{}, mapErr(key, err)
	}
	next, err := s.SetText(key, value)
	if err != nil {
		return Change{}, mapErr(key, err)
	}
	if err := next.Validate(); err != nil {
		return Change{}, mapErr(key, err)
	}
	changes, err := policy.Diff(s, next)
	if err != nil {
		return Change{}, fmt.Errorf("diff settings: %w", err)
	}
	if err := d.Docs.Save(ctx, group, next, actor, changes); err != nil {
		return Change{}, mapErr(key, err)
	}
	now, _ := next.Get(key)
	return Change{Key: key, Old: text(old), New: text(now)}, nil
}

// presetApplier is a settings store that switches presets in one step on the latest document
// (policy.SettingsService), so two admins switching presets at once never mix the two grids.
type presetApplier interface {
	ApplyPreset(ctx context.Context, group wa.JID, actor string, p policy.Preset) (policy.Settings, []policy.Change, error)
}

var _ presetApplier = (*policy.SettingsService)(nil)

func changesOf(diff []policy.Change) []Change {
	out := make([]Change, len(diff))
	for i, c := range diff {
		out[i] = Change{Key: c.Key, Old: c.From, New: c.To}
	}
	return out
}

// ApplyPreset switches the preset and resets the auto-ban matrix to it.
func (d DocSettings) ApplyPreset(ctx context.Context, group wa.JID, preset string, actor wa.JID) ([]Change, error) {
	p := policy.Preset(strings.ToLower(preset))
	if pa, ok := d.Docs.(presetApplier); ok {
		_, diff, err := pa.ApplyPreset(ctx, group, string(actor), p)
		if err != nil {
			return nil, fmt.Errorf("preset %q: %w", preset, mapErr("preset", err))
		}
		return changesOf(diff), nil
	}
	s, err := d.Docs.Load(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("load settings %s: %w", group, err)
	}
	next := s.Clone()
	if err := next.ApplyPreset(p); err != nil {
		return nil, fmt.Errorf("preset %q: %w: %w", preset, ErrInvalidValue, err)
	}
	diff, err := policy.Diff(s, next)
	if err != nil {
		return nil, fmt.Errorf("diff settings: %w", err)
	}
	if err := d.Docs.Save(ctx, group, next, actor, diff); err != nil {
		return nil, mapErr("preset", err)
	}
	return changesOf(diff), nil
}
