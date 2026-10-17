package review

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// SettingsDocs loads and saves groups' settings documents (F-054). The Postgres store with
// versions, history and NOTIFY implements it; MemoryDocs serves tests and early wiring.
type SettingsDocs interface {
	// Load returns a group's settings, or policy.Defaults() for a group with none yet.
	Load(ctx context.Context, group wa.JID) (policy.Settings, error)
	// Save stores a validated document with the changes that produced it.
	Save(ctx context.Context, group wa.JID, s policy.Settings, actor wa.JID, changes []policy.Change) error
}

// ConfigFrom extracts the review configuration from a settings document. In ask-admins mode
// (b) the timeout and fallback come from enforcement.ask_before, whose fallback can never ban.
func ConfigFrom(s policy.Settings) Config {
	cfg := Config{
		Destination: Destination(s.Review.Destination),
		ReviewGroup: wa.JID(s.Review.ReviewGroup),
		Timeout:     time.Duration(s.Review.Timeout),
		OnTimeout:   OnTimeout(s.Review.OnTimeout),
		Mode:        EnforcementMode(s.Enforcement.Mode),
		Language:    s.Review.Language,
		Ladder:      s.Ladder.Enabled,
		BlurMedia:   s.Review.BlurMedia,
	}
	if len(s.Review.Reactions.Ban) > 0 || len(s.Review.Reactions.NotSpam) > 0 {
		cfg.Reactions = map[Decision][]string{DecisionBan: s.Review.Reactions.Ban, DecisionNotSpam: s.Review.Reactions.NotSpam}
	}
	if cfg.Mode == ModeAskBeforeAction {
		cfg.Timeout = time.Duration(s.Enforcement.AskBefore.Timeout)
		cfg.OnTimeout = TimeoutDeleteNoBan
		if s.Enforcement.AskBefore.OnTimeout == "leave" {
			cfg.OnTimeout = TimeoutDismiss
		}
	}
	return cfg
}

// PolicySettings is the Settings port backed by settings documents.
type PolicySettings struct {
	Docs SettingsDocs
}

var _ Settings = PolicySettings{}

// ReviewConfig loads the group's document and extracts its review configuration.
func (p PolicySettings) ReviewConfig(ctx context.Context, group wa.JID) (Config, error) {
	s, err := p.Docs.Load(ctx, group)
	if err != nil {
		return Config{}, fmt.Errorf("load settings %s: %w", group, err)
	}
	return ConfigFrom(s), nil
}

// MemoryDocs is an in-memory SettingsDocs, safe for concurrent use.
type MemoryDocs struct {
	mu   sync.Mutex
	docs map[wa.JID]policy.Settings
}

var _ SettingsDocs = (*MemoryDocs)(nil)

// Load returns the group's document, or the defaults.
func (m *MemoryDocs) Load(ctx context.Context, group wa.JID) (policy.Settings, error) {
	if err := ctx.Err(); err != nil {
		return policy.Settings{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.docs[group]; ok {
		return s.Clone(), nil
	}
	return policy.Defaults(), nil
}

// Save validates and stores the document.
func (m *MemoryDocs) Save(ctx context.Context, group wa.JID, s policy.Settings, _ wa.JID, _ []policy.Change) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs == nil {
		m.docs = map[wa.JID]policy.Settings{}
	}
	m.docs[group] = s.Clone()
	return nil
}
