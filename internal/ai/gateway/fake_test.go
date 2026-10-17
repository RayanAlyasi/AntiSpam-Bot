package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/fake"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

func TestVerifierWithAnyClassifier(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	f := fake.New(ai.Caps{NativeSchema: true})
	f.Push(
		fake.Response{Verdict: ai.Verdict{Label: ai.LabelSpam, Confidence: 0.93, Categories: []string{"crypto"}, Reason: "r"}},
		fake.Response{Block: true},
	)
	rc, _ := NewResultCache(cache.NewMemStore(), clk)
	b, _ := budget.New(budget.NewMemStore(), clk, budget.Limits{GroupPerDay: 2}, nil, nil)
	v, err := NewVerifier(VerifierOptions{Classifier: f, Model: "fake", Cache: rc, Budget: b})
	if err != nil {
		t.Fatal(err)
	}
	if out := v.Verify(t.Context(), uncertain("a")); out.Verdict.Source != scoring.SourceAI || !out.Called {
		t.Fatalf("spam 0.93: %+v, want AI-verified", out)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if out := v.Verify(ctx, uncertain("b")); out.Reason != ReasonTimeout || out.Verdict.DeletesAsSpam() {
		t.Fatalf("blocked classifier: %+v, want still uncertain on timeout, nothing deleted", out)
	}
	if out := v.Verify(t.Context(), uncertain("c")); out.Reason != ReasonBudget || out.Called {
		t.Fatalf("third distinct message with a budget of 2: %+v, want budget, no call", out)
	}
	if f.Calls() != 2 {
		t.Fatalf("classifier calls = %d, want 2", f.Calls())
	}

	var none *Classifier // what New returns when the AI is off
	off, err := NewVerifier(VerifierOptions{Classifier: none})
	if err != nil {
		t.Fatal(err)
	}
	if out := off.Verify(t.Context(), uncertain("d")); out.Reason != ReasonAIOff {
		t.Fatalf("typed-nil classifier: %+v, want ai_off", out)
	}
}
