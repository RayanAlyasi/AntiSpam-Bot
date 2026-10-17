package aimetrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
)

func TestMetricsRegisterAndRecord(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := New(reg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := New(reg); err == nil {
		t.Fatal("registering twice succeeded, want an AlreadyRegistered error")
	}
	m.ObserveCall("anthropic", "claude-haiku-5-5", OutcomeOK, 800*time.Millisecond,
		map[string]int64{"input": 50, "output": 20, "cache_read": 0}, 120)
	m.ObserveCall("anthropic", "claude-haiku-5-5", OutcomeError, 5*time.Second, nil, 0)
	m.BudgetRemaining(budget.ScopeGroupDay, 42)
	m.BudgetDenied(budget.ScopeInstanceDay)

	tests := []struct {
		name string
		c    prometheus.Collector
		want float64
	}{
		{"ok calls", m.Calls.WithLabelValues("anthropic", "claude-haiku-5-5", OutcomeOK), 1},
		{"error calls", m.Calls.WithLabelValues("anthropic", "claude-haiku-5-5", OutcomeError), 1},
		{"input tokens", m.Tokens.WithLabelValues("anthropic", "claude-haiku-5-5", "input"), 50},
		{"cost", m.CostMicros.WithLabelValues("anthropic", "claude-haiku-5-5"), 120},
		{"group budget left", m.BudgetLeft.WithLabelValues("group_day"), 42},
		{"instance budget left after denial", m.BudgetLeft.WithLabelValues("instance_day"), 0},
		{"denials", m.BudgetDenials.WithLabelValues("instance_day"), 1},
	}
	for _, tt := range tests {
		if got := testutil.ToFloat64(tt.c); got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
		}
	}
	if n := testutil.CollectAndCount(m.Tokens); n != 2 {
		t.Errorf("token series = %d, want 2 (zero counts are not emitted)", n)
	}
	if probs, err := testutil.GatherAndLint(reg); err != nil || len(probs) > 0 {
		t.Errorf("metric lint: %v %v", probs, err)
	}
}
