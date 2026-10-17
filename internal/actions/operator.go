package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Operator holds the operator's controls over the queue: the global kill switch, closing a
// breaker and changing a bot's ceiling (DESIGN.md §10.3, §10.5). Every change is audited.
// Executors pick changes up from the store within Config.PollInterval.
type Operator struct {
	Store Store
	Audit Auditor
	Clock clock.Clock // clock.Real when nil
}

// Pause turns the global kill switch on: no action runs on any bot until Resume.
func (o Operator) Pause(ctx context.Context, actor, reason string) error {
	if err := o.check(); err != nil {
		return err
	}
	if _, err := o.Store.UpdateControl(ctx, GlobalScope, func(c *Control) {
		c.Paused, c.PausedBy, c.PausedAt = true, actor, o.now()
	}); err != nil {
		return fmt.Errorf("pause actions: %w", err)
	}
	return o.audit(ctx, actor, "actions.pause", map[string]any{"paused": true, "reason": reason})
}

// Resume turns the global kill switch off.
func (o Operator) Resume(ctx context.Context, actor string) error {
	if err := o.check(); err != nil {
		return err
	}
	if _, err := o.Store.UpdateControl(ctx, GlobalScope, func(c *Control) {
		c.Paused, c.PausedBy, c.PausedAt = false, actor, o.now()
	}); err != nil {
		return fmt.Errorf("resume actions: %w", err)
	}
	return o.audit(ctx, actor, "actions.resume", map[string]any{"paused": false})
}

// CloseBreaker closes bot's circuit breaker. The rate stays slowed down and recovers as usual.
func (o Operator) CloseBreaker(ctx context.Context, bot wa.JID, actor string) error {
	if err := o.check(); err != nil {
		return err
	}
	var reason string
	if _, err := o.Store.UpdateControl(ctx, string(bot), func(c *Control) {
		reason = c.BreakerReason
		c.BreakerOpen, c.BreakerReason, c.BreakerClosedAt = false, "", o.now()
	}); err != nil {
		return fmt.Errorf("close breaker of %s: %w", bot, err)
	}
	return o.audit(ctx, actor, "actions.breaker_close", map[string]any{"bot": string(bot), "was": reason})
}

// SetCeiling sets bot's write ceiling to perMin writes/min (0 returns to the configured one).
// Raising it is risky: it increases the chance of a ban (DESIGN.md §10.3). The current rate
// doesn't jump: it keeps recovering by 1 write/min per calm period, up to the new ceiling.
func (o Operator) SetCeiling(ctx context.Context, bot wa.JID, actor string, perMin int) error {
	if err := o.check(); err != nil {
		return err
	}
	if perMin < 0 {
		return fmt.Errorf("%w: ceiling %d is negative", ErrInvalid, perMin)
	}
	var old int
	if _, err := o.Store.UpdateControl(ctx, string(bot), func(c *Control) {
		old = c.Ceiling
		c.Ceiling = perMin
	}); err != nil {
		return fmt.Errorf("set ceiling of %s: %w", bot, err)
	}
	return o.audit(ctx, actor, "actions.ceiling", map[string]any{"bot": string(bot), "from": old, "to": perMin})
}

// check refuses a change that couldn't be audited.
func (o Operator) check() error {
	if o.Store == nil || o.Audit == nil {
		return errors.New("actions operator needs a store and an auditor: queue controls are always audited")
	}
	return nil
}

func (o Operator) audit(ctx context.Context, actor, action string, diff map[string]any) error {
	if err := o.Audit.Audit(ctx, AuditEntry{Actor: actor, Action: action, Diff: diff}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

func (o Operator) now() time.Time {
	if o.Clock == nil {
		return clock.Real{}.Now()
	}
	return o.Clock.Now()
}
