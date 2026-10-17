package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// TestPresetThroughSettingsService: with wp-i's settings service, .preset switches in one step
// on the latest document (no mixed grids when two admins switch at once).
func TestPresetThroughSettingsService(t *testing.T) {
	ctx := context.Background()
	repo := policy.NewMemSettingsRepo(clock.NewFake(chattest.Start))
	s := chat.DocSettings{Docs: policy.NewSettingsService(repo)}
	changes, err := s.ApplyPreset(ctx, cars, "ask_admins", sara)
	if err != nil || len(changes) == 0 {
		t.Fatalf("ApplyPreset = %d changes, %v; want the preset's changes", len(changes), err)
	}
	if v, _ := s.Get(ctx, cars, "enforcement.mode"); v != "ask_delete_first" {
		t.Errorf("enforcement.mode = %q after the ask preset, want ask_delete_first (DESIGN §12.2)", v)
	}
	if a := repo.Audit(); len(a) == 0 {
		t.Error("preset switch wrote no audit entry, want one (DESIGN §13.7)")
	}
	if _, err := s.ApplyPreset(ctx, cars, "bogus", sara); !errors.Is(err, chat.ErrInvalidValue) {
		t.Errorf("unknown preset: %v, want ErrInvalidValue", err)
	}
}
