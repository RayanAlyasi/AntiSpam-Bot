package ai_test

import (
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/fake"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/gateway"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/safety"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// t0 is a fixed "now"; nothing here reads the wall clock.
var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const testGroup = "120363000000000001@g.us"

var (
	namedPresets = []policy.Preset{policy.PresetRelaxed, policy.PresetStandard, policy.PresetStrict, policy.PresetAskAdmins}
	allEnforce   = []policy.EnforcementMode{policy.EnforceAuto, policy.EnforceAskDeleteFirst, policy.EnforceAskBeforeAction}
	allRoles     = []policy.Role{policy.RoleMember, policy.RoleAdmin, policy.RoleSuperAdmin}
	newMember    = policy.Standing{Role: policy.RoleMember, New: true, Trust: 0}
)

// fataler is the part of *testing.T and *rapid.T the helpers need.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// met is a track record that has been earned (only possible from v0.4 on).
type met struct{}

func (met) AIMet() bool { return true }

// notMet is a caller-supplied unmet track record, distinct from safety.Unmet.
type notMet struct{}

func (notMet) AIMet() bool { return false }

var _ safety.TrackRecord = met{}

// testWeights is a tiny weight table: no bias, every signal keeps its own weight.
var testWeights = scoring.Weights{Version: "acceptance-1", Bias: 0}

// scored runs the public scoring path so the verifier sees a real pre-AI verdict.
func scored(signals ...scoring.Signal) scoring.Verdict {
	return scoring.Classify(scoring.Score(testWeights, signals), scoring.DefaultThresholds())
}

// uncertainVerdict is suspicious by heuristics only: p = sigmoid(1.5) ≈ 0.82 ≥ T_review 0.60.
func uncertainVerdict() scoring.Verdict {
	return scored(scoring.Signal{Code: "url.new_domain", Weight: 1.5, Evidence: "link to a new domain"})
}

// certainVerdict carries known-spam evidence.
func certainVerdict() scoring.Verdict {
	return scored(scoring.Signal{Code: "url.blocklisted", Weight: 4, Known: true, Evidence: "blocklisted link"})
}

// cleanVerdict is below T_review: p = sigmoid(-3) ≈ 0.05.
func cleanVerdict() scoring.Verdict {
	return scored(scoring.Signal{Code: "text.greeting", Weight: -3, Evidence: "greeting"})
}

func item(v scoring.Verdict, text string) gateway.Item {
	return gateway.Item{
		Group:  testGroup,
		Scored: v,
		Request: ai.ClassifyRequest{Message: ai.Message{
			Kind:  "text",
			Texts: []ai.Text{{Source: "body", Text: text}},
		}},
		NormText:    text,
		ContextHash: "no-context",
		AIEnabled:   true,
		Thresholds:  scoring.DefaultThresholds(),
	}
}

func spamVerdict(conf float64) ai.Verdict {
	return ai.Verdict{Label: ai.LabelSpam, Confidence: conf, Categories: []string{"crypto"}, Reason: "crypto investment pitch"}
}

func notSpamVerdict(conf float64) ai.Verdict {
	return ai.Verdict{Label: ai.LabelNotSpam, Confidence: conf, Categories: []string{}, Reason: "ordinary chat"}
}

type verifierOpts struct {
	classifier ai.AIClassifier
	budget     *budget.Budget
}

func newVerifier(t fataler, o verifierOpts) *gateway.Verifier {
	t.Helper()
	clk := clock.NewFake(t0)
	c, err := gateway.NewResultCache(cache.NewMemStore(), clk)
	if err != nil {
		t.Fatalf("NewResultCache error = %v; want nil", err)
	}
	v, err := gateway.NewVerifier(gateway.VerifierOptions{
		Classifier: o.classifier, // nil means no AI is configured
		Model:      "test-model",
		Cache:      c,
		Budget:     o.budget,
	})
	if err != nil {
		t.Fatalf("NewVerifier error = %v; want nil", err)
	}
	return v
}

func fakeAnswering(rs ...fake.Response) *fake.Classifier {
	f := fake.New(ai.Caps{})
	f.Push(rs...)
	return f
}

// finding is the caller's mapping from scoring's verdict to the decision engine's input:
// bands and sources share their string values.
func finding(v scoring.Verdict, ct policy.ContentType) policy.Finding {
	f := policy.Finding{
		Band:   policy.Band(v.Band),
		Source: policy.Source(v.Source),
		Score:  v.Score.P,
		Type:   ct,
		At:     t0,
	}
	for _, s := range v.Score.Signals {
		f.Signals = append(f.Signals, policy.Signal{Code: s.Code, Weight: s.Applied, Known: s.Known})
	}
	return f
}

func mustPreset(t fataler, p policy.Preset) policy.Settings {
	t.Helper()
	s, err := policy.PresetSettings(p)
	if err != nil {
		t.Fatalf("PresetSettings(%q) error = %v; want nil (every §12.2 preset is valid)", p, err)
	}
	return s
}

func hasAction(d policy.Decision, k policy.ActionKind) bool {
	for _, a := range d.Actions() {
		if a.Kind == k {
			return true
		}
	}
	return false
}
