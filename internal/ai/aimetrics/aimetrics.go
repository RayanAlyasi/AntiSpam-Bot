// Package aimetrics holds the Prometheus metrics of the AI tier (DESIGN.md §7.4 cost control 6,
// §16): calls by provider/model/outcome, calls per 1,000 messages (as two counters to divide),
// tokens, cost, latency, cache and cluster-reuse hits, invalid-output retries, verdict labels
// (the share of "unsure") and budget remaining. Per-group cost lives in ai_usage, not in labels,
// to keep metric cardinality bounded.
package aimetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
)

// Call outcomes for CallsTotal.
const (
	OutcomeOK      = "ok"      // a parseable verdict came back
	OutcomeInvalid = "invalid" // the output failed validation
	OutcomeError   = "error"   // timeout, transport or provider error
)

// Reuse kinds for HitsTotal.
const (
	HitCache   = "cache"   // the 30-day result cache answered
	HitCluster = "cluster" // a copy in the same SimHash cluster or campaign reused the verdict
)

// Metrics are the AI tier's collectors.
type Metrics struct {
	Messages        prometheus.Counter     // messages that reached detection; calls/messages*1000 is the AI rate
	Calls           *prometheus.CounterVec // provider, model, outcome
	Tokens          *prometheus.CounterVec // provider, model, kind (input|output|cache_read|cache_write)
	CostMicros      *prometheus.CounterVec // provider, model
	Latency         *prometheus.HistogramVec
	Hits            *prometheus.CounterVec // kind (cache|cluster)
	InvalidRetries  prometheus.Counter
	Verdicts        *prometheus.CounterVec // label (spam|not_spam|unsure)
	BudgetLeft      *prometheus.GaugeVec   // scope
	BudgetDenials   *prometheus.CounterVec // scope
	StillUncertains *prometheus.CounterVec // reason: why an item went to admins without an AI verdict
}

var _ budget.Observer = (*Metrics)(nil)

// New creates the collectors and registers them with reg (nil registers nothing, for tests).
func New(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		Messages: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "antispam_ai_messages_seen_total", Help: "Messages that reached detection; divide AI calls by this for the AI rate.",
		}),
		Calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "antispam_ai_calls_total", Help: "AI provider calls by provider, model and outcome.",
		}, []string{"provider", "model", "outcome"}),
		Tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "antispam_ai_tokens_total", Help: "AI tokens by provider, model and kind.",
		}, []string{"provider", "model", "kind"}),
		CostMicros: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "antispam_ai_cost_micros_total", Help: "Estimated AI cost in millionths of the configured currency.",
		}, []string{"provider", "model"}),
		Latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "antispam_ai_call_duration_seconds", Help: "AI provider call latency.",
			Buckets: []float64{0.25, 0.5, 1, 2, 3, 5, 10, 20, 40},
		}, []string{"provider", "model"}),
		Hits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "antispam_ai_reuse_hits_total", Help: "Verdicts served without a provider call, by kind.",
		}, []string{"kind"}),
		InvalidRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "antispam_ai_invalid_output_retries_total", Help: "Retries after invalid AI output.",
		}),
		Verdicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "antispam_ai_verdicts_total", Help: "AI verdicts by label.",
		}, []string{"label"}),
		BudgetLeft: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "antispam_ai_budget_remaining_calls", Help: "Calls left in the most recently used budget window, by scope.",
		}, []string{"scope"}),
		BudgetDenials: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "antispam_ai_budget_denied_total", Help: "AI calls skipped because a budget was exhausted, by scope.",
		}, []string{"scope"}),
		StillUncertains: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "antispam_ai_still_uncertain_total", Help: "Uncertain items sent to admins without a confident AI verdict, by reason.",
		}, []string{"reason"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{
			m.Messages, m.Calls, m.Tokens, m.CostMicros, m.Latency, m.Hits,
			m.InvalidRetries, m.Verdicts, m.BudgetLeft, m.BudgetDenials, m.StillUncertains,
		} {
			if err := reg.Register(c); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

// ObserveCall records one provider call.
func (m *Metrics) ObserveCall(provider, model, outcome string, latency time.Duration, tokens map[string]int64, costMicros int64) {
	m.Calls.WithLabelValues(provider, model, outcome).Inc()
	m.Latency.WithLabelValues(provider, model).Observe(latency.Seconds())
	for kind, n := range tokens {
		if n > 0 {
			m.Tokens.WithLabelValues(provider, model, kind).Add(float64(n))
		}
	}
	if costMicros > 0 {
		m.CostMicros.WithLabelValues(provider, model).Add(float64(costMicros))
	}
}

// BudgetRemaining implements budget.Observer.
func (m *Metrics) BudgetRemaining(scope budget.Scope, remaining int) {
	m.BudgetLeft.WithLabelValues(string(scope)).Set(float64(remaining))
}

// BudgetDenied implements budget.Observer.
func (m *Metrics) BudgetDenied(scope budget.Scope) {
	m.BudgetDenials.WithLabelValues(string(scope)).Inc()
	m.BudgetLeft.WithLabelValues(string(scope)).Set(0)
}
