package policy

import (
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// Engine runs the whole policy step for one message: content rules, kill switches, routing,
// the group's mode and the safety rails. It holds the rails' state, so one Engine serves every
// group of the process.
type Engine struct {
	rails *Rails
}

// NewEngine returns an engine with fresh rails reading time from c.
func NewEngine(c clock.Clock) *Engine { return &Engine{rails: NewRails(c)} }

// Rails returns the engine's safety rails, for the kill switches and circuit resets.
func (e *Engine) Rails() *Rails { return e.rails }

// Outcome is the policy step's result for one message.
type Outcome struct {
	Finding  Finding
	Decision Decision
	Plan     Plan // after the rails: Enqueue is what goes to the action queue
	Alerts   []Alert
}

// Route decides what happens to a message in group whose scored verdict (after the AI tier) is
// v, sent at sentAt by a member with standing st under settings s (DESIGN.md §8.3, §8.4, §8.6,
// §9.3). sentAt is the message's own timestamp, so windows such as policy.repeat count the
// same after a reconnect backlog; the rails' per-minute and per-hour windows run on the
// engine's clock, because they limit what the bot does now.
func (e *Engine) Route(group string, s Settings, env envelope.Envelope, v scoring.Verdict, st Standing, sentAt time.Time) Outcome {
	violations := EvaluateContent(s.Content, FactsOf(env), st)
	f := FindingFrom(v, TypeOf(env), violations, sentAt)
	f = e.rails.FilterFindingFor(f, s)
	d := Decide(s, f, st)
	p, alerts := e.rails.Admit(group, s, PlanFor(s, d))
	return Outcome{Finding: f, Decision: d, Plan: p, Alerts: alerts}
}
