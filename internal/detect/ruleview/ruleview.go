// Package ruleview connects the rule analyzers to the current rules snapshot: it narrows the
// snapshot to the scopes of one message's group (DESIGN.md §12.3).
package ruleview

import (
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// Settings returns the group-settings part of the view options for a message: subscribed
// packs, allow-listed domains and whether the instance blacklist applies (§12.1). It is
// called on every message, so it must read an in-memory settings cache. Bot and Group are
// taken from the envelope and need not be set.
type Settings func(e *envelope.Envelope) rules.ViewOptions

// Source is what every rule analyzer is built with.
type Source struct {
	Rules    *rules.Holder
	Settings Settings // nil: no packs, no allow-list, no instance blacklist
}

// View returns the rules that apply to e.
func (s Source) View(e *envelope.Envelope) rules.View {
	var o rules.ViewOptions
	if s.Settings != nil {
		o = s.Settings(e)
	}
	o.Bot, o.Group = string(e.Bot), string(e.Group)
	return s.Rules.Current().View(o)
}
