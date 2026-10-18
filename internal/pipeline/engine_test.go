package pipeline

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// wordAnalyzer flags texts containing "spam" as known spam and "maybe" as a suspicious heuristic.
type wordAnalyzer struct{}

func (wordAnalyzer) Name() string                                      { return "words" }
func (wordAnalyzer) Tier() detect.Tier                                 { return detect.T0 }
func (wordAnalyzer) Applies(*envelope.Envelope, *policy.Settings) bool { return true }
func (wordAnalyzer) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	var out []detect.Signal
	for _, t := range e.Texts {
		switch {
		case strings.Contains(t.Norm, "spam"):
			out = append(out, detect.Signal{Code: "test.known", Weight: 5, Known: true, Evidence: "known phrase"})
		case strings.Contains(t.Norm, "maybe"):
			out = append(out, detect.Signal{Code: "test.heuristic", Weight: 4, Evidence: "heuristic"})
		}
	}
	return out, nil
}

type fixedVerifier struct{ v *ai.Verdict }

func (f fixedVerifier) Verify(_ context.Context, _ *envelope.Envelope, _ policy.Settings, v scoring.Verdict, th scoring.Thresholds) scoring.Verdict {
	return scoring.Resolve(v, f.v, th)
}

func settings(t *testing.T, p policy.Preset, mode policy.Mode) policy.Settings {
	t.Helper()
	aiCell := p == "ai_cell"
	if aiCell {
		p = policy.PresetStrict
	}
	s, err := policy.PresetSettings(p)
	if aiCell { // an operator listed ai in the text cell: the early-AI safety rule must still hold
		c := s.Enforcement.AutobanMatrix[policy.TypeText]
		c.RequireSource = append(c.RequireSource, policy.SourceAI)
		s.Enforcement.AutobanMatrix[policy.TypeText] = c
	}
	if err != nil {
		t.Fatal(err)
	}
	s.Mode = mode
	return s
}

func TestEngineRoutes(t *testing.T) {
	newbie := Sender{}
	established := Sender{Known: true, Member: policy.MemberRecord{LID: "2@lid", Role: policy.RoleMember, JoinedAt: t0.AddDate(-1, 0, 0), PreExisting: true}}
	admin := Sender{Known: true, Member: policy.MemberRecord{LID: "3@lid", Role: policy.RoleAdmin, JoinedAt: t0.AddDate(-1, 0, 0), PreExisting: true}}
	tests := []struct {
		name     string
		text     string
		preset   policy.Preset
		mode     policy.Mode
		sender   Sender
		verifier Verifier
		aiOn     bool
		want     []ActionKind
		band     Band
		source   Source
	}{
		{"known spam from a new member: delete + ban", "buy spam now", policy.PresetStandard, policy.ModeEnforce, newbie, nil, false, []ActionKind{ActionDelete, ActionBan}, BandCertain, SourceKnown},
		{"known spam from an established member: delete + review", "buy spam now", policy.PresetStandard, policy.ModeEnforce, established, nil, false, []ActionKind{ActionDelete, ActionReview}, BandCertain, SourceKnown},
		{"known spam from an admin: nothing", "buy spam now", policy.PresetStandard, policy.ModeEnforce, admin, nil, false, nil, BandCertain, SourceKnown},
		{"shadow lists would-be actions", "buy spam now", policy.PresetStandard, policy.ModeShadow, newbie, nil, false, []ActionKind{ActionDelete, ActionBan}, BandCertain, SourceKnown},
		{"paused does nothing", "buy spam now", policy.PresetStandard, policy.ModePaused, newbie, nil, false, nil, BandCertain, SourceKnown},
		{"ask admins deletes and asks", "buy spam now", policy.PresetAskAdmins, policy.ModeEnforce, newbie, nil, false, []ActionKind{ActionDelete, ActionReview}, BandCertain, SourceKnown},
		{"normal message: nothing", "see you at five", policy.PresetStandard, policy.ModeEnforce, newbie, nil, false, nil, BandClean, SourceNone},
		{"heuristic without AI: review only", "maybe this", policy.PresetStandard, policy.ModeEnforce, newbie, nil, false, []ActionKind{ActionReview}, BandStillUncertain, SourceRules},
		{
			"heuristic, AI says spam: delete, ban goes to admins", "maybe this", policy.PresetStandard, policy.ModeEnforce, newbie,
			fixedVerifier{&ai.Verdict{Label: ai.LabelSpam, Confidence: 0.95}},
			true,
			[]ActionKind{ActionDelete, ActionReview},
			BandUncertain, SourceAI,
		},
		{
			"AI configured but off for the group: review only", "maybe this", policy.PresetStandard, policy.ModeEnforce, newbie,
			fixedVerifier{&ai.Verdict{Label: ai.LabelSpam, Confidence: 0.95}},
			false,
			[]ActionKind{ActionReview},
			BandStillUncertain, SourceRules,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			en, err := NewEngine(EngineConfig{
				Analyzers: []Analyzer{wordAnalyzer{}}, Weights: scoring.DefaultWeights(),
				Normalize: NormalizeTexts, Verify: tt.verifier, Clock: clock.NewFake(t0),
			})
			if err != nil {
				t.Fatal(err)
			}
			s := settings(t, tt.preset, tt.mode)
			s.Detection.AI.Enabled = tt.aiOn
			e := &envelope.Envelope{
				Group: "1@g.us", Sender: envelope.Identity{LID: "9@lid"}, MsgID: "M1", Kind: envelope.KindText,
				Texts: []envelope.TextSurface{{Source: "body", Raw: tt.text}},
			}
			d, err := en.Evaluate(t.Context(), e, time.Time{}, s, tt.sender)
			if err != nil {
				t.Fatal(err)
			}
			v := d.Verdict
			if !slices.Equal(v.Actions, tt.want) || v.Band != tt.band || v.Source != tt.source {
				t.Fatalf("got actions %v, band %s, source %s (route %s); want %v, %s, %s",
					v.Actions, v.Band, v.Source, v.Route, tt.want, tt.band, tt.source)
			}
			if tt.mode == policy.ModeShadow && len(d.Plan.Enqueue) != 0 {
				t.Fatalf("shadow mode enqueued %v; it must act on nothing (DESIGN §8.4)", d.Plan.Enqueue)
			}
			if v.Group != e.Group || v.Message != e.MsgID || v.Sender != "9@lid" || v.Mode != Mode(tt.mode) {
				t.Fatalf("verdict identifies %s/%s/%s/%s, want the message's", v.Group, v.Message, v.Sender, v.Mode)
			}
		})
	}
}

func TestEngineJustJoinedSignal(t *testing.T) {
	en, err := NewEngine(EngineConfig{Analyzers: []Analyzer{wordAnalyzer{}}, Weights: scoring.DefaultWeights(), Clock: clock.NewFake(t0)})
	if err != nil {
		t.Fatal(err)
	}
	e := &envelope.Envelope{
		Group: "1@g.us", Sender: envelope.Identity{LID: "9@lid"}, MsgID: "M1",
		Texts: []envelope.TextSurface{{Source: "body", Raw: "hello", Norm: "hello"}},
	}
	d, err := en.Evaluate(t.Context(), e, time.Time{}, settings(t, policy.PresetStandard, policy.ModeEnforce), Sender{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(d.Score.Signals, func(s scoring.Scored) bool { return s.Code == policy.SignalJustJoined }) {
		t.Fatalf("signals %+v lack the just-joined signal for a new sender (DESIGN §8.2)", d.Score.Signals)
	}
	if d.Verdict.Band != BandClean {
		t.Fatalf("band %s: the just-joined signal alone must not make a message suspicious", d.Verdict.Band)
	}
}
