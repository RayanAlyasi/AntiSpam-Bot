// Package budget enforces the AI call budgets (DESIGN.md §7.4, cost control 4): calls per group
// per day and per instance per day and month. A call is reserved before it is made; when any
// budget is exhausted the caller sends the item straight to admins and nothing is deleted. The
// operator is alerted once per budget window at 80%. Usage lives in ai_usage behind Store, so
// this package never imports pgx.
package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Limits are call budgets. Zero means unlimited.
type Limits struct {
	GroupPerDay      int // detection.ai.daily_budget_calls per group (default 200)
	InstancePerDay   int
	InstancePerMonth int
}

// DefaultLimits are used when the configuration sets none: 200 calls per group per day
// (DESIGN.md §7.4) and no instance cap.
func DefaultLimits() Limits { return Limits{GroupPerDay: 200} }

// Validate rejects negative limits.
func (l Limits) Validate() error {
	if l.GroupPerDay < 0 || l.InstancePerDay < 0 || l.InstancePerMonth < 0 {
		return errors.New("budget: limits must not be negative")
	}
	return nil
}

// Scope names one budget.
type Scope string

// The three budgets.
const (
	ScopeGroupDay      Scope = "group_day"
	ScopeInstanceDay   Scope = "instance_day"
	ScopeInstanceMonth Scope = "instance_month"
)

// Counts are the calls already made in each window, including the reservation just taken.
type Counts struct {
	GroupDay, InstanceDay, InstanceMonth int
}

// Usage is what one provider call cost.
type Usage struct {
	InputTokens, OutputTokens int64 // input includes cache reads and writes
	CostMicros                int64 // millionths of the configured currency unit
}

// Store persists ai_usage(day, group_jid, calls, input_tokens, output_tokens, cost_micros).
type Store interface {
	// Reserve atomically counts one call for (day, group) if, after counting it, no limit is
	// exceeded. It returns the counts and whether the call was reserved; on denial nothing is
	// counted. Reservations must be serialized instance-wide (in Postgres, an advisory or row
	// lock taken before reading the counts), so concurrent callers never push a count past its
	// limit and each returned count is unique: the 80% alert relies on exactly one reservation
	// per window reaching the line.
	Reserve(ctx context.Context, day time.Time, group string, lim Limits) (Counts, bool, error)
	// AddUsage adds tokens and cost to (day, group) without counting a call.
	AddUsage(ctx context.Context, day time.Time, group string, u Usage) error
}

// Alerter notifies the operator (F-017 sinks). key deduplicates; the budget raises each key once.
type Alerter interface {
	Alert(ctx context.Context, key, message string)
}

// Observer receives budget metrics; Metrics implements it. Nil is allowed.
type Observer interface {
	BudgetRemaining(scope Scope, remaining int)
	BudgetDenied(scope Scope)
}

// Decision is the result of Allow.
type Decision struct {
	Allowed   bool
	Exhausted Scope // the first exhausted budget when not allowed
	Counts    Counts
}

// Budget reserves AI calls against the limits.
type Budget struct {
	store   Store
	clock   clock.Clock
	limits  Limits
	alerter Alerter
	obs     Observer
}

// New builds a Budget. alerter and obs may be nil.
func New(store Store, clk clock.Clock, limits Limits, alerter Alerter, obs Observer) (*Budget, error) {
	if store == nil || clk == nil {
		return nil, errors.New("budget: store and clock are required")
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Budget{store: store, clock: clk, limits: limits, alerter: alerter, obs: obs}, nil
}

// Day is the UTC calendar day that t falls in; budget windows are UTC days and months.
func Day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Allow reserves one call for group. A denied decision means: make no provider call, send the
// item to admins, delete nothing. Every provider request needs its own Allow, including the
// invalid-output retry; cache and cluster-reuse hits need none.
func (b *Budget) Allow(ctx context.Context, group string) (Decision, error) {
	day := Day(b.clock.Now())
	counts, ok, err := b.store.Reserve(ctx, day, group, b.limits)
	if err != nil {
		return Decision{}, fmt.Errorf("reserve ai call for %s: %w", group, err)
	}
	d := Decision{Allowed: ok, Counts: counts}
	if !ok {
		d.Exhausted = b.exhausted(counts)
		if b.obs != nil {
			b.obs.BudgetDenied(d.Exhausted)
		}
		return d, nil
	}
	b.observe(counts)
	b.alertAt80(ctx, day, group, counts)
	return d, nil
}

// Record adds the tokens and cost of a finished call.
func (b *Budget) Record(ctx context.Context, group string, u Usage) error {
	if err := b.store.AddUsage(ctx, Day(b.clock.Now()), group, u); err != nil {
		return fmt.Errorf("record ai usage for %s: %w", group, err)
	}
	return nil
}

type window struct {
	scope Scope
	used  int
	limit int
}

func (b *Budget) windows(c Counts) []window {
	return []window{
		{ScopeGroupDay, c.GroupDay, b.limits.GroupPerDay},
		{ScopeInstanceDay, c.InstanceDay, b.limits.InstancePerDay},
		{ScopeInstanceMonth, c.InstanceMonth, b.limits.InstancePerMonth},
	}
}

// exhausted names the first budget with no call left (counts are the current, unreserved ones).
func (b *Budget) exhausted(c Counts) Scope {
	for _, w := range b.windows(c) {
		if w.limit > 0 && w.used >= w.limit {
			return w.scope
		}
	}
	return ScopeGroupDay
}

func (b *Budget) observe(c Counts) {
	if b.obs == nil {
		return
	}
	for _, w := range b.windows(c) {
		if w.limit > 0 {
			b.obs.BudgetRemaining(w.scope, w.limit-w.used)
		}
	}
}

// alertAt80 alerts when this reservation is the one that reached 80% of a budget. Reservations
// are atomic and count one call each, so exactly one caller per window crosses the line; no
// extra state is needed, and a restart cannot raise a second alert.
func (b *Budget) alertAt80(ctx context.Context, day time.Time, group string, c Counts) {
	if b.alerter == nil {
		return
	}
	for _, w := range b.windows(c) {
		if w.limit <= 0 || !crosses80(w.used, w.limit) {
			continue
		}
		var key, msg string
		switch w.scope {
		case ScopeGroupDay:
			key = fmt.Sprintf("ai_budget:%s:%s:%s", w.scope, day.Format(time.DateOnly), group)
			msg = fmt.Sprintf("AI budget at 80%% for group %s today: %d of %d calls", group, w.used, w.limit)
		case ScopeInstanceDay:
			key = fmt.Sprintf("ai_budget:%s:%s", w.scope, day.Format(time.DateOnly))
			msg = fmt.Sprintf("AI budget at 80%% for the instance today: %d of %d calls", w.used, w.limit)
		case ScopeInstanceMonth:
			key = fmt.Sprintf("ai_budget:%s:%s", w.scope, day.Format("2006-01"))
			msg = fmt.Sprintf("AI budget at 80%% for the instance this month: %d of %d calls", w.used, w.limit)
		}
		b.alerter.Alert(ctx, key, msg)
	}
}

// crosses80 reports whether the call that brought the count to used reached 80% of limit:
// used-1 calls were below the line and used calls are at or above it.
func crosses80(used, limit int) bool {
	at := func(n int) bool { return n*5 >= limit*4 } // n/limit >= 0.8 without floats
	return used >= 1 && at(used) && !at(used-1)
}
