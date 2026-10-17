package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/fake"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

func TestNewVerifierNeedsAModelForTheCacheKey(t *testing.T) {
	rc, _ := NewResultCache(cache.NewMemStore(), clock.NewFake(time.Unix(0, 0)))
	if _, err := NewVerifier(VerifierOptions{Classifier: fake.New(ai.Caps{}), Cache: rc}); err == nil {
		t.Fatal("NewVerifier accepted a classifier without a model; a model change would then hit old cache entries")
	}
}

type failingRecord struct{ *budget.MemStore }

func (failingRecord) AddUsage(context.Context, time.Time, string, budget.Usage) error {
	return errors.New("db down")
}

func TestUsageWriteFailureIsReportedButKeepsTheVerdict(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	s := &script{queue: []answer{{text: verdictJSON("spam", 0.95)}}}
	rc, _ := NewResultCache(cache.NewMemStore(), clk)
	b, _ := budget.New(failingRecord{budget.NewMemStore()}, clk, budget.Limits{}, nil, nil)
	v, err := NewVerifier(VerifierOptions{Classifier: NewClassifier(s, Options{Model: "m"}), Cache: rc, Budget: b})
	if err != nil {
		t.Fatal(err)
	}
	out := v.Verify(t.Context(), uncertain("x"))
	if out.Verdict.Source != scoring.SourceAI || out.Err == nil {
		t.Fatalf("Verify() = %+v, want the AI verdict plus the usage-write error for logging (GO-002)", out)
	}
}
