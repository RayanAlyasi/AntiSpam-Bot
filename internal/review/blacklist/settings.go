package blacklist

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// PolicySettings is the Settings port backed by group settings documents. v0.1 has no
// blacklist.* keys yet, so every group uses the instance list (the §9.8 default) and local
// numbers are read with the instance's default country.
type PolicySettings struct {
	Docs           review.SettingsDocs
	DefaultCountry string
}

var _ Settings = PolicySettings{}

// Blacklist returns a group's blacklist settings.
func (p PolicySettings) Blacklist(ctx context.Context, group wa.JID) (GroupSettings, error) {
	s, err := p.Docs.Load(ctx, group)
	if err != nil {
		return GroupSettings{}, fmt.Errorf("load settings %s: %w", group, err)
	}
	return GroupSettings{
		UseInstance: true, Mode: review.EnforcementMode(s.Enforcement.Mode), DefaultCountry: p.DefaultCountry,
		Inactive: s.Mode != policy.ModeEnforce,
	}, nil
}
