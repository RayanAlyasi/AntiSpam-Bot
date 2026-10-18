// Package contact detects messages that offer a way to reach someone outside the group: a
// phone number, a contact card, a group or channel invite, or a wa.me / t.me style link
// (contract .agent/contracts/holdout-recall.md). Stub: the builder replaces it.
package contact

import (
	"context"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// CodeAdvertised is the signal code: the message advertises a contact channel.
const CodeAdvertised = "contact.advertised"

// Weight is the signal's default log-odds weight.
const Weight = 1.0

// Analyzer is the T0 contact-channel analyzer. P is the group-policy type; it isn't read.
type Analyzer[P any] struct{}

// New returns the analyzer.
func New[P any]() *Analyzer[P] { panic("not implemented") }

// Name implements detect.Analyzer.
func (*Analyzer[P]) Name() string { panic("not implemented") }

// Tier implements detect.Analyzer.
func (*Analyzer[P]) Tier() detect.Tier { panic("not implemented") }

// Applies implements detect.Analyzer.
func (*Analyzer[P]) Applies(_ *envelope.Envelope, _ *P) bool { panic("not implemented") }

// Analyze implements detect.Analyzer.
func (*Analyzer[P]) Analyze(_ context.Context, _ *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	panic("not implemented")
}
