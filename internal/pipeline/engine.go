package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/safety"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Analyzer is a detection analyzer as the pipeline instantiates the framework.
type Analyzer = detect.Analyzer[envelope.Envelope, policy.Settings]

// Normalizer fills in the normalized form of every text surface and attaches extracted phone
// numbers (DESIGN.md §7.2, F-039). It must keep Raw untouched.
type Normalizer func(e *envelope.Envelope)

// Verifier is the AI check for the UNCERTAIN band (DESIGN.md §7.0, §7.4). It returns the resolved
// verdict (scoring.Resolve): AI off, out of budget, timed out, failed, unsure or not confident
// enough all mean STILL_UNCERTAIN, so the message goes to admins. AIVerifier is the gateway one.
type Verifier interface {
	Verify(ctx context.Context, e *envelope.Envelope, s policy.Settings, v scoring.Verdict, th scoring.Thresholds) scoring.Verdict
}

// Sender is what the engine knows about who sent a message in a group.
type Sender struct {
	Member policy.MemberRecord
	Known  bool // false: never seen before, so new (DESIGN.md §8.1 "New or unknown sender")
	IsBot  bool
}

// Engine is the storage-free core of the fast lane: envelope in, decision and plan out. It never
// performs I/O itself; the Pipeline around it loads the inputs and writes the outputs.
type Engine struct {
	runner    *detect.Runner[envelope.Envelope, policy.Settings]
	normalize Normalizer
	verify    Verifier
	policy    *policy.Engine
	track     safety.TrackRecord
	clock     clock.Clock
}

// EngineConfig configures NewEngine. Clock is required; zero values of the rest mean default
// weights and thresholds, no normalizer (texts are matched as received), no AI and a fresh
// policy engine.
type EngineConfig struct {
	Analyzers  []Analyzer
	Weights    scoring.Weights
	Thresholds scoring.Thresholds
	Normalize  Normalizer
	Verify     Verifier
	Policy     *policy.Engine
	// Track is the AI's track record (DESIGN.md §9.2). Nil means unmet, as in v0.1: AI evidence
	// never auto-bans.
	Track safety.TrackRecord
	Clock clock.Clock
}

// NewEngine builds the engine and its analyzer registry.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Clock == nil {
		return nil, errors.New("pipeline: engine needs a clock")
	}
	reg := detect.NewRegistry[envelope.Envelope, policy.Settings]()
	for _, a := range cfg.Analyzers {
		if err := reg.Register(a); err != nil {
			return nil, fmt.Errorf("pipeline: register analyzer: %w", err)
		}
	}
	runner, err := detect.NewRunner(reg, detect.Config{Weights: cfg.Weights, Thresholds: cfg.Thresholds})
	if err != nil {
		return nil, fmt.Errorf("pipeline: runner: %w", err)
	}
	pe := cfg.Policy
	if pe == nil {
		pe = policy.NewEngine(cfg.Clock)
	}
	track := cfg.Track
	if track == nil {
		track = safety.Unmet{}
	}
	return &Engine{runner: runner, normalize: cfg.Normalize, verify: cfg.Verify, policy: pe, track: track, clock: cfg.Clock}, nil
}

// Rails returns the safety rails the engine applies, so callers can note bans and kill rules.
func (en *Engine) Rails() *policy.Rails { return en.policy.Rails() }

// Decision is everything the engine concluded about one message.
type Decision struct {
	Envelope *envelope.Envelope
	Score    scoring.Result
	Finding  policy.Finding
	Decision policy.Decision
	Plan     policy.Plan // after the safety rails
	Alerts   []policy.Alert
	Verdict  Verdict
}

// Evaluate runs one message through the fast lane: normalize, T0/T1 analyzers, scoring, the AI
// check for the UNCERTAIN band, then the policy step (content rules, routing, the group's mode
// and the safety rails). at is the message's own timestamp (zero: now). It doesn't modify
// settings.
func (en *Engine) Evaluate(ctx context.Context, e *envelope.Envelope, at time.Time, s policy.Settings, snd Sender) (Decision, error) {
	now := en.clock.Now()
	if en.normalize != nil {
		en.normalize(e)
	}
	member := snd.Member
	if !snd.Known {
		member = policy.MemberRecord{LID: string(e.Sender.LID), Role: policy.RoleMember, JoinedAt: now}
	}
	standing := policy.StandingOf(member, snd.IsBot, s, now)

	lane := detect.Fast()
	if !member.PreExisting {
		if sig, ok := policy.JustJoinedScoringSignal(s.NewMembers.JustJoined, member.JoinedAt, now); ok {
			lane.Prior = append(lane.Prior, sig)
		}
	}
	out, err := en.runner.Run(ctx, e, &s, lane)
	if err != nil {
		return Decision{}, fmt.Errorf("pipeline: analyze %s: %w", e.MsgID, err)
	}
	th := thresholdsOf(s)
	v := scoring.Classify(out.Verdict.Score, th)
	if v.Band == scoring.BandUncertain {
		if en.verify != nil && s.Detection.AI.Enabled {
			v = en.verify.Verify(ctx, e, s, v, th)
		} else {
			v = scoring.Resolve(v, nil, th)
		}
	}
	sentAt := now
	if !at.IsZero() {
		sentAt = at
	}
	o := en.route(s, *e, v, standing, sentAt)
	return Decision{
		Envelope: e,
		Score:    v.Score,
		Finding:  o.Finding,
		Decision: o.Decision,
		Plan:     o.Plan,
		Alerts:   o.Alerts,
		Verdict:  verdictOf(e, s, o.Finding, o.Decision, o.Plan, v.Score),
	}, nil
}

// route is policy.Engine.Route with the early-AI safety rule (DESIGN.md §9.2, F-112): content
// rules, kill switches, safety.Decide, the group's mode and the safety rails.
func (en *Engine) route(s policy.Settings, env envelope.Envelope, v scoring.Verdict, st policy.Standing, sentAt time.Time) policy.Outcome {
	rails := en.policy.Rails()
	violations := policy.EvaluateContent(s.Content, policy.FactsOf(env), st)
	f := rails.FilterFindingFor(policy.FindingFrom(v, policy.TypeOf(env), violations, sentAt), s)
	d := safety.Decide(s, f, st, en.track)
	p, alerts := rails.Admit(string(env.Group), s, policy.PlanFor(s, d))
	return policy.Outcome{Finding: f, Decision: d, Plan: p, Alerts: alerts}
}

// thresholdsOf takes T_review and the AI confidence from the group's settings.
func thresholdsOf(s policy.Settings) scoring.Thresholds {
	th := scoring.DefaultThresholds()
	if r := s.Detection.Thresholds.Review; r > 0 {
		th.Review = r
	}
	if c := s.Detection.AI.SpamConfidence; c > 0 {
		th.AIConfidence = c
	}
	return th
}

func verdictOf(e *envelope.Envelope, s policy.Settings, f policy.Finding, d policy.Decision, p policy.Plan, r scoring.Result) Verdict {
	v := Verdict{
		Group:       e.Group,
		Message:     e.MsgID,
		Sender:      senderJID(e),
		Mode:        Mode(s.Mode),
		Band:        Band(f.Band),
		Source:      Source(f.Source),
		Route:       Route(d.Route),
		Explanation: r.Explain(),
	}
	if v.Route == "" {
		v.Route = RouteNone
	}
	// The verdict lists what the bot does: the enqueued actions, or in shadow mode what it
	// would have done. A paused group does nothing.
	acts := p.Enqueue
	if p.Mode == policy.DetectionShadow {
		acts = p.Would
	}
	for _, a := range acts {
		v.Actions = append(v.Actions, ActionKind(a.Kind))
	}
	return v
}

func senderJID(e *envelope.Envelope) wa.JID {
	if e.Sender.LID != "" {
		return e.Sender.LID
	}
	return e.Sender.PN
}

// NormalizeTexts fills in every text surface's normalized forms with the §7.2 normalizer (NFKC,
// invisible characters, confusables, leetspeak, Arabic folds, …) and attaches the phone numbers
// advertised in the text and contact cards (F-039, textnorm.Prepare), leaving Raw untouched for
// evidence. Numbers written without a country code aren't read, since v0.1 groups have no
// default country setting yet.
func NormalizeTexts(e *envelope.Envelope) { textnorm.Prepare(e, "") }
