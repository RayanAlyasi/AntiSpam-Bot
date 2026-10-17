package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

// TestDocSettings runs the settings commands' port against real policy documents: the same
// keys, validation and presets as the website (DESIGN §12.1).
func TestDocSettings(t *testing.T) {
	ctx := context.Background()
	docs := &review.MemoryDocs{}
	s := chat.DocSettings{Docs: docs}

	if v, err := s.Get(ctx, cars, "review.destination"); err != nil || v != "dashboard" {
		t.Fatalf("default review.destination = %q, %v; want dashboard", v, err)
	}
	ch, err := s.Set(ctx, cars, "review.timeout", "2h", sara)
	if err != nil || ch.New != "2h0m0s" || ch.Old != "6h0m0s" {
		t.Fatalf("Set review.timeout 2h = %+v, %v", ch, err)
	}
	if _, err := s.Set(ctx, cars, "no.such.key", "1", sara); !errors.Is(err, chat.ErrUnknownKey) {
		t.Errorf("unknown key: %v, want ErrUnknownKey", err)
	}
	if _, err := s.Set(ctx, cars, "review.destination", "mars", sara); !errors.Is(err, chat.ErrInvalidValue) {
		t.Errorf("invalid enum: %v, want ErrInvalidValue", err)
	}
	if v, _ := s.Get(ctx, cars, "review.destination"); v != "dashboard" {
		t.Errorf("a refused value changed the document: destination = %q", v)
	}
	if _, err := s.ApplyPreset(ctx, cars, "ask_admins", sara); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get(ctx, cars, "enforcement.mode"); v != "ask_delete_first" {
		t.Errorf("after preset ask_admins enforcement.mode = %q, want ask_delete_first (DESIGN §12.2)", v)
	}
	if _, err := s.ApplyPreset(ctx, cars, "bogus", sara); !errors.Is(err, chat.ErrInvalidValue) {
		t.Errorf("unknown preset: %v, want ErrInvalidValue", err)
	}

	cfg, err := review.PolicySettings{Docs: docs}.ReviewConfig(ctx, cars)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != review.ModeAskDeleteFirst || cfg.Timeout != 2*time.Hour {
		t.Errorf("review config = %+v, want ask_delete_first and the 2h timeout", cfg)
	}
	if _, err := s.Set(ctx, cars, "enforcement.mode", "ask_before_action", sara); err != nil {
		t.Fatal(err)
	}
	cfg, _ = review.PolicySettings{Docs: docs}.ReviewConfig(ctx, cars)
	if cfg.Timeout != 15*time.Minute || cfg.OnTimeout != review.TimeoutDeleteNoBan {
		t.Errorf("mode (b) review config = %v %s, want 15m delete_no_ban from enforcement.ask_before", cfg.Timeout, cfg.OnTimeout)
	}
}
