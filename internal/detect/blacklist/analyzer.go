package blacklist

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// CodeBlacklisted is the signal for a manually blacklisted number in the content. It is
// known spam evidence (confidence source known, §7.3, §9.8 step 4).
const CodeBlacklisted = "number.blacklisted"

// Weight is the signal weight. Known evidence is CERTAIN whatever the weight.
const Weight = 6.0

// Analyzer flags messages that advertise a blacklisted number (T0): in a contact card (waid
// or TEL), in a wa.me link or written in the text. The lookup uses the in-memory blacklist,
// so removing an entry lifts the signal as soon as the rules snapshot reloads.
type Analyzer[P any] struct {
	src  ruleview.Source
	hash func(e164 string) string
}

// New returns the analyzer; hash is Hasher.Hash.
func New[P any](src ruleview.Source, hash func(e164 string) string) *Analyzer[P] {
	return &Analyzer[P]{src: src, hash: hash}
}

// Name implements detect.Analyzer.
func (*Analyzer[P]) Name() string { return "number.blacklist" }

// Tier implements detect.Analyzer.
func (*Analyzer[P]) Tier() detect.Tier { return detect.T0 }

// Applies implements detect.Analyzer.
func (*Analyzer[P]) Applies(e *envelope.Envelope, _ *P) bool {
	return len(e.Phones) > 0 || len(e.URLs) > 0 || len(e.Texts) > 0 || len(e.Contacts) > 0
}

// Analyze implements detect.Analyzer. It emits one signal, for the first listed number.
func (a *Analyzer[P]) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	v := a.src.View(e)
	for _, n := range Numbers(e) {
		entry := v.Blacklisted(a.hash(n.E164))
		if entry == nil {
			continue
		}
		scope := "this group's"
		if entry.Scope == rules.BlacklistInstance {
			scope = "the instance"
		}
		return []detect.Signal{{
			Code: CodeBlacklisted, Weight: Weight, Known: true,
			Evidence: fmt.Sprintf("number %s in %s is on %s blacklist", Mask(n.E164), n.Source, scope),
		}}, nil
	}
	return nil, nil
}
