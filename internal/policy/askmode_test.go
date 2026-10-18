package policy

import (
	"context"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestPresetKeepsAskMode: switching preset never turns asking the admins off. Only the Ask admins
// preset sets an ask mode; any other preset keeps the group's ask mode, and sets auto otherwise
// (maintainer decision of 2026-10-18).
func TestPresetKeepsAskMode(t *testing.T) {
	for _, tt := range []struct {
		from   EnforcementMode
		preset Preset
		want   EnforcementMode
	}{
		{EnforceAuto, PresetRelaxed, EnforceAuto},
		{EnforceAuto, PresetStrict, EnforceAuto},
		{EnforceAuto, PresetAskAdmins, EnforceAskDeleteFirst},
		{EnforceAskBeforeAction, PresetRelaxed, EnforceAskBeforeAction},
		{EnforceAskBeforeAction, PresetStandard, EnforceAskBeforeAction},
		{EnforceAskBeforeAction, PresetStrict, EnforceAskBeforeAction},
		{EnforceAskBeforeAction, PresetAskAdmins, EnforceAskDeleteFirst},
		{EnforceAskDeleteFirst, PresetStrict, EnforceAskDeleteFirst},
		{EnforceAskDeleteFirst, PresetCustom, EnforceAskDeleteFirst},
		{EnforceAuto, PresetCustom, EnforceAuto},
	} {
		s := Defaults()
		s.Enforcement.Mode = tt.from
		if err := s.ApplyPreset(tt.preset); err != nil {
			t.Fatalf("ApplyPreset(%s): %v", tt.preset, err)
		}
		if s.Enforcement.Mode != tt.want {
			t.Errorf("%s then preset %s: mode %s, want %s (a preset must not silently stop asking the admins)", tt.from, tt.preset, s.Enforcement.Mode, tt.want)
		}
		if tt.preset != PresetCustom && s.Enforcement.AutobanMatrix[TypeText].Action != presetMatrix(tt.preset)[TypeText].Action {
			t.Errorf("%s then preset %s: the matrix wasn't reset to the preset", tt.from, tt.preset)
		}
	}
	for _, p := range []Preset{PresetRelaxed, PresetStandard, PresetStrict, PresetAskAdmins} {
		s, err := PresetSettings(p)
		if err != nil {
			t.Fatal(err)
		}
		if s.Enforcement.Mode != presetMode[p] {
			t.Errorf("PresetSettings(%s) mode = %s, want %s (unchanged: it starts from Defaults())", p, s.Enforcement.Mode, presetMode[p])
		}
	}
}

// TestNewGroupMode: a group with nothing stored starts in the configured mode, and keeps it
// through its first saved change and a preset switch; without the option it's Defaults().
func TestNewGroupMode(t *testing.T) {
	ctx := context.Background()
	const group wa.JID = "120363000000000001@g.us"
	for _, tt := range []struct {
		name string
		opts []SettingsOption
		want EnforcementMode
	}{
		{"no option", nil, EnforceAuto},
		{"before", []SettingsOption{WithNewGroupMode(EnforceAskBeforeAction)}, EnforceAskBeforeAction},
		{"delete", []SettingsOption{WithNewGroupMode(EnforceAskDeleteFirst)}, EnforceAskDeleteFirst},
		{"off", []SettingsOption{WithNewGroupMode(EnforceAuto)}, EnforceAuto},
		{"unknown is ignored", []SettingsOption{WithNewGroupMode("sometimes")}, EnforceAuto},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSettingsService(NewMemSettingsRepo(clock.NewFake(time.Unix(0, 0))), tt.opts...)
			s, version, err := svc.Get(ctx, group)
			if err != nil || version != 0 || s.Enforcement.Mode != tt.want {
				t.Fatalf("Get(new group) = mode %s, version %d, %v; want %s at version 0", s.Enforcement.Mode, version, err, tt.want)
			}
			if err := s.Validate(); err != nil {
				t.Fatalf("new-group settings don't validate: %v", err)
			}
			if _, _, err := svc.ApplyPreset(ctx, group, "admin", PresetStrict); err != nil {
				t.Fatal(err)
			}
			s, version, err = svc.Get(ctx, group)
			if err != nil || version != 1 || s.Enforcement.Mode != tt.want {
				t.Errorf("after .preset strict: mode %s, version %d, %v; want %s stored at version 1", s.Enforcement.Mode, version, err, tt.want)
			}
		})
	}
}
