package policy

import (
	"bytes"
	"fmt"
)

// presetDelete is each preset's delete line; Strict lowers it (DESIGN.md §12.2).
var presetDelete = map[Preset]float64{
	PresetRelaxed:   0.85,
	PresetStandard:  0.85,
	PresetStrict:    0.80,
	PresetAskAdmins: 0.85,
}

// presetMode is each preset's enforcement mode: only Ask admins changes it, to variant (a).
var presetMode = map[Preset]EnforcementMode{
	PresetRelaxed:   EnforceAuto,
	PresetStandard:  EnforceAuto,
	PresetStrict:    EnforceAuto,
	PresetAskAdmins: EnforceAskDeleteFirst,
}

// applyPreset overwrites what p controls: the matrix and the preset's extras. Custom only
// renames the preset and keeps the current values.
func applyPreset(s *Settings, p Preset) {
	s.Preset = p
	if p == PresetCustom {
		return
	}
	s.Enforcement.AutobanMatrix = presetMatrix(p)
	// A group that asks its admins keeps asking when it switches to another preset: only the
	// Ask admins preset sets an ask mode, and no preset turns asking off (maintainer decision of
	// 2026-10-18). .askadmins off does that.
	if m := presetMode[p]; m.AsksAdmins() || !s.Enforcement.Mode.AsksAdmins() {
		s.Enforcement.Mode = m
	}
	s.Detection.Thresholds.Delete = presetDelete[p]
	if s.Review.OnTimeout == TimeoutAutoBan && s.Enforcement.Mode.AsksAdmins() {
		s.Review.OnTimeout = TimeoutKeepDeletedNoBan
	}
}

// PresetSettings returns the default settings with preset p applied. Custom starts from
// Standard.
func PresetSettings(p Preset) (Settings, error) {
	s := Defaults()
	if err := s.ApplyPreset(p); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// ApplyPreset switches s to preset p. It resets the auto-ban matrix and the preset's extras
// (enforcement mode, delete line) and keeps every other override (DESIGN.md §9.2: changing the
// preset resets the grid). Custom keeps the current grid and extras.
func (s *Settings) ApplyPreset(p Preset) error {
	if err := oneOf("preset", p, PresetRelaxed, PresetStandard, PresetStrict, PresetAskAdmins, PresetCustom); err != nil {
		return err
	}
	applyPreset(s, p)
	return nil
}

// Change is one leaf that differs between two settings documents.
type Change struct {
	Key  string // dotted key
	From string // JSON value in the reference document
	To   string // JSON value in this document
}

// String renders the change as "key: from → to".
func (c Change) String() string { return fmt.Sprintf("%s: %s → %s", c.Key, c.From, c.To) }

// DiffFromPreset lists the leaves that differ from s's preset applied to the defaults, sorted
// by key, so the website and `.autoban` can show what the group changed. Custom is compared
// against Standard.
func (s Settings) DiffFromPreset() ([]Change, error) {
	ref, err := PresetSettings(s.Preset)
	if err != nil {
		return nil, err
	}
	return Diff(ref, s)
}

// Diff lists the leaves whose values differ between from and to, sorted by dotted key.
func Diff(from, to Settings) ([]Change, error) {
	a, err := leaves(from)
	if err != nil {
		return nil, err
	}
	b, err := leaves(to)
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, k := range unionKeys(a, b) {
		if !bytes.Equal(a[k], b[k]) {
			out = append(out, Change{Key: k, From: orNull(a[k]), To: orNull(b[k])})
		}
	}
	return out, nil
}

func orNull(b []byte) string {
	if b == nil {
		return "null"
	}
	return string(b)
}
