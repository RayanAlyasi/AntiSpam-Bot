package policy

import (
	"encoding/json"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestStartFrom: a group with no settings of its own starts with a copy of its community's
// document; a group that has settings, or a community without any, changes nothing (§12.4).
func TestStartFrom(t *testing.T) {
	const (
		community wa.JID = "1203100@g.us"
		fresh     wa.JID = "1203101@g.us"
		set       wa.JID = "1203102@g.us"
		bare      wa.JID = "1203199@g.us"
	)
	ctx := t.Context()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo)
	if _, _, err := svc.Set(ctx, community, "a", "mode", json.RawMessage(`"shadow"`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Set(ctx, set, "a", "mode", json.RawMessage(`"paused"`)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		group, from wa.JID
		wantStarted bool
		wantMode    Mode
	}{
		{fresh, community, true, ModeShadow},
		{fresh, community, false, ModeShadow}, // it has settings now
		{set, community, false, ModePaused},
		{bare, "1203198@g.us", false, Defaults().Mode},
	}
	for _, tt := range tests {
		started, err := svc.StartFrom(ctx, tt.group, tt.from, "system")
		if err != nil || started != tt.wantStarted {
			t.Errorf("StartFrom(%s, %s) = %v, %v; want %v", tt.group, tt.from, started, err, tt.wantStarted)
		}
		if s, _, _ := svc.Get(ctx, tt.group); s.Mode != tt.wantMode {
			t.Errorf("%s mode = %s, want %s", tt.group, s.Mode, tt.wantMode)
		}
	}
	if h := repo.History(fresh); len(h) != 1 || h[0].UpdatedBy != "system" {
		t.Errorf("history of the started group = %+v, want one version by system", h)
	}
}

// TestStartFromKeepsDefaultsEqualToBuiltIns: with new groups asking the admins first, a community
// set back to auto (equal to Defaults()) still starts its new groups in auto.
func TestStartFromKeepsDefaultsEqualToBuiltIns(t *testing.T) {
	const community, group wa.JID = "1203200@g.us", "1203201@g.us"
	ctx := t.Context()
	repo := NewMemSettingsRepo(clock.NewFake(t0))
	svc := NewSettingsService(repo, WithNewGroupMode(EnforceAskBeforeAction))
	if _, _, err := svc.Set(ctx, community, "a", "enforcement.mode", json.RawMessage(`"auto"`)); err != nil {
		t.Fatal(err)
	}
	if started, err := svc.StartFrom(ctx, group, community, "system"); err != nil || !started {
		t.Fatalf("StartFrom = %v, %v; want the community's settings stored", started, err)
	}
	if s, v, _ := svc.Get(ctx, group); s.Enforcement.Mode != EnforceAuto || v != 1 {
		t.Errorf("new group: mode %s at version %d, want the community's auto stored as version 1", s.Enforcement.Mode, v)
	}
}
